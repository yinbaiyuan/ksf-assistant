#!/usr/bin/env python3
"""Bounded, offline preview.4 adaptation of staged official lark-cli 1.0.93 Python.

Run before packaging: python3 scripts/adapt-lark-skill-python.py --root STAGED_SKILLS_ROOT
Only Python bytes are written. Markdown, metadata, and packaging belong to the
parent adapter. The complete pinned Python inventory is required; any drift must
be reviewed rather than accepted automatically. Reports use relative POSIX paths.
"""

from __future__ import annotations

import sys
sys.dont_write_bytecode = True

import argparse
import ast
import hashlib
import json
import os
import re
from pathlib import Path


SHEETS = "lark-sheets/scripts/"
SLIDES = "lark-slides/scripts/"
UPSTREAM_SHA256 = {
    SHEETS + "lark_chart_layout_check.py": "670649d2cdb6e282dfb10d2a69ed26e718f0a0e2eb70a729a43a368a7dcff4a0",
    SHEETS + "lark_detect_subtables.py": "ac0016c23cc051f1fc2fcf4cf5e8ed272510ee1e7f9e64d548083188383db203",
    SHEETS + "lark_inspect_workbook.py": "e45d12779cbcc178a1d0ab6a57807e94b07aef0f014e0b010a755e6f4a0672a3",
    SHEETS + "lark_profile_table.py": "3fa4f0aae050b143794dbacd7d299986bf21320cbbbe0b24f69f619a06664519",
    SHEETS + "lark_sheet_range.py": "dc54be144daeb4d3bc8dab7db72b72ddc23e00f9fb6a77bc91acbf5c0644e8ac",
    SHEETS + "lark_sheet_read_cli.py": "37a5f4caf4215d21f35387103dff0abfadac80b8d110cf6aae5a0b75e10ed01e",
    SHEETS + "sheets_df.py": "8166b9a624e362c7408418b7fecf14421f4465fe3056f6793cfb2f840dd6371b",
    SLIDES + "iconpark_tool.py": "39061ef88e55549762aac4fc3b337f5a60cc8114c111299c9436d88eb6184431",
    SLIDES + "iconpark_tool_test.py": "db83d31146404fe53ec2a5d6e9037c62f9d20605a326709f48345b3e54dab49e",
    SLIDES + "sxsd_validator.py": "cf75b86fac04700848e9556ad9cee26a51461b0eb7ef45d9e550cadc229add85",
    SLIDES + "xml_lint.py": "0c9f4ba72a9f26a343f797fdbd11d9d035f461a7cdd16864e2cc3b3ca75079e4",
    SLIDES + "xml_lint_test.py": "778b389fc39834d83121a2f5bd9ed6001f3af3046ce673437184d8ed826e4c91",
    SLIDES + "xml_text_overlap_lint.py": "5f0a1af89e4d331a4c87e69d80dcfc469145a7f392f41ca6156037c2930ccb35",
    SLIDES + "xml_text_overlap_lint_test.py": "9e6af04d8b16b09422fba80773b32e668aef50943187ecd66ccc9e232bb4d98a",
}
ONLINE_COUNTS = {
    SHEETS + "lark_chart_layout_check.py": 4,
    SHEETS + "lark_detect_subtables.py": 4,
    SHEETS + "lark_inspect_workbook.py": 3,
    SHEETS + "lark_profile_table.py": 2,
}
SUBPROCESS_LINES = {
    SHEETS + "lark_sheet_read_cli.py": (68,),
    SLIDES + "iconpark_tool_test.py": (121,),
    SLIDES + "xml_lint_test.py": (40, 71, 95, 3169),
}
EXEC_LINES = {SLIDES + "xml_text_overlap_lint_test.py": (13,)}
BOOTSTRAP = "import sys\nsys.dont_write_bytecode = True\n\n"
MANAGED_SUPPORT = '''class _ExplicitIdentity(argparse.Action):
    def __call__(self, parser, namespace, values, option_string=None):
        if getattr(namespace, self.dest, None) is not None:
            parser.error("--as must be supplied exactly once")
        setattr(namespace, self.dest, values)


def add_identity_arg(parser) -> None:
    parser.allow_abbrev = False
    parser.add_argument("--as", dest="identity", choices=("user", "bot"),
                        required=True, action=_ExplicitIdentity)


def _managed_lark() -> str:
    home = Path.home()
    if not home.is_absolute():
        raise LarkCliError("Platform home must be absolute")
    if sys.platform == "darwin":
        entry = home / ".local/share/ksfassistant/toolchain/bin/ksfas-lark"
    elif sys.platform == "win32":
        entry = home / "AppData/Local/KSFAssistant/toolchain/bin/ksfas-lark.exe"
    else:
        raise LarkCliError("Unsupported managed CLI platform: " + sys.platform)
    if not entry.is_file():
        raise LarkCliError("Managed CLI not found: " + str(entry))
    return str(entry)


'''
IDENTITY_GUARD = '''    if identity not in ("user", "bot"):
        raise LarkCliError("Pass explicit identity: user or bot")
    for key in (flags or {}):
        normalized = str(key).replace("_", "-").split("=", 1)[0].strip("-")
        if normalized in {"as", "identity"}:
            raise LarkCliError("Identity must not be supplied through flags")

'''
SNIPPET_HASHES = {
    "lark-sheets/references/lark-sheets-read-data.md": (
        "67e79d3f569c0f5f68c69a3742831e5044333cfb67b7cbf6cbbac50da349d625",
        "4d8cdf80871addbc50602c0ada0606732d31bf48e0def05825a6d05930b27d2d",
    ),
    "lark-sheets/references/lark-sheets-write-cells.md": (
        "78e7205582b681e1fcbd3a09b691a922d525e21fd4aa34150dc939e6ab0f2e5c",
        "d791d6281223fa4e3a5856d8d38caa3c6e8c6a34683bd4e2186e3c6d569d2368",
    ),
}
ROUNDTRIP_HASH = SNIPPET_HASHES["lark-sheets/references/lark-sheets-read-data.md"][1]
SNIPPET_PREFIX = (BOOTSTRAP + "import argparse\nfrom pathlib import Path\n\n"
                  + MANAGED_SUPPORT.replace("LarkCliError", "RuntimeError")
                  + 'parser = argparse.ArgumentParser(allow_abbrev=False)\n'
                  + 'add_identity_arg(parser)\n'
                  + 'parser.add_argument("--url", required=True)\n'
                  + 'args = parser.parse_args()\n'
                  + 'IDENTITY = args.identity\nURL = args.url\n'
                  + 'MANAGED_LARK = _managed_lark()\n\n')


