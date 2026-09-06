# Fixed official execution descriptors

The embedded `execution-manifest.json` is the local machine inventory for the pinned official CLI. It projects all 250 typed schema commands and 526 registered shortcuts, exact legacy raw API adapters, policy aliases, per-command flags, reviewed restrictions and every extracted Skills invocation. Coverage is not a claim that all commands or parameter branches are executable: inspect each descriptor's `status`, `limitations`, flag roles, unresolved metadata and the Skills `upstream-missing` entries. Local policy can further disable an executable descriptor.

The former 38-command execution allowlist no longer gates dispatch. Its legacy entries only preserve policy aliases and review-target labels. Restricted commands remain discoverable through local help/schema, but cannot execute business actions.

## Sources and regeneration

Run from the repository root, without installing anything:

```sh
python3 scripts/generate-execution-manifest.py \
  --source /path/to/cli-1.0.93 \
  --binary /absolute/path/to/pinned/lark-cli
```

Add `--check` for reproducibility verification. The generator verifies the binary against the fixed four-platform executable hash set, the cached source archive against the Skills manifest, source Go files against that archive, and every Skills file hash. It queries only local `schema` and `--help` under isolated HOME/config with remote metadata and notifiers disabled. The source release has an empty fallback typed catalog; it is never used as the 250-command schema source. A standard-library AST extractor follows `AllShortcuts`' registered domain `Shortcuts()` returns and the sheets generated flag definitions; actual help verifies the executable surface. Unresolved input metadata is restricted, not defaulted to read/user.

Official risk is retained independently. Existing product capability risk and source-reviewed `execution-overlay.json` calibrate effective risk, including query-only POSTs, local-only downloads, remote export jobs, destructive replacement and implicit uploads. Unknown effects, credential operations, unreviewed nested resources, directory synchronization, streaming DB sync, dynamic workflow execution and independent event streams stay restricted. Previously unpublished product capabilities stay restricted independently of policy overrides. Existing fixed subscription lifecycle adapters remain exact, reviewed routes.

The manifest binds the four-platform binary hash map, canonical typed schema hash, Skills/archive/overlay/generator/extractor digests and every non-test Go source in usercommand and capabilitypolicy. `PolicyVersion` is v2; approval digests include the manifest digest and all frozen command bytes.

## Shared APIs

- `CapabilitiesJSON() ([]byte, error)`: local machine manifest, including root `digest`.
- `ManifestDigest() string`: SHA256 of the embedded manifest bytes, independent of host/config/credentials.
- `Resolve(args []string) (ExecutionDescriptor, error)`: copied descriptor for a command prefix; restricted metadata remains discoverable. Raw route placeholders are for discovery only.
- `SupportsFlag(args []string, name string) bool`: command-specific flag/alias lookup, never a global allowlist.
- `ValidateArguments(args []string) error`: structural/type/identity validation without reading input files or credentials. `Freeze` completes frozen-input and conditional semantic checks.
- `Freeze(args, stdin)` / `FreezeAt(args, stdin, cwd)`: explicit user/bot identity mode; copies files and declared field input into bounded immutable command data, preserving repeatable flags, business JSON/format/token flags and original upload basenames. IM `--content` remains inline because its official field does not consume stdin.
- `Evaluate(command)` / `Decode`: revalidate frozen bytes; derive `Review.Effects` with per-capability risk and `CapabilityIDs` as their union. Consumers enforce every effect through the one local policy. User writes always require one explicit decision; there is no retry/replay authorization.
- `Materialize(command)`: reconstruct only frozen input bytes under a private stage. `Args`, `Stdin`, `Dir` and `ArtifactsDir` describe that stage. `RequiresCLIConfirmation` identifies command-level official `--yes` support for either identity; callers may append it only after their existing gate succeeds.

Inputs remain capped at 2 MiB total and 3 MiB encoded request size. JSON keys are case-sensitive business data (`A` and `a` may coexist), while the control envelope's allowed field names and duplicates remain strict. `--page-all` requires an explicit supported `--page-limit` from 1 through 100; no limit is silently injected and upstream partial-result markers must not be reported as complete.

Output format enums remain command-specific, including Base `--format ndjson --output <relative-path>`. Base record share-link creation allows at most 100 explicit IDs under high-impact sharing policy. Sheets batch-update accepts 1–100 explicit operations from `boundedSheetBatchShortcuts`, validating each through its standalone descriptor and checking every child policy effect. Nested batches, per-child workbook/auth overrides, file/stdin indirection, plural fan-out, chart/pivot/object operations and continue-on-error remain explicitly restricted. Upstream partial remote writes are not rolled back or automatically retried.

## Artifact delivery

`Command.ArtifactPlan` binds the original absolute working-directory root and explicit relative targets. Implicit filenames are assigned an explicit unique `ksfas-artifacts-...` destination directory shown in review. Absolute/outside targets, traversal and symlink parents are refused. The plan participates in approval digests.

After the child exits successfully, call `Execution.PublishArtifacts() ([]Artifact, error)`, then `Close()`. Each `Artifact` contains final absolute `Path`, `Bytes` and `SHA256`. Publication accepts complete regular staged files only, preflights conflicts and uses same-filesystem temporary files and atomic non-overwriting hard-link publication. A multi-file set is not one filesystem transaction: a later failure returns a non-nil error together with the exact already-published partial list. It never deletes those paths as rollback, because the user may already have modified/replaced them. Callers must expose partial delivery as non-success. At most 1,000 files / 512 MiB are published per execution. Publication is attempted once, not retried.

Do not publish after a failed or unknown child outcome. Explicit/mandatory download targets producing no file fail delivery. `Close()` always removes the stage, not final artifacts. Internal callers may consume staged private resources through their existing verified lifecycle instead of publishing public outputs.

Buffer upstream stdout until publication completes. Return canonical final `artifacts` separately and label upstream paths as staging/removed; do not globally rewrite arbitrary business strings. A publication failure must not leave an already-emitted `ok:true` success. Read calls without artifacts preserve the original official stdout contract.

## Tests

Ordinary `go test ./internal/usercommand` runs offline and does not require the official binary or real configuration. `TestFixedBinaryOfflineContract` runs only with an explicit `KSF_USERCOMMAND_PINNED_CLI`; the release verification scripts require it and reject an all-skipped run. It validates all 776 command help contracts and the 250 canonical typed schemas. `TestExecutionEngineClosureAndLegacyAdapters` prevents source/alias/raw-route changes from sharing an old approval identity.

No tests send real Feishu requests or use production credentials. The optional direct `execution-shortcut-probe.go` requires the official source's existing Go module cache; generation does not depend on that optional probe.
