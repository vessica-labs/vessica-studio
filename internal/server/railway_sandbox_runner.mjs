import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";

let sandbox;
let destroyPromise;
let cancellationReason = "";
let resolveCancellation;
const cancellation = new Promise(resolve => { resolveCancellation = resolve; });
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    if (cancellationReason) return;
    cancellationReason = `dispatcher received ${signal}`;
    resolveCancellation();
    void destroySandbox();
  });
}

const input = await new Promise((resolve, reject) => {
  let body = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", chunk => { body += chunk; });
  process.stdin.on("end", () => {
    try { resolve(JSON.parse(body)); } catch (error) { reject(error); }
  });
  process.stdin.on("error", reject);
});

const metadata = {
  sandboxId: "",
  status: "",
  networkIsolation: "",
  exitCode: null,
  timedOut: false,
  truncated: false,
  destroyed: false,
  changes: [],
};

try {
  const sdkPath = process.env.VSTD_RAILWAY_SDK;
  if (!sdkPath) throw new Error("VSTD_RAILWAY_SDK is not configured");
  const { Sandbox } = await import(sdkPath);
  sandbox = await Sandbox.create({
    idleTimeoutMinutes: input.idleTimeoutMinutes,
    networkIsolation: "ISOLATED",
    env: { CODEX_API_KEY: input.codexKeyReference },
  });
  throwIfCancelled();
  metadata.sandboxId = sandbox.id;
  metadata.status = sandbox.status;
  metadata.networkIsolation = sandbox.networkIsolation;
  console.error(`sandbox created id=${sandbox.id} network=${sandbox.networkIsolation}`);
  if (sandbox.networkIsolation !== "ISOLATED") {
    throw new Error(`sandbox network isolation mismatch: ${sandbox.networkIsolation}`);
  }

  for (const file of input.inputs) {
    await cancellable(sandbox.files.write(file.remotePath, () => fs.createReadStream(file.localPath), { mode: file.mode }));
  }
  const forbiddenEnvironment = await cancellable(sandbox.exec(
    "for key in DATABASE_URL PGHOST PGPASSWORD TELNYX_API_KEY RESEND_API_KEY AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY S3_ACCESS_KEY S3_SECRET_KEY RAILWAY_TOKEN RAILWAY_API_TOKEN; do if printenv \"$key\" >/dev/null 2>&1; then echo \"forbidden sandbox environment: $key\" >&2; exit 41; fi; done",
    { timeoutSec: 30 },
  ));
  if (forbiddenEnvironment.exitCode !== 0) {
    throw new Error(forbiddenEnvironment.stderr || "sandbox received forbidden environment variables");
  }
  await cancellable(sandbox.files.write("/workspace/.vstd/prompt.txt", input.prompt, { mode: 0o600 }));
  const remoteImages = input.remoteImages ?? [];
  const imageArgs = remoteImages.map(image => ` -i ${shellQuote(image)}`).join("");
  const runScript = `#!/usr/bin/env bash
set -uo pipefail
export PATH="/workspace/bin:$PATH"
prompt="$(cat /workspace/.vstd/prompt.txt)"
exec codex exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check --ephemeral -C /workspace${imageArgs} "$prompt"
`;
  await cancellable(sandbox.files.write("/workspace/.vstd/run-agent.sh", runScript, { mode: 0o700 }));
  const execution = await cancellable(sandbox.exec("/workspace/.vstd/run-agent.sh", {
    cwd: "/workspace",
    timeoutSec: input.timeoutSeconds,
  }));
  metadata.exitCode = execution.exitCode;
  metadata.timedOut = execution.timedOut;
  metadata.truncated = execution.truncated;
  const output = `${execution.stderr || ""}${execution.stderr && execution.stdout ? "\n" : ""}${execution.stdout || ""}`;
  fs.writeFileSync(path.join(input.resultDir, "agent.log"), output, { mode: 0o600 });

  if (execution.exitCode === 0 && !execution.timedOut) {
    const original = new Map(input.inputs.filter(file => file.relative).map(file => [file.relative, file.sha256]));
    let totalBytes = 0;
    for (const prefix of input.outputPrefixes) {
      const remoteRoot = `/workspace/${prefix.replace(/\/$/, "")}`;
      if (!(await cancellable(sandbox.files.exists(remoteRoot)))) continue;
      for (const remotePath of await cancellable(listFiles(sandbox, remoteRoot))) {
        const relative = remotePath.slice("/workspace/".length);
        if (!isAllowed(relative, input.outputPrefixes)) throw new Error(`sandbox output escaped scope: ${relative}`);
        if (isGeneratedOutput(relative)) continue;
        const stat = await cancellable(sandbox.files.stat(remotePath));
        const bytes = await cancellable(sandbox.files.read(remotePath, { format: "bytes" }));
        const digest = crypto.createHash("sha256").update(bytes).digest("hex");
        if (original.get(relative) === digest) continue;
        totalBytes += stat.size;
        if (metadata.changes.length >= input.maxChanges || totalBytes > input.maxChangeBytes) {
          throw new Error("sandbox output exceeded configured limits");
        }
        const destination = safeLocalJoin(path.join(input.resultDir, "files"), relative);
        fs.mkdirSync(path.dirname(destination), { recursive: true, mode: 0o700 });
        fs.writeFileSync(destination, bytes, { mode: 0o600 });
        metadata.changes.push({ path: relative, sha256: digest, size: bytes.byteLength });
      }
    }
  }
} catch (error) {
  metadata.error = error instanceof Error ? error.message : String(error);
} finally {
  await destroySandbox();
}

process.stdout.write(JSON.stringify(metadata));

function throwIfCancelled() {
  if (cancellationReason) throw new Error(cancellationReason);
}

function cancellable(operation) {
  return Promise.race([
    operation,
    cancellation.then(() => { throw new Error(cancellationReason); }),
  ]);
}

async function destroySandbox() {
  if (!sandbox) return;
  if (!destroyPromise) {
    destroyPromise = (async () => {
      try {
        await sandbox.destroy();
        metadata.destroyed = true;
        console.error(`sandbox destroyed id=${sandbox.id}`);
      } catch (error) {
        metadata.destroyError = error instanceof Error ? error.message : String(error);
      }
    })();
  }
  await destroyPromise;
}

async function listFiles(sandbox, directory) {
  const output = [];
  for (const entry of await sandbox.files.list(directory)) {
    const child = `${directory}/${entry.name}`;
    if (entry.isDir) output.push(...await listFiles(sandbox, child));
    else output.push(child);
  }
  return output;
}

function isAllowed(relative, prefixes) {
  if (!relative || relative.startsWith("/") || relative.includes("\0")) return false;
  const normalized = path.posix.normalize(relative);
  if (normalized !== relative || normalized.startsWith("../")) return false;
  return prefixes.some(prefix => normalized.startsWith(prefix) && normalized.length > prefix.length);
}

function isGeneratedOutput(relative) {
  return /^decks\/[^/]+\/build(?:\/|$)/.test(relative);
}

function safeLocalJoin(root, relative) {
  if (!isAllowed(relative, input.outputPrefixes)) throw new Error(`unsafe result path: ${relative}`);
  const destination = path.resolve(root, ...relative.split("/"));
  const base = path.resolve(root) + path.sep;
  if (!destination.startsWith(base)) throw new Error(`unsafe result path: ${relative}`);
  return destination;
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", `'"'"'`)}'`;
}
