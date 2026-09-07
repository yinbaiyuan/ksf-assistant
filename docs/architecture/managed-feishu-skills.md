# Managed Feishu Skills

Target: `0.11.0-preview.4`. This describes the managed Skills supply chain and
developer review, not a release acceptance record or permission to install.

## Source → adapt → hash → receipt

1. `runtime/lark-skills.json` locks official `larksuite/cli` source, license and
   original Skill file hashes. CLI/Skills remain pinned to `1.0.93`; the application
   version and adapter revision are independent identities.
2. `scripts/prepare-feishu-runtime.mjs` verifies the locked source and stages an
   isolated official Skills tree. `scripts/adapt-lark-skills.mjs` adapts Markdown
   and invokes the bounded Python adapter; it does not patch installed Skills.
3. The stage contains `skills/`, `LICENSE`, `manifest.json` and
   `adaptation-report.json`. The report records upstream/final hashes, adapter
   source hashes, Markdown assessments and Python callsites. The staged manifest
   retains `schemaVersion:1` and `version:1.0.93`, with **final** Skill hashes.
4. Optional manifest `adaptation` contains `schemaVersion:1`,
   `revision:"ksfas-entry-v1"`, `digest`, `upstreamVersion:"1.0.93"` and
   `upstreamManifestSha256`. Both digest fields are 64 lowercase hex characters.
   `digest` binds the exact generated report bytes; the upstream manifest digest
   binds the original lock bytes. Formatting changes affect these hashes.
5. Go toolchain validation checks the report digest when metadata exists, plus
   license and final Skill hashes. Explicit installation records adaptation
   metadata and final owned file hashes in the receipt. Status compares bundle
   and receipt identity; an unchanged CLI version cannot hide an adapter change.

Legacy bundles without adaptation metadata remain readable for explicit rollback.
Neither old-bundle compatibility nor a valid report grants permission to overwrite
user edits. Report provenance is not proof that every documented workflow works.

## Execution boundary and removal of ksfas

Adapted official `lark-*` names and domain material remain discoverable. Feishu
calls use the managed `ksfas-lark` entry, resolved to a quoted absolute user path:

- macOS: `$HOME/.local/share/ksfassistant/toolchain/bin/ksfas-lark`
- Windows: `$env:USERPROFILE/AppData/Local/KSFAssistant/toolchain/bin/ksfas-lark.exe`

Identity is explicit (`--as user|bot`). Existing command restrictions, platform
permissions, user-write approval and bot confirmation rules still apply. Missing
entry, rejection or unknown results do not authorize PATH searches, another CLI,
direct API calls, identity switching or automatic retries. Help/schema and
`managed capabilities --json` are discovery, not execution approval.

New bundles do not add the standalone `ksfas` integration Skill. Upgrades remove
the old `ksfas` directory only when the previous receipt owns its complete,
unchanged tree. Unowned Skills are left alone. The separate **task CLI stays**:

- macOS: `$HOME/.local/share/ksfassistant/toolchain/bin/ksf-assistant-task`
- Windows: `$env:USERPROFILE/AppData/Local/KSFAssistant/toolchain/bin/ksf-assistant-task.exe`

The task launcher resolves the bundled `runtime/task/<platform>` executable and
checks its hash. It is not a Feishu subcommand or a replacement integration Skill;
local reporting works independently of Desktop, Core and the Feishu bridge. See
the [task runtime contract](../../Core/internal/taskruntime/README.md).

## Offline report comparison

```sh
node scripts/review-lark-skills-upgrade.mjs \
  --before /path/before/adaptation-report.json \
  --after /path/after/adaptation-report.json
node --test scripts/test-lark-skills-upgrade.mjs
```

The executable also exports pure `compareAdaptationReports(before, after)`.
Inputs must be schema-1 reports with complete inventories. CLI stdout is one
deterministically ordered JSON object; there are no timestamps, network calls,
adapter executions or writes to inputs/locks. Exit `0` means comparison completed,
`1` means unreadable/invalid reports, and `2` means invalid arguments. Errors are
JSON on stderr. There is no automatic upgrade-approval or supported-count result.