class AdaptationError(ValueError):
    pass


def fixed_patches(name: str) -> list[tuple[str, str]]:
    patches = []
    if name == SHEETS + "sheets_df.py":
        patches.extend([
            ("    from sheets_df import df_to_sheet, sheet_to_df",
             "    import sys\n    sys.dont_write_bytecode = True\n    from sheets_df import df_to_sheet, sheet_to_df"),
            ("Callers run lark-cli themselves; this file is a library, not a CLI.",
             "This file is an offline library, not a CLI. Run callers with python3 -B,\n"
             "or disable bytecode before importing as shown above. For online calls,\n"
             "resolve only the fixed absolute managed entry from platform home:\n"
             "macOS: ~/.local/share/ksfassistant/toolchain/bin/ksfas-lark\n"
             "Windows: ~/AppData/Local/KSFAssistant/toolchain/bin/ksfas-lark.exe\n"
             "Pass exactly one explicit --as user or --as bot according to user intent.\n"
             "If the entry is missing or refuses the call, stop; never search PATH,\n"
             "fall back to another CLI, or switch identity. Existing capability and\n"
             "approval policies still apply."),
        ])
    if name == SHEETS + "lark_sheet_read_cli.py":
        patches.extend([
            ("import json\n", "import argparse\nfrom pathlib import Path\n\nimport json\n"),
            ("class LarkCliError(RuntimeError):", MANAGED_SUPPORT + "class LarkCliError(RuntimeError):"),
            ("    spreadsheet = parser.add_mutually_exclusive_group(required=True)",
             "    add_identity_arg(parser)\n    spreadsheet = parser.add_mutually_exclusive_group(required=True)"),
            ("    shortcut: str,\n    *,\n", "    shortcut: str,\n    *,\n    identity: str,\n"),
            ('    cmd = ["lark-cli", "sheets", shortcut]',
             IDENTITY_GUARD + '    cmd = [_managed_lark(), "sheets", shortcut, "--as", identity]'),
        ])
    if name == SHEETS + "lark_chart_layout_check.py":
        patches.extend([
            ("    LarkCliError,\n", "    LarkCliError,\n    add_identity_arg,\n"),
            ('    parser.add_argument("sheet_id",', '    add_identity_arg(parser)\n    parser.add_argument("sheet_id",'),
            ("*, timeout: int, sample_limit: int", "*, identity: str, timeout: int, sample_limit: int"),
            ("check_sheet(locator, sheet, timeout=", "check_sheet(locator, sheet, identity=args.identity, timeout="),
        ])
    return patches


