import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { copyFile, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { request } from "node:http";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

function get(port, path) {
  return new Promise((resolve, reject) => {
    const req = request({ hostname: "127.0.0.1", port, path }, (response) => {
      let body = "";
      response.setEncoding("utf8");
      response.on("data", (chunk) => (body += chunk));
      response.on("end", () => resolve({ status: response.statusCode, body }));
    });
    req.on("error", reject);
    req.end();
  });
}

test("invalid request paths cannot terminate the static docs server", { timeout: 10_000 }, async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "vstd-docs-server-"));
  await mkdir(join(directory, "dist"));
  await writeFile(join(directory, "dist/index.html"), "docs fixture");
  await copyFile(new URL("./server.mjs", import.meta.url), join(directory, "server.mjs"));
  const reservation = createServer();
  reservation.listen(0, "127.0.0.1");
  await once(reservation, "listening");
  const port = reservation.address().port;
  await new Promise((resolve) => reservation.close(resolve));
  const server = spawn(process.execPath, [join(directory, "server.mjs")], {
    env: { ...process.env, PORT: String(port) },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let errors = "";
  server.stderr.on("data", (chunk) => (errors += chunk));
  t.after(async () => {
    if (server.exitCode === null) {
      server.kill();
      await once(server, "exit");
    }
    await rm(directory, { recursive: true, force: true });
  });
  await Promise.race([
    once(server.stdout, "data"),
    once(server, "exit").then(() => { throw new Error(errors); }),
  ]);
  assert.deepEqual(await get(port, "/"), { status: 200, body: "docs fixture" });
  for (const path of ["/%", "/%E0%A4%A", "//[", "/%00"]) {
    assert.equal((await get(port, path)).status, 400, path);
    assert.deepEqual(await get(port, "/"), { status: 200, body: "docs fixture" });
  }
  assert.equal((await get(port, "/not-present")).status, 404);
});
