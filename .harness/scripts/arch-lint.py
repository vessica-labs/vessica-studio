#!/usr/bin/env python3
"""Deterministically enforce repository-specific architecture rules.

Rule file format:

{
  "version": 1,
  "rules": [
    {
      "id": "source-file-size",
      "type": "max_file_lines",
      "globs": ["src/**/*.py"],
      "exclude": ["**/generated/**"],
      "max_lines": 1000,
      "warn_lines": 500,
      "message": "Split large source files along a clear responsibility boundary."
    },
    {
      "id": "domain-does-not-import-api",
      "type": "forbid_text",
      "globs": ["src/domain/**/*.py"],
      "exclude": ["**/tests/**"],
      "pattern": "from src\\.api|import src\\.api",
      "message": "Domain code must not depend on the API layer."
    },
    {
      "id": "required-boundary",
      "type": "require_path",
      "path": "src/domain",
      "message": "The domain boundary must exist."
    },
    {
      "id": "forbidden-legacy-module",
      "type": "forbid_path",
      "glob": "src/legacy/**",
      "message": "New code must not use the retired legacy module."
    },
    {
      "id": "entrypoint-wires-domain",
      "type": "require_text",
      "file": "src/main.py",
      "pattern": "create_domain",
      "message": "The entrypoint must construct the domain boundary."
    }
  ]
}

The shipped rule file contains a small, language-neutral baseline. Add only
rules that encode an accepted architecture decision and can be checked without
model judgment. File-size rules can declare exact legacy caps with a reason and
removal plan. The immutable parent comparison rejects cap growth, and shrinking
source requires the corresponding exception to shrink or disappear.
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


IGNORED_PREFIXES = (".git/", ".harness/runs/", ".harness/worktrees/", ".harness/adrs/")
RULE_TYPES = {"forbid_path", "require_path", "forbid_text", "require_text", "max_file_lines"}


class ConfigError(Exception):
    pass


def safe_relative(value: str, field: str) -> str:
    path = Path(value)
    if not value or path.is_absolute() or ".." in path.parts:
        raise ConfigError(f"{field} must be a non-empty repository-relative path")
    return value


def is_ignored(relative: str) -> bool:
    normalized = relative.replace("\\", "/")
    return any(normalized == prefix.rstrip("/") or normalized.startswith(prefix) for prefix in IGNORED_PREFIXES)


def matches_pattern(relative: str, pattern: str) -> bool:
    path = Path(relative)
    return (
        path.match(pattern)
        or fnmatch.fnmatchcase(relative, pattern)
        or (pattern.startswith("**/") and fnmatch.fnmatchcase(relative, pattern[3:]))
    )


def inside_root(root: Path, path: Path) -> bool:
    resolved = path.resolve()
    return resolved == root or root in resolved.parents


def matching_paths(root: Path, pattern: str, excludes: list[str] | None = None) -> list[Path]:
    safe_relative(pattern, "glob")
    matches: list[Path] = []
    for path in root.glob(pattern):
        if not inside_root(root, path):
            raise ConfigError(f"glob resolves outside repository: {pattern}")
        relative = path.relative_to(root).as_posix()
        if is_ignored(relative):
            continue
        if any(matches_pattern(relative, exclude) for exclude in (excludes or [])):
            continue
        matches.append(path)
    return sorted(matches, key=lambda path: path.relative_to(root).as_posix())


def line_number(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def validate_rule(rule: Any, index: int) -> dict[str, Any]:
    if not isinstance(rule, dict):
        raise ConfigError(f"rules[{index}] must be an object")
    rule_id = rule.get("id")
    rule_type = rule.get("type")
    message = rule.get("message")
    if not isinstance(rule_id, str) or not re.fullmatch(r"[a-zA-Z0-9._-]+", rule_id):
        raise ConfigError(f"rules[{index}].id is invalid")
    if rule_type not in RULE_TYPES:
        raise ConfigError(f"rules[{index}].type must be one of {', '.join(sorted(RULE_TYPES))}")
    if not isinstance(message, str) or not message.strip():
        raise ConfigError(f"rules[{index}].message is required")
    common = {"id", "type", "message"}
    allowed = {
        "forbid_path": common | {"glob", "exclude"},
        "require_path": common | {"path"},
        "forbid_text": common | {"globs", "exclude", "pattern"},
        "require_text": common | {"file", "pattern"},
        "max_file_lines": common | {"globs", "exclude", "max_lines", "warn_lines", "legacy"},
    }[rule_type]
    unknown = sorted(set(rule) - allowed)
    if unknown:
        raise ConfigError(f"rules[{index}] contains unknown fields: {', '.join(unknown)}")
    if rule_type == "forbid_path":
        safe_relative(rule.get("glob", ""), f"rules[{index}].glob")
        validate_excludes(rule, index)
    elif rule_type == "require_path":
        safe_relative(rule.get("path", ""), f"rules[{index}].path")
    elif rule_type in {"forbid_text", "max_file_lines"}:
        globs = rule.get("globs")
        if not isinstance(globs, list) or not globs or any(not isinstance(item, str) for item in globs):
            raise ConfigError(f"rules[{index}].globs must be a non-empty string list")
        for pattern in globs:
            safe_relative(pattern, f"rules[{index}].globs")
        validate_excludes(rule, index)
        if rule_type == "forbid_text":
            compile_pattern(rule, index)
        else:
            maximum = rule.get("max_lines")
            if isinstance(maximum, bool) or not isinstance(maximum, int) or maximum < 1:
                raise ConfigError(f"rules[{index}].max_lines must be a positive integer")
            warning = rule.get("warn_lines", maximum)
            if isinstance(warning, bool) or not isinstance(warning, int) or not 0 < warning <= maximum:
                raise ConfigError(f"rules[{index}].warn_lines must be positive and no larger than max_lines")
            legacy = rule.get("legacy", {})
            if not isinstance(legacy, dict):
                raise ConfigError(f"rules[{index}].legacy must be an object")
            for path, entry in legacy.items():
                safe_relative(path, "legacy path")
                if any(c in path for c in "*?[") or not isinstance(entry, dict):
                    raise ConfigError("Legacy exceptions require exact paths and objects")
                count = entry.get("lines")
                if isinstance(count, bool) or not isinstance(count, int) or count <= maximum:
                    raise ConfigError("Legacy lines must exceed the hard limit")
                if not all(isinstance(entry.get(k), str) and entry[k].strip() for k in ("reason", "plan")):
                    raise ConfigError("Legacy exceptions require a reason and removal plan")
    else:
        safe_relative(rule.get("file", ""), f"rules[{index}].file")
        compile_pattern(rule, index)
    return rule


def validate_excludes(rule: dict[str, Any], index: int) -> None:
    excludes = rule.get("exclude", [])
    if not isinstance(excludes, list) or any(not isinstance(item, str) for item in excludes):
        raise ConfigError(f"rules[{index}].exclude must be a string list")
    for pattern in excludes:
        safe_relative(pattern, f"rules[{index}].exclude")


def compile_pattern(rule: dict[str, Any], index: int) -> re.Pattern[str]:
    pattern = rule.get("pattern")
    if not isinstance(pattern, str) or not pattern:
        raise ConfigError(f"rules[{index}].pattern is required")
    try:
        return re.compile(pattern, re.MULTILINE)
    except re.error as exc:
        raise ConfigError(f"rules[{index}].pattern is invalid: {exc}") from exc


def load_config(path: Path) -> list[dict[str, Any]]:
    try:
        config = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ConfigError(f"rule file not found: {path}") from exc
    except json.JSONDecodeError as exc:
        raise ConfigError(f"invalid JSON in {path}: {exc}") from exc
    if not isinstance(config, dict) or config.get("version") != 1:
        raise ConfigError("rule file must be an object with version: 1")
    unknown = sorted(set(config) - {"version", "rules"})
    if unknown:
        raise ConfigError(f"rule file contains unknown fields: {', '.join(unknown)}")
    rules = config.get("rules")
    if not isinstance(rules, list):
        raise ConfigError("rule file must contain a rules array")
    validated = [validate_rule(rule, index) for index, rule in enumerate(rules)]
    ids = [rule["id"] for rule in validated]
    if len(ids) != len(set(ids)):
        raise ConfigError("rule ids must be unique")
    return validated


def lint(root: Path, rules: list[dict[str, Any]]) -> list[dict[str, Any]]:
    violations: list[dict[str, Any]] = []
    for index, rule in enumerate(rules):
        rule_type = rule["type"]
        if rule_type == "require_path":
            path = root / rule["path"]
            if not inside_root(root, path):
                raise ConfigError(f"rule {rule['id']} resolves outside repository")
            if not path.exists():
                violations.append({"rule": rule["id"], "path": rule["path"], "line": None, "message": rule["message"]})
        elif rule_type == "forbid_path":
            for path in matching_paths(root, rule["glob"], rule.get("exclude")):
                violations.append({"rule": rule["id"], "path": path.relative_to(root).as_posix(), "line": None, "message": rule["message"]})
        elif rule_type == "forbid_text":
            pattern = compile_pattern(rule, index)
            candidates: set[Path] = set()
            for glob in rule["globs"]:
                candidates.update(path for path in matching_paths(root, glob, rule.get("exclude")) if path.is_file())
            for path in sorted(candidates, key=lambda candidate: candidate.relative_to(root).as_posix()):
                try:
                    text = path.read_text(encoding="utf-8")
                except UnicodeDecodeError as exc:
                    raise ConfigError(f"rule {rule['id']} matched non-UTF-8 file {path.relative_to(root)}") from exc
                for match in pattern.finditer(text):
                    violations.append({
                        "rule": rule["id"],
                        "path": path.relative_to(root).as_posix(),
                        "line": line_number(text, match.start()),
                        "message": rule["message"],
                    })
        elif rule_type == "require_text":
            path = root / rule["file"]
            if not inside_root(root, path):
                raise ConfigError(f"rule {rule['id']} resolves outside repository")
            if not path.is_file():
                violations.append({"rule": rule["id"], "path": rule["file"], "line": None, "message": rule["message"]})
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError as exc:
                raise ConfigError(f"rule {rule['id']} matched non-UTF-8 file {rule['file']}") from exc
            if compile_pattern(rule, index).search(text) is None:
                violations.append({"rule": rule["id"], "path": rule["file"], "line": None, "message": rule["message"]})
        else:
            candidates: set[Path] = set()
            for glob in rule["globs"]:
                candidates.update(path for path in matching_paths(root, glob, rule.get("exclude")) if path.is_file())
            seen: set[str] = set()
            for path in sorted(candidates, key=lambda candidate: candidate.relative_to(root).as_posix()):
                try:
                    text = path.read_text(encoding="utf-8")
                except UnicodeDecodeError as exc:
                    raise ConfigError(f"rule {rule['id']} matched non-UTF-8 file {path.relative_to(root)}") from exc
                actual = len(text.splitlines())
                relative = path.relative_to(root).as_posix()
                seen.add(relative)
                legacy = rule.get("legacy", {}).get(relative)
                if legacy and actual < legacy["lines"]:
                    violations.append({"rule": rule["id"], "path": relative, "line": None,
                                       "message": "Reduce or remove the stale legacy exception to match improved source."})
                maximum = legacy["lines"] if legacy else rule["max_lines"]
                if actual > maximum:
                    violations.append({
                        "rule": rule["id"],
                        "path": relative,
                        "line": maximum + 1,
                        "message": f"{rule['message']} ({actual} lines; maximum {maximum})",
                    })
                elif actual > rule.get("warn_lines", rule["max_lines"]):
                    violations.append({"rule": rule["id"], "path": relative,
                                       "line": rule.get("warn_lines", rule["max_lines"]) + 1,
                                       "severity": "warning",
                                       "message": f"{rule['message']} ({actual} lines; soft limit {rule.get('warn_lines', rule['max_lines'])})"})
            for stale in sorted(set(rule.get("legacy", {})) - seen):
                violations.append({"rule": rule["id"], "path": stale, "line": None,
                                   "message": "Remove the legacy exception for an unmatched or removed file."})
    return violations


def validate_baseline(root: Path, rules: list[dict[str, Any]], config: Path, ref: str) -> None:
    """Compare exceptions against an immutable parent, never a mutable branch."""
    if not re.fullmatch(r"[a-fA-F0-9]{40}", ref):
        raise ConfigError("Baseline reference must be a full immutable Git SHA")
    exists = subprocess.run(["git", "cat-file", "-e", f"{ref}^{{commit}}"], cwd=root,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    if exists.returncode:
        raise ConfigError("Baseline commit is unavailable; fetch the immutable parent before linting")
    def historical(path: str) -> str | None:
        result = subprocess.run(["git", "show", f"{ref}:{path}"], cwd=root, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False)
        return result.stdout if result.returncode == 0 else None
    previous = historical(config.resolve().relative_to(root).as_posix())
    old = json.loads(previous) if previous is not None else {"rules": []}
    old_rules = {rule["id"]: rule for rule in old["rules"]}
    for rule in rules:
        for path, entry in rule.get("legacy", {}).items():
            prior = old_rules.get(rule["id"], {}).get("legacy", {}).get(path)
            if prior:
                if entry["lines"] > prior["lines"]:
                    raise ConfigError(f"Legacy exception cannot grow: {path}")
            else:
                source = historical(path)
                if source is None or len(source.splitlines()) != entry["lines"]:
                    raise ConfigError(f"New legacy exception must exactly match existing baseline source: {path}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".", help="Repository root; defaults to the current directory")
    parser.add_argument("--config", default=".harness/arch-lint-rules.json", help="Rule file relative to the repository root")
    parser.add_argument("--json", action="store_true", help="Emit machine-readable JSON")
    parser.add_argument("--baseline-ref", default=os.environ.get("ARCHITECTURE_BASE_REF"),
                        help="Immutable parent SHA used to prevent exception growth")
    args = parser.parse_args()
    root = Path(args.root).resolve()
    config_path = Path(args.config)
    if not config_path.is_absolute():
        config_path = root / config_path
    try:
        rules = load_config(config_path)
        if args.baseline_ref and not re.fullmatch(r"0+", args.baseline_ref):
            validate_baseline(root, rules, config_path, args.baseline_ref)
        violations = lint(root, rules)
    except (ConfigError, OSError) as exc:
        if args.json:
            print(json.dumps({"ok": False, "error": str(exc), "violations": []}, sort_keys=True))
        else:
            print(f"arch-lint: CONFIG ERROR: {exc}", file=sys.stderr)
        raise SystemExit(2)
    errors = [v for v in violations if v.get("severity") != "warning"]
    if args.json:
        print(json.dumps({"ok": not errors, "rules": len(rules), "violations": violations}, sort_keys=True))
    elif violations:
        for violation in violations:
            location = violation["path"]
            if violation["line"] is not None:
                location += f":{violation['line']}"
            print(f"{location}: {violation.get('severity', 'error')} [{violation['rule']}] {violation['message']}")
        print(f"arch-lint: {'FAIL' if errors else 'PASS'} ({len(errors)} error(s), {len(violations)-len(errors)} warning(s), {len(rules)} rule(s))")
    else:
        print(f"arch-lint: PASS ({len(rules)} rule(s))")
    raise SystemExit(1 if errors else 0)


if __name__ == "__main__":
    main()