def digest(source: str) -> str:
    return hashlib.sha256(source.encode("utf-8")).hexdigest()


def pristine_source(name: str, source: str) -> str:
    if digest(source) == UPSTREAM_SHA256[name]:
        return source
    restored = source.replace(BOOTSTRAP, "", 1)
    for before, after in reversed(fixed_patches(name)):
        restored = restored.replace(after, before, 1)
    restored = restored.replace(", identity=args.identity", "").replace(", identity=identity", "")
    if digest(restored) != UPSTREAM_SHA256[name]:
        raise AdaptationError(f"{name}: upstream drift or unknown subprocess/CLI pattern (expected lark-cli 1.0.93)")
    return restored


def parse(source: str, name: str) -> ast.Module:
    try:
        tree = ast.parse(source, filename=name)
        compile(tree, name, "exec")
        return tree
    except (SyntaxError, ValueError) as exc:
        raise AdaptationError(f"{name}: invalid Python: {exc}") from exc


def byte_offset(source: bytes, line: int, column: int) -> int:
    return sum(len(part) for part in source.splitlines(keepends=True)[:line - 1]) + column


def render(name: str, source: str) -> tuple[str, list[dict]]:
    tree = parse(source, name)
    calls = sorted((node for node in ast.walk(tree) if isinstance(node, ast.Call)), key=lambda node: node.lineno)
    subprocess_calls = [node for node in calls if isinstance(node.func, ast.Attribute)
                        and isinstance(node.func.value, ast.Name) and node.func.value.id == "subprocess"]
    if tuple(node.lineno for node in subprocess_calls) != SUBPROCESS_LINES.get(name, ()):
        raise AdaptationError(f"{name}: unknown subprocess callsites")
    report = []
    exec_calls = [node for node in calls if isinstance(node.func, ast.Attribute)
                  and isinstance(node.func.value, ast.Name) and node.func.value.id == "os"
                  and node.func.attr.startswith(("exec", "spawn", "system", "popen"))]
    if tuple(node.lineno for node in exec_calls) != EXEC_LINES.get(name, ()):
        raise AdaptationError(f"{name}: unknown process replacement callsites")
    for node in exec_calls:
        expected = ast.parse("os.execv(sys.executable, [sys.executable, str(target), *sys.argv[1:]])", mode="eval").body
        if ast.dump(node) != ast.dump(expected):
            raise AdaptationError(f"{name}:{node.lineno}: unknown process replacement argv")
        report.append({"file": name, "line": node.lineno, "kind": "local-python-exec"})
    for node in subprocess_calls:
        if node.func.attr != "run" or not node.args or any(key.arg == "shell" for key in node.keywords):
            raise AdaptationError(f"{name}:{node.lineno}: unknown subprocess pattern")
        managed = name == SHEETS + "lark_sheet_read_cli.py"
        if managed:
            valid = ast.dump(node.args[0]) == ast.dump(ast.Name(id="cmd", ctx=ast.Load()))
        else:
            valid = isinstance(node.args[0], ast.List) and ast.unparse(node.args[0].elts[0]) == "sys.executable"
        if not valid:
            raise AdaptationError(f"{name}:{node.lineno}: unknown subprocess argv")
        report.append({"file": name, "line": node.lineno, "kind": "managed-cli" if managed else "local-python-test"})
    online = [node for node in calls if isinstance(node.func, ast.Name) and node.func.id == "run_sheets"]
    if len(online) != ONLINE_COUNTS.get(name, 0):
        raise AdaptationError(f"{name}: unknown run_sheets callsites")
    edits = []
    encoded = source.encode("utf-8")
    for node in online:
        if len(node.args) != 1 or not isinstance(node.args[0], ast.Constant) or not isinstance(node.args[0].value, str):
            raise AdaptationError(f"{name}:{node.lineno}: unknown CLI shortcut pattern")
        function = next(owner for owner in ast.walk(tree) if isinstance(owner, ast.FunctionDef)
                        and owner.lineno <= node.lineno <= owner.end_lineno)
        if function.name == "check_sheet" and name == SHEETS + "lark_chart_layout_check.py":
            identity = "identity"
        elif any(argument.arg == "args" for argument in function.args.args) or function.name == "main":
            identity = "args.identity"
        else:
            raise AdaptationError(f"{name}:{node.lineno}: unknown identity propagation scope")
        shortcut = node.args[0]
        edits.append((byte_offset(encoded, shortcut.end_lineno, shortcut.end_col_offset),
                      f", identity={identity}".encode()))
        report.append({"file": name, "line": node.lineno, "kind": "run_sheets", "shortcut": shortcut.value})
    first_statement = next(node for node in tree.body
                           if not (isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str))
                           and not (isinstance(node, ast.ImportFrom) and node.module == "__future__"))
    edits.append((byte_offset(encoded, first_statement.lineno, 0), BOOTSTRAP.encode()))
    for offset, addition in sorted(edits, reverse=True):
        encoded = encoded[:offset] + addition + encoded[offset:]
    output = encoded.decode("utf-8")
    for before, after in fixed_patches(name):
        if output.count(before) != 1:
            raise AdaptationError(f"{name}: patch anchor not unique: {before!r}")
        output = output.replace(before, after, 1)
    adapted = parse(output, name)
    for node in ast.walk(adapted):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "run_sheets":
            if len([key for key in node.keywords if key.arg == "identity"]) != 1:
                raise AdaptationError(f"{name}: missing explicit identity after adaptation")
    return output, sorted(report, key=lambda item: item["line"])