- `before`/`after`: provenance and adapter identities, including reported Python
  changed-file metadata. `filesChanged` describes an adapter run, not an upgrade.
- `files.added|removed|modified`: full file/hash records. A change to either
  upstream or final hash is visible, even if the other hash is unchanged.
- `commandEntries.added|removed|modified`: recorded calls grouped by source
  (`markdown` or `python`), relative path and reported line. Multiple calls on one
  line are preserved. A line shift appears as removal/addition; this is not a
  semantic rename matcher, full argv reconstruction or dynamic-code analysis.
- `unresolved.before|after`: unresolved templates and unknown state/kind labels.
- `restricted.before|after`: restricted states, argument limitations, desktop-only
  operations and local-reference boundaries, with original assessment details.
- `limitations`: explicit limits of the comparison. Empty lists only describe
  the supplied report records, not complete invocation coverage or safe execution.

An `entry-adapted-template` is a reviewed command-entry template, not a tested
business workflow. Recognized Python kinds likewise record structure, not remote
support. Unknown labels such as a future `supported` state are unresolved until
the comparison contract is reviewed. Removing a restriction from a report is not
evidence that the corresponding limitation has been implemented or approved.

## Developer upgrade sequence

1. Preserve the previous shipped report, bundle and receipt evidence outside the
   new stage. Review proposed upstream source/license changes and command coverage
   before any separately authorized lock or adapter update. This review tool never
   changes pinned versions or hashes automatically.
2. Inspect original file diffs, especially new/deleted scripts and invocation
   forms. Validate the candidate source against its reviewed locks, then adapt a
   fresh isolated stage. Do not use installed Skills as source or overwrite a
   user-modified installation to make the adapter pass.
3. For Python, review the complete file inventory and each upstream SHA-256 in
   `scripts/adapt-lark-skill-python.py`. Inspect AST callsite positions, subprocess
   operation/argv shape, `run_sheets` shortcut literals/counts and explicit identity
   propagation scopes. Patch anchors must be unique and the transformed source
   must parse and retain exactly the expected identity propagation. Changed hash,
   unknown file/callsite, structural drift or noncanonical edits require review;
   do not refresh allowlisted hashes blindly, loosen checks or add a fallback.
4. Generate and compare the two reports using the command above. Review every
   added/removed/modified file and call group; retain unresolved and restricted
   entries as limitations with evidence, not as support. Inspect Python byte and
   structure diffs even when the report shows unchanged callsite summaries.
5. Run `node --test scripts/test-lark-skills-upgrade.mjs`, the adapter's Node and
   Python tests, focused Go toolchain migration tests, and the mandatory
   `scripts/check-managed-feishu-contract.sh` against the pinned local artifacts.
   The mandatory check invokes `test-lark-skills-upgrade.mjs`. The review tool
   itself neither fetches missing artifacts nor regenerates a report. Packaging
   preparation is a separate operation and may fetch its pinned build inputs.
6. Check final manifest/report digests, receipt identity, same-version adapter
   upgrades, old owned `ksfas` removal, refusal on modifications/collisions,
   repeated install and rollback. Record platform package and real workflow
   evidence separately; passing offline tests does not authorize release or local
   installation. Install only after an explicit user action and clean preflight.

## Directory conflicts are ownership conflicts

Every owned tree is checked for extra files **and directories**, not just file
hash differences. An extra empty `lark-sheets/scripts/__pycache__` therefore
blocks installation even if all hashes still match. Keep installation paused and
report the conflict; never auto-clean caches, regenerate ownership, skip directory
validation or fall back to another installation path.

Only after explicit approval for that exact empty directory may an operator use
`rmdir`, then rerun preflight. A nonempty directory or failed `rmdir` requires
separate inspection; it does not authorize `rm -rf` or deleting its contents.
Run Python helpers with `-B` to prevent new bytecode caches. Rollback and uninstall
retain the same protection for user changes and unrelated installed Skills.

See the [toolchain contract](../../Core/internal/toolchain/README.md) for receipt,
transaction, interrupted-journal and independent launcher behavior.
