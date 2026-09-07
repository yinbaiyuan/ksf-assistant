#!/usr/bin/env python3
"""Offline adapter tests: python3 -B scripts/test-lark-skill-python.py.

Use --source ORIGINAL_SKILLS_ROOT or KSF_SKILLS_SOURCE to select extracted
lark-cli 1.0.93 Skills. Otherwise read the original lark-* Skills from
~/.agents/skills. Originals are only read; all execution uses temporary copies,
temporary homes, and fake managed CLIs. No network or dependency installation is
needed. The optional DataFrame round-trip test uses pandas when already present;
without it that test is explicitly skipped, never replaced with a fake pandas.
"""

from __future__ import annotations

import sys
sys.dont_write_bytecode = True

import argparse
import ast
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest
from unittest import mock


ADAPTER = Path(__file__).with_name("adapt-lark-skill-python.py")
API = runpy.run_path(str(ADAPTER))
SOURCE = None
SOURCE_EXPLICIT = False
FAKE_CLI = '''import json, os, sys
from pathlib import Path
with open(os.environ["FAKE_CLI_LOG"], "a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv, ensure_ascii=False) + "\\n")
shortcut = sys.argv[2]
if shortcut == "+table-put":
    Path(os.environ["FAKE_CLI_STDIN"]).write_text(sys.stdin.read(), encoding="utf-8")
if os.environ.get("FAKE_CLI_REFUSE"):
    print(json.dumps({"ok": False, "error": "fixture refusal; do not retry"}))
    raise SystemExit(7)
responses = {
    "+workbook-info": {"sheets": [{"sheet_id": "sheet1", "title": "测试表", "row_count": 20, "column_count": 4}]},
    "+sheet-info": {},
    "+csv-get": {"annotated_csv": "Name,Value\\nAlice,42", "col_indices": ["A", "B"], "row_indices": [1, 2]},
    "+chart-list": {"charts": [{"chart_id": "chart1", "position": {"row": 0, "col": "A"}, "size": {"width": 20, "height": 20}}]},
    "+cells-get": {},
    "+table-get": {"sheets": [{"name": "销售", "columns": ["姓名", "营收"], "data": [["Alice", 100]], "dtypes": {"姓名": "object", "营收": "float64"}, "formats": {"营收": "0.00"}}]},
}
if os.environ.get("FAKE_MERGE_ANCHOR"):
    responses["+sheet-info"] = {"merges": ["A1:B2"]}
    anchor = sys.argv[sys.argv.index("--range") + 1] == "A1" if "--range" in sys.argv else False
    responses["+csv-get"] = {"annotated_csv": "anchor" if anchor else "42", "col_indices": ["A" if anchor else "B"], "row_indices": [1 if anchor else 2]}
print(json.dumps({"ok": True, "data": responses.get(shortcut, {})}))
'''
FAKE_PANDAS = '''import json
class Series(list):
    def __mul__(self, factor):
        return Series(value * factor for value in self)
class DataFrame:
    def __init__(self, data, columns):
        self.data = [list(row) for row in data]
        self.columns = list(columns)
        self.dtypes = ["object", "float64"]
    def astype(self, dtypes):
        self.dtypes = [dtypes[column] for column in self.columns]
        return self
    def __getitem__(self, column):
        return Series(row[self.columns.index(column)] for row in self.data)
    def __setitem__(self, column, values):
        for row, value in zip(self.data, values):
            row[self.columns.index(column)] = value
    def to_json(self, orient, date_format):
        assert orient == "split" and date_format == "iso"
        return json.dumps({"columns": self.columns, "data": self.data, "index": list(range(len(self.data)))})
'''


def snapshot(root: Path) -> dict:
    return {path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted(root.rglob("*")) if path.is_file()}


class AdapterTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workspace = tempfile.TemporaryDirectory(prefix="ksfas-python-tests-")
        cls.addClassCleanup(cls.workspace.cleanup)
        cls.base = Path(cls.workspace.name)
        cls.pristine = cls.base / "pristine"
        cls.pristine.mkdir()
        if SOURCE_EXPLICIT:
            shutil.copytree(SOURCE, cls.pristine, dirs_exist_ok=True,
                            ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
        else:
            for directory in sorted(SOURCE.glob("lark-*")):
                if directory.is_dir():
                    shutil.copytree(directory, cls.pristine / directory.name,
                                    ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
        cls.original_hashes = snapshot(cls.pristine)
        cls.adapted = cls.base / "adapted"
        shutil.copytree(cls.pristine, cls.adapted)
        cls.initial_report = API["adapt"](cls.adapted)

    @classmethod
    def tearDownClass(cls):
        cls.workspace.cleanup()

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(dir=self.base)
        self.addCleanup(self.temporary.cleanup)
        self.temp = Path(self.temporary.name)
        self.home = self.temp / "home space"
        self.home.mkdir()
        self.log = self.temp / "argv.jsonl"
        self.env = dict(os.environ, HOME=str(self.home), USERPROFILE=str(self.home),
                        FAKE_CLI_LOG=str(self.log), FAKE_CLI_STDIN=str(self.temp / "stdin.json"), PATH=str(self.temp / "hostile-path"))
        for key in ("PYTHONPATH", "PYTHONHOME", "PYTHONPYCACHEPREFIX", "PYTHONDONTWRITEBYTECODE"):
            self.env.pop(key, None)
        self.entries = {
            "darwin": self.home / ".local/share/ksfassistant/toolchain/bin/ksfas-lark",
            "win32": self.home / "AppData/Local/KSFAssistant/toolchain/bin/ksfas-lark.exe",
        }
        for entry in self.entries.values():
            entry.parent.mkdir(parents=True, exist_ok=True)
            entry.write_text("#!" + sys.executable + "\n" + FAKE_CLI, encoding="utf-8")
            entry.chmod(0o755)

    def copy_stage(self):
        stage = self.temp / "staged"
        shutil.copytree(self.pristine, stage)
        return stage

    def invoke_adapter(self, root):
        return subprocess.run([sys.executable, "-B", str(ADAPTER), "--root", str(root)],
                              text=True, capture_output=True, env=self.env, timeout=30)

    def invoke_script(self, name, arguments, platform=None):
        path = self.adapted / name
        if platform is None or platform == sys.platform:
            command = [sys.executable, str(path), *arguments]
        else:
            runner = "import runpy,sys; sys.platform=sys.argv.pop(1); sys.argv.pop(0); path=sys.argv[0]; sys.path.insert(0,str(__import__('pathlib').Path(path).parent)); runpy.run_path(path,run_name='__main__')"
            command = [sys.executable, "-c", runner, platform, str(path), *arguments]
        return subprocess.run(command, text=True, capture_output=True, env=self.env, timeout=30)

    def argv(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def load_helper(self):
        return runpy.run_path(str(self.adapted / API["SHEETS"] / "lark_sheet_read_cli.py"))

    def test_inventory_business_text_and_pristine_are_preserved(self):
        self.assertEqual(snapshot(self.pristine), self.original_hashes)
        adapted = snapshot(self.adapted)
        self.assertEqual(adapted.keys(), self.original_hashes.keys())
        self.assertEqual(set(self.initial_report["filesChanged"]), set(API["UPSTREAM_SHA256"]))
        for name in adapted:
            if name.endswith(".py"):
                self.assertEqual(API["pristine_source"](name, (self.adapted / name).read_text()),
                                 (self.pristine / name).read_text())
            else:
                self.assertEqual(adapted[name], self.original_hashes[name], name)
        self.assertEqual(len(self.initial_report["callsites"]), 20)

    def test_json_determinism_idempotence_and_markdown_independence(self):
        stage = self.copy_stage()
        markdown = stage / "lark-sheets/SKILL.md"
        markdown.write_text("Parent already adapted Markdown and metadata.\n")
        first = self.invoke_adapter(stage)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(json.loads(first.stdout), self.initial_report)
        self.assertEqual(first.stderr, "")
        before = snapshot(stage)
        second = self.invoke_adapter(stage)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(second.stdout), {"filesChanged": [], "callsites": self.initial_report["callsites"]})
        self.assertEqual(snapshot(stage), before)
        self.assertEqual(markdown.read_text(), "Parent already adapted Markdown and metadata.\n")

    def test_normal_help_no_bytecode_no_cli(self):
        entries = [name for name in API["UPSTREAM_SHA256"] if not name.endswith("/sheets_df.py")]
        if importlib.util.find_spec("pandas") is not None:
            entries.append(API["SHEETS"] + "sheets_df.py")
        for name in entries:
            with self.subTest(name=name):
                result = self.invoke_script(name, ["--help"])
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertFalse(list(self.adapted.rglob("__pycache__")))
                self.assertFalse(list(self.adapted.rglob("*.pyc")))
        self.assertEqual(self.argv(), [])

    def test_public_identity_required_invalid_and_duplicate_rejected(self):
        for name in API["ONLINE_COUNTS"]:
            for flags in ([], ["--as", "auto"], ["--as", "user", "--as", "bot"],
                          ["--as=user", "--as=user"], ["--a", "user"]):
                with self.subTest(name=name, flags=flags):
                    base = ["token"] if "chart_layout" in name else ["--spreadsheet-token", "token", "--sheet-id", "sheet1"]
                    if "profile_table" in name:
                        base += ["--range", "A1:B2"]
                    result = self.invoke_script(name, base + flags)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("--as", result.stderr)
        self.assertEqual(self.argv(), [])

    def test_all_public_online_chains_exact_identity_and_params(self):
        platform = sys.platform if sys.platform in self.entries else "darwin"
        for identity in ("user", "bot"):
            for name in API["ONLINE_COUNTS"]:
                with self.subTest(identity=identity, name=name):
                    start = len(self.argv())
                    if "chart_layout" in name:
                        arguments = ["token", "--worksheet-id", "sheet1"]
                        shortcuts = ["+workbook-info", "+sheet-info", "+chart-list", "+cells-get"]
                    else:
                        arguments = ["--spreadsheet-token", "token", "--sheet-id", "sheet1"]
                        shortcuts = ["+workbook-info", "+sheet-info", "+csv-get"]
                        if "profile_table" in name:
                            arguments += ["--range", "A1:B2"]
                            shortcuts = ["+csv-get", "+sheet-info"]
                    result = self.invoke_script(name, arguments + ["--as", identity], platform)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    calls = self.argv()[start:]
                    self.assertEqual([call[2] for call in calls], shortcuts)
                    for call in calls:
                        self.assertEqual(call[:2], [str(self.entries[platform]), "sheets"])
                        self.assertEqual(call[3:5], ["--as", identity])
                        self.assertEqual(call.count("--as"), 1)
                        self.assertEqual(call[call.index("--spreadsheet-token") + 1], "token")
                        if call[2] != "+workbook-info":
                            self.assertEqual(call[call.index("--sheet-id") + 1], "sheet1")
        self.assertFalse(list(self.adapted.rglob("__pycache__")))

    def test_direct_argv_both_platforms_and_flags_preserved(self):
        helper = self.load_helper()
        for platform in self.entries:
            for identity in ("user", "bot"):
                with self.subTest(platform=platform, identity=identity):
                    with mock.patch.dict(os.environ, self.env, clear=True), mock.patch.object(sys, "platform", platform):
                        if os.name == "nt":
                            with mock.patch.object(helper["subprocess"], "run", return_value=subprocess.CompletedProcess([], 0, '{"ok":true}')) as run:
                                helper["run_sheets"]("+csv-get", identity=identity, url="https://example.invalid/sheets/token",
                                                     sheet_name="测试表", flags={"range": "A1:B2", "skip_hidden": True, "enabled": False, "unused": None})
                                call = run.call_args.args[0]
                        else:
                            helper["run_sheets"]("+csv-get", identity=identity, url="https://example.invalid/sheets/token",
                                                 sheet_name="测试表", flags={"range": "A1:B2", "skip_hidden": True, "enabled": False, "unused": None})
                            call = self.argv()[-1]
                    self.assertEqual(call, [str(self.entries[platform]), "sheets", "+csv-get", "--as", identity,
                                            "--url", "https://example.invalid/sheets/token", "--sheet-name", "测试表",
                                            "--range", "A1:B2", "--skip-hidden", "--enabled=false"])

    def test_missing_entry_never_uses_path_or_network(self):
        platform = sys.platform if sys.platform in self.entries else "darwin"
        self.entries[platform].unlink()
        hostile = Path(self.env["PATH"])
        hostile.mkdir()
        for filename in ("lark-cli", "ksfas-lark", "ksfas-lark.exe"):
            entry = hostile / filename
            entry.write_text("#!" + sys.executable + "\n" + FAKE_CLI)
            entry.chmod(0o755)
        for name in API["ONLINE_COUNTS"]:
            for identity in ("user", "bot"):
                with self.subTest(name=name, identity=identity):
                    arguments = ["token"] if "chart_layout" in name else ["--spreadsheet-token", "token", "--sheet-id", "sheet1"]
                    if "profile_table" in name:
                        arguments += ["--range", "A1:B2"]
                    result = self.invoke_script(name, arguments + ["--as", identity], platform)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("Managed CLI not found", result.stdout)
        self.assertEqual(self.argv(), [])
        helper = self.load_helper()
        with mock.patch.dict(os.environ, self.env, clear=True), mock.patch.object(sys, "platform", platform), mock.patch.object(helper["subprocess"], "run") as run:
            with self.assertRaises(helper["LarkCliError"]):
                helper["run_sheets"]("+workbook-info", identity="user", spreadsheet_token="token")
            run.assert_not_called()

    def test_direct_identity_conflicts_and_unsupported_platform_fail_closed(self):
        helper = self.load_helper()
        with mock.patch.object(helper["subprocess"], "run") as run:
            for identity in (None, "", "auto", "USER"):
                with self.assertRaises(helper["LarkCliError"]):
                    helper["run_sheets"]("+workbook-info", identity=identity, spreadsheet_token="token")
            with self.assertRaises(TypeError):
                helper["run_sheets"]("+workbook-info", spreadsheet_token="token")
            for key in ("as", "--as", "as=user", "_as", "as_", "identity", "--identity=bot"):
                with self.assertRaises(helper["LarkCliError"]):
                    helper["run_sheets"]("+workbook-info", identity="user", spreadsheet_token="token", flags={key: "bot"})
            with mock.patch.object(sys, "platform", "linux"):
                with self.assertRaisesRegex(helper["LarkCliError"], "Unsupported"):
                    helper["run_sheets"]("+workbook-info", identity="bot", spreadsheet_token="token")
            run.assert_not_called()

    def test_unknown_patterns_and_upstream_drift_no_partial_writes(self):
        mutations = [
            ('subprocess.run(', 'subprocess.Popen('),
            ('            check=False,', '            check=False, shell=True,'),
            ('["lark-cli", "sheets", shortcut]', '["lark-cli", *extra, "sheets", shortcut]'),
            ('import subprocess', 'import subprocess as child'),
            ('return envelope', 'return {"changed": envelope}'),
        ]
        stage = self.copy_stage()
        name = API["SHEETS"] + "lark_sheet_read_cli.py"
        original = (stage / name).read_text()
        for before, after in mutations:
            with self.subTest(after=after):
                self.assertIn(before, original)
                (stage / name).write_text(original.replace(before, after))
                snapshot_before = snapshot(stage)
                result = self.invoke_adapter(stage)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertIn("drift", json.loads(result.stderr)["error"])
                self.assertEqual(snapshot(stage), snapshot_before)

    def test_unknown_missing_python_and_symlink_rejected(self):
        stage = self.copy_stage()
        unknown = stage / "lark-sheets/scripts/new_helper.py"
        unknown.write_text('from subprocess import run\nrun(["lark-cli", "sheets"])\n')
        before = snapshot(stage)
        self.assertNotEqual(self.invoke_adapter(stage).returncode, 0)
        self.assertEqual(snapshot(stage), before)
        unknown.unlink()
        target = stage / API["SHEETS"] / "sheets_df.py"
        target.unlink()
        self.assertNotEqual(self.invoke_adapter(stage).returncode, 0)
        if os.name != "nt":
            target.symlink_to(self.pristine / API["SHEETS"] / "sheets_df.py")
            self.assertNotEqual(self.invoke_adapter(stage).returncode, 0)

    def test_adapted_mutation_rejected(self):
        stage = self.copy_stage()
        API["adapt"](stage)
        target = stage / API["SHEETS"] / "lark_sheet_read_cli.py"
        target.write_text(target.read_text().replace('"--as", identity]', '"--as", "bot"]'))
        before = snapshot(stage)
        self.assertNotEqual(self.invoke_adapter(stage).returncode, 0)
        self.assertEqual(snapshot(stage), before)

    def test_entry_bootstrap_precedes_local_imports(self):
        for name in API["UPSTREAM_SHA256"]:
            tree = ast.parse((self.adapted / name).read_text())
            assignment = next(node for node in tree.body if isinstance(node, ast.Assign)
                              and ast.unparse(node.targets[0]) == "sys.dont_write_bytecode")
            self.assertIs(assignment.value.value, True)
            for node in tree.body:
                if isinstance(node, ast.ImportFrom) and node.module and node.module.startswith(("lark_", "xml_", "sxsd_")):
                    self.assertLess(assignment.lineno, node.lineno)

    def test_full_offline_upstream_suites_normally_no_cache(self):
        for name in ("iconpark_tool_test.py", "xml_lint_test.py", "xml_text_overlap_lint_test.py"):
            with self.subTest(name=name):
                result = self.invoke_script(API["SLIDES"] + name, [])
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("OK", result.stderr)
                self.assertFalse(list(self.adapted.rglob("__pycache__")))
                self.assertFalse(list(self.adapted.rglob("*.pyc")))
        self.assertEqual(self.argv(), [])

    def test_external_merge_anchor_propagates_exact_identity(self):
        self.env["FAKE_MERGE_ANCHOR"] = "1"
        platform = sys.platform if sys.platform in self.entries else "darwin"
        for identity in ("user", "bot"):
            start = len(self.argv())
            result = self.invoke_script(API["SHEETS"] + "lark_detect_subtables.py",
                                        ["--spreadsheet-token", "token", "--sheet-id", "sheet1", "--range", "B2:B2", "--as", identity], platform)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            calls = self.argv()[start:]
            self.assertEqual([call[2] for call in calls], ["+workbook-info", "+sheet-info", "+csv-get", "+csv-get"])
            self.assertEqual(calls[-1], [str(self.entries[platform]), "sheets", "+csv-get", "--as", identity,
                                        "--spreadsheet-token", "token", "--sheet-id", "sheet1", "--range", "A1", "--max-chars", "1024"])

    def test_cli_refusal_does_not_switch_identity_or_retry(self):
        self.env["FAKE_CLI_REFUSE"] = "1"
        platform = sys.platform if sys.platform in self.entries else "darwin"
        for name in API["ONLINE_COUNTS"]:
            for identity in ("user", "bot"):
                start = len(self.argv())
                arguments = ["token"] if "chart_layout" in name else ["--spreadsheet-token", "token", "--sheet-id", "sheet1"]
                if "profile_table" in name:
                    arguments += ["--range", "A1:B2"]
                result = self.invoke_script(name, arguments + ["--as", identity], platform)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("fixture refusal", result.stdout)
                calls = self.argv()[start:]
                self.assertEqual(len(calls), 1)
                self.assertEqual(calls[0][3:5], ["--as", identity])

    def test_dataframe_docstring_teaches_managed_identity_and_safe_import(self):
        source = (self.adapted / API["SHEETS"] / "sheets_df.py").read_text()
        docstring = ast.get_docstring(ast.parse(source))
        self.assertNotIn("lark-cli", docstring)
        self.assertIn("--as user or --as bot", docstring)
        self.assertIn("~/.local/share/ksfassistant/toolchain/bin/ksfas-lark", docstring)
        self.assertIn("~/AppData/Local/KSFAssistant/toolchain/bin/ksfas-lark.exe", docstring)
        self.assertLess(docstring.index("sys.dont_write_bytecode = True"), docstring.index("from sheets_df import"))

    def test_markdown_cli_modify_existing_only_and_idempotent(self):
        stage = self.copy_stage()
        for name in API["SNIPPET_HASHES"]:
            path = stage / name
            path.write_text(path.read_text().replace("lark-cli", "ksfas-lark"))
        before = snapshot(stage)
        command = [sys.executable, "-B", str(ADAPTER), "--markdown-root", str(stage)]
        first = subprocess.run(command, capture_output=True, text=True, env=self.env, timeout=30)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(first.stderr, "")
        report = json.loads(first.stdout)
        self.assertEqual(report["filesChanged"], sorted(API["SNIPPET_HASHES"]))
        self.assertEqual(len(report["callsites"]), 2)
        for callsite in report["callsites"]:
            self.assertIsInstance(callsite["line"], int)
            self.assertGreater(callsite["line"], 0)
            self.assertIn("subprocess.", (stage / callsite["file"]).read_text().splitlines()[callsite["line"] - 1])
        after = snapshot(stage)
        self.assertEqual(before.keys(), after.keys())
        for name in before:
            if name not in API["SNIPPET_HASHES"]:
                self.assertEqual(before[name], after[name], name)
        second = subprocess.run(command, capture_output=True, text=True, env=self.env, timeout=30)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(second.stdout), {"filesChanged": [], "callsites": report["callsites"]})
        self.assertEqual(snapshot(stage), after)

    def test_markdown_unknown_python_processes_fail_without_false_prose_matches(self):
        prose = 'subprocess.run is mentioned in prose.\n```text\nimport subprocess\n```\n```bash\necho "subprocess.run"\n```\n'
        self.assertEqual(API["adapt_markdown_snippets"]("unrelated.md", prose), (prose, []))
        for code in ('import subprocess as child\nchild.run(["ksfas-lark"])\n',
                     'from subprocess import check_output\ncheck_output(["ksfas-lark"])\n',
                     'import os\nos.system("lark-cli sheets")\n',
                     '__import__("subprocess").run(["ksfas-lark"])\n',
                     'subprocess.Popen(command)\n', 'import subprocess\nsubprocess.run(\n'):
            with self.subTest(code=code), self.assertRaises(API["AdaptationError"]):
                API["adapt_markdown_snippets"]("new.md", "```python\n" + code + "```\n")
        stage = self.copy_stage()
        name = "lark-sheets/references/lark-sheets-read-data.md"
        path = stage / name
        original = path.read_text()
        for before, after in [('subprocess.check_output(', 'subprocess.Popen('),
                              ('input=json.dumps(payload).encode(), check=True', 'input=json.dumps(payload).encode(), check=True, shell=True')]:
            path.write_text(original.replace(before, after))
            initial = snapshot(stage)
            with self.assertRaises(API["AdaptationError"]):
                API["adapt_markdown_root"](stage)
            self.assertEqual(snapshot(stage), initial)

    def test_markdown_roundtrip_fake_pandas_exact_argv_identity_and_no_cache(self):
        name = "lark-sheets/references/lark-sheets-read-data.md"
        source = (self.pristine / name).read_text().replace("lark-cli", "ksfas-lark")
        output, _ = API["adapt_markdown_snippets"](name, source)
        bodies = [output[start:end] for start, end, language in API["python_fences"](output)]
        body = next(body for body in bodies if 'subprocess.check_output(' in body)
        script = self.temp / "roundtrip.py"
        script.write_text(body)
        (self.temp / "pandas.py").write_text(FAKE_PANDAS)
        platform = sys.platform if sys.platform in self.entries else "darwin"
        cwd = self.adapted / "lark-sheets"
        command = [sys.executable, str(script)]
        if sys.platform not in self.entries:
            runner = "import runpy,sys; sys.platform='darwin'; sys.argv.pop(0); runpy.run_path(sys.argv[0],run_name='__main__')"
            command = [sys.executable, "-c", runner, str(script)]
        help_result = subprocess.run(command + ["--help"], cwd=cwd, env=self.env, text=True, capture_output=True, timeout=30)
        self.assertEqual(help_result.returncode, 0, help_result.stderr)
        self.assertEqual(self.argv(), [])
        for identity in ("user", "bot"):
            start = len(self.argv())
            result = subprocess.run(command + ["--as", identity, "--url", "https://example.invalid/sheets/token"],
                                    cwd=cwd, env=self.env, text=True, capture_output=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(self.argv()[start:], [
                [str(self.entries[platform]), "sheets", "+table-get", "--as", identity, "--url", "https://example.invalid/sheets/token", "--sheet-name", "销售"],
                [str(self.entries[platform]), "sheets", "+table-put", "--as", identity, "--url", "https://example.invalid/sheets/token", "--sheets", "-"],
            ])
            payload = json.loads(Path(self.env["FAKE_CLI_STDIN"]).read_text())
            self.assertEqual(payload["sheets"][0]["formats"], {"营收": "0.00"})
            self.assertAlmostEqual(payload["sheets"][0]["data"][0][1], 110)
        for identity_args in ([], ["--as", "auto"], ["--as", "user", "--as", "bot"]):
            start = len(self.argv())
            result = subprocess.run(command + identity_args + ["--url", "https://example.invalid"],
                                    cwd=cwd, env=self.env, text=True, capture_output=True, timeout=30)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(len(self.argv()), start)
        self.entries[platform].unlink()
        hostile = Path(self.env["PATH"])
        hostile.mkdir()
        for filename in ("lark-cli", "ksfas-lark"):
            trap = hostile / filename
            trap.write_text("#!" + sys.executable + "\n" + FAKE_CLI)
            trap.chmod(0o755)
        start = len(self.argv())
        result = subprocess.run(command + ["--as", "user", "--url", "https://example.invalid"],
                                cwd=cwd, env=self.env, text=True, capture_output=True, timeout=30)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Managed CLI not found", result.stderr)
        self.assertEqual(len(self.argv()), start)
        self.assertFalse(list(self.adapted.rglob("__pycache__")))
        self.assertFalse(list(self.temp.rglob("__pycache__")))

    @unittest.skipUnless(importlib.util.find_spec("pandas") is not None, "pandas absent; real DataFrame round-trip requires the upstream optional dependency")
    def test_dataframe_real_roundtrip_using_documented_import_no_cache(self):
        source = (self.adapted / API["SHEETS"] / "sheets_df.py").read_text()
        docstring = ast.get_docstring(ast.parse(source))
        snippet = "\n".join(line[4:] for line in docstring.splitlines() if line.startswith("    "))
        runner = "import sys\nsys.path.insert(0, sys.argv[1])\n" + snippet + '''
import pandas as pd
original = pd.DataFrame({"姓名": ["Alice", "Bob"], "Value": [42, 7]})
packed = df_to_sheet(original, "测试表", formats={"Value": "0"})
assert packed["name"] == "测试表" and packed["formats"] == {"Value": "0"}
pd.testing.assert_frame_equal(sheet_to_df(packed), original)
try:
    df_to_sheet(pd.DataFrame([[1, 2]], columns=[1, "1"]), "duplicate")
except ValueError:
    pass
else:
    raise AssertionError("Column collision policy changed")
'''
        result = subprocess.run([sys.executable, "-c", runner, str(self.adapted / API["SHEETS"])],
                                env=self.env, text=True, capture_output=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(list(self.adapted.rglob("__pycache__")))
        self.assertEqual(self.argv(), [])


def main():
    global SOURCE, SOURCE_EXPLICIT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=Path(os.environ.get("KSF_SKILLS_SOURCE", str(Path.home() / ".agents/skills"))))
    args, remaining = parser.parse_known_args()
    SOURCE_EXPLICIT = "KSF_SKILLS_SOURCE" in os.environ or any(argument == "--source" or argument.startswith("--source=") for argument in sys.argv[1:])
    SOURCE = args.source.expanduser().resolve()
    if not SOURCE.is_dir():
        parser.error("Original Skills source missing; use --source or KSF_SKILLS_SOURCE")
    unittest.main(argv=[sys.argv[0], *remaining], verbosity=2)


if __name__ == "__main__":
    main()