def python_fences(source: str) -> list[tuple[int, int, str]]:
    blocks = []
    opening = None
    offset = 0
    for line in source.splitlines(keepends=True):
        if opening is None:
            match = re.match(r"^ {0,3}(`{3,}|~{3,})([^\r\n]*)", line)
            if match:
                marker, info = match.groups()
                language = info.strip().lower().split(maxsplit=1)
                opening = (marker, language[0] if language else "", offset + len(line))
        else:
            marker, language, start = opening
            if re.fullmatch(r" {0,3}" + re.escape(marker[0]) + "{" + str(len(marker)) + r",}\s*", line):
                if language in {"python", "python3", "py", "{.python}", ""}:
                    blocks.append((start, offset, language))
                opening = None
        offset += len(line)
    if opening and opening[1] in {"python", "python3", "py", "{.python}"}:
        raise AdaptationError("Unclosed Python Markdown fence")
    return blocks


def sensitive_python(tree: ast.Module) -> bool:
    for node in ast.walk(tree):
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            modules = [alias.name for alias in node.names] if isinstance(node, ast.Import) else [node.module or ""]
            if any(module.split(".")[0] in {"subprocess", "os", "importlib", "sheets_df", "lark_sheet_read_cli"} for module in modules):
                return True
        if isinstance(node, ast.Call):
            function = node.func.id if isinstance(node.func, ast.Name) else node.func.attr if isinstance(node.func, ast.Attribute) else ""
            if function in {"__import__", "eval", "exec", "run", "Popen", "check_call", "check_output", "system", "popen"} or function.startswith(("execv", "spawn")):
                return True
    return False


def adapt_markdown_snippets(name: str, source: str) -> tuple[str, list[dict]]:
    """Pure bounded fenced-Python transform; return (Markdown, callsite records)."""
    edits = []
    reports = []
    found = []
    for start, end, language in python_fences(source):
        current = source[start:end]
        restored = current
        if restored.startswith(SNIPPET_PREFIX):
            restored = restored[len(SNIPPET_PREFIX):]
            for shortcut in ("+table-get", "+table-put"):
                restored = restored.replace(f'MANAGED_LARK,"sheets","{shortcut}","--as",IDENTITY',
                                            f'"ksfas-lark","sheets","{shortcut}"')
        elif restored.startswith(BOOTSTRAP):
            restored = restored[len(BOOTSTRAP):]
        normalized = restored.replace('"ksfas-lark"', '"lark-cli"')
        fingerprint = digest(normalized)
        expected = SNIPPET_HASHES.get(name, ())
        if fingerprint not in expected:
            try:
                tree = parse(current, name)
            except AdaptationError:
                if language:
                    raise
                if re.search(r"(?m)^\s*(?:from|import)\s+(?:subprocess|os)\b", current):
                    raise AdaptationError(f"{name}: invalid unknown Python process snippet")
                continue
            if sensitive_python(tree) or name in SNIPPET_HASHES:
                raise AdaptationError(f"{name}: unknown Python subprocess/helper snippet or upstream drift")
            continue
        if fingerprint in found:
            raise AdaptationError(f"{name}: duplicated pinned Python snippet")
        found.append(fingerprint)
        tree = parse(normalized, name)
        output = BOOTSTRAP + restored
        if fingerprint == ROUNDTRIP_HASH:
            calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
                     and isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name)
                     and node.func.value.id == "subprocess"]
            if sorted(node.func.attr for node in calls) != ["check_output", "run"]:
                raise AdaptationError(f"{name}: unexpected round-trip subprocess shape")
            output = restored
            for shortcut in ("+table-get", "+table-put"):
                anchors = [f'"{launcher}","sheets","{shortcut}"' for launcher in ("lark-cli", "ksfas-lark")]
                if sum(output.count(anchor) for anchor in anchors) != 1:
                    raise AdaptationError(f"{name}: nonunique round-trip argv anchor")
                for anchor in anchors:
                    output = output.replace(anchor, f'MANAGED_LARK,"sheets","{shortcut}","--as",IDENTITY')
                reports.append({"file": name, "snippet": expected.index(fingerprint) + 1,
                                "kind": "markdown-managed-cli", "shortcut": shortcut})
            output = SNIPPET_PREFIX + output
        parse(output, name)
        if current != restored and current != output:
            raise AdaptationError(f"{name}: mutated or noncanonical adapted Python snippet")
        edits.append((start, end, output))
    if found != list(SNIPPET_HASHES.get(name, ())):
        raise AdaptationError(f"{name}: missing or reordered pinned Python snippets")
    for start, end, output in reversed(edits):
        source = source[:start] + output + source[end:]
    for start, end, language in python_fences(source):
        body = source[start:end]
        if not body.startswith(SNIPPET_PREFIX):
            continue
        for node in ast.walk(parse(body, name)):
            if not (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                    and isinstance(node.func.value, ast.Name) and node.func.value.id == "subprocess"):
                continue
            shortcut = node.args[0].elts[2].value
            for report in reports:
                if report["shortcut"] == shortcut:
                    report["line"] = source[:start].count("\n") + node.lineno
    return source, reports


def adapt_markdown_root(root: Path) -> dict:
    if root.is_symlink() or not root.is_dir():
        raise AdaptationError("--root must be a real staged directory")
    root = root.resolve()
    installed = (Path.home() / ".agents/skills").resolve()
    if root == installed or installed in root.parents:
        raise AdaptationError("Refusing to modify installed Skills; use a staged copy")
    pending = []
    reports = []
    seen = set()
    for directory, directories, files in os.walk(root, followlinks=False):
        for entry in directories + files:
            if (Path(directory) / entry).is_symlink():
                raise AdaptationError("Symlink in staged Markdown tree")
        for filename in sorted(files):
            if not filename.endswith(".md"):
                continue
            path = Path(directory) / filename
            name = path.relative_to(root).as_posix()
            seen.add(name)
            original = path.read_bytes()
            output, callsites = adapt_markdown_snippets(name, original.decode("utf-8"))
            reports.extend(callsites)
            if output.encode("utf-8") != original:
                pending.append((name, path, original, output.encode("utf-8")))
    if not SNIPPET_HASHES.keys() <= seen:
        raise AdaptationError("Missing pinned Markdown snippet documents")
    for name, path, original, output in pending:
        if path.is_symlink() or path.read_bytes() != original:
            raise AdaptationError(f"{name}: staged Markdown changed during validation")
    for name, path, original, output in pending:
        path.write_bytes(output)
    return {"filesChanged": sorted(name for name, *_ in pending),
            "callsites": sorted(reports, key=lambda item: (item["file"], item["snippet"], item["shortcut"]))}


def adapt(root: Path) -> dict:
    if root.is_symlink() or not root.is_dir():
        raise AdaptationError("--root must be a real staged directory")
    root = root.resolve()
    installed = (Path.home() / ".agents/skills").resolve()
    if root == installed or installed in root.parents:
        raise AdaptationError("Refusing to modify installed Skills; use a staged copy")
    paths = {}
    for directory, directories, files in os.walk(root, followlinks=False):
        for entry in directories + files:
            path = Path(directory) / entry
            if path.is_symlink():
                raise AdaptationError(f"Symlink in staged tree: {path.relative_to(root)}")
        for filename in files:
            if filename.endswith(".py"):
                path = Path(directory) / filename
                paths[path.relative_to(root).as_posix()] = path
    if paths.keys() != UPSTREAM_SHA256.keys():
        missing = sorted(UPSTREAM_SHA256.keys() - paths.keys())
        unknown = sorted(paths.keys() - UPSTREAM_SHA256.keys())
        raise AdaptationError(f"Python inventory drift; missing={missing}; unknown={unknown}")
    pending = []
    callsites = []
    for name, path in sorted(paths.items()):
        original = path.read_bytes()
        current = original.decode("utf-8")
        pristine = pristine_source(name, current)
        output, report = render(name, pristine)
        if current != pristine and current != output:
            raise AdaptationError(f"{name}: non-canonical or mutated adaptation")
        callsites.extend(report)
        if current != output:
            pending.append((name, path, original, output.encode("utf-8")))
    for name, path, original, output in pending:
        if path.is_symlink() or path.read_bytes() != original:
            raise AdaptationError(f"{name}: staged file changed during validation")
    for name, path, original, output in pending:
        path.write_bytes(output)
    return {"filesChanged": [name for name, *_ in pending], "callsites": callsites}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, allow_abbrev=False)
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument("--root", type=Path)
    modes.add_argument("--markdown-root", type=Path)
    args = parser.parse_args()
    try:
        report = adapt_markdown_root(args.markdown_root) if args.markdown_root is not None else adapt(args.root)
    except (AdaptationError, OSError, UnicodeError) as exc:
        print(json.dumps({"error": str(exc), "filesChanged": [], "callsites": []}, ensure_ascii=False), file=sys.stderr)
        return 1
    print(json.dumps(report, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
