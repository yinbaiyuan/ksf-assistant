# Versioning

KSFAssistant maintains two release lines.

## KSFAssistant product version

`/version.json` is the only manually managed product-version source. The macOS
and Windows hosts, Shared Core, Feishu service, task CLI, managed command
client, generated compliance assets and every other KSFAssistant-owned runtime
share this version. They are not independently versioned components.

Any change to shipped KSFAssistant business, security or runtime behavior must
bump `productVersion`. `macOSBuildNumber` is a separate monotonically increasing
platform build number. Run `node scripts/sync-versions.mjs` after changing the
manifest; builds reject unsynchronized derived files.

## Managed lark-cli version

`/runtime/lark-cli-runtime.json` owns one version for the complete managed
lark-cli distribution. It combines the upstream source version and the exact
KSFAssistant patch revision, for example `1.0.93-ksfassistant.1`. The patch has
no independent release version: its path and SHA-256 remain only as auditable
provenance.

Changing either the upstream source or the patch requires a new managed
lark-cli version and new hashes for every platform binary. `upstreamVersion`
is retained only to bind source archives, Feishu API metadata and official
Skills to the reviewed upstream release. It is not a third release line.

If a managed lark-cli change also changes shipped KSFAssistant behavior, the
KSFAssistant product version must be bumped as well.

## Compatibility versions

RPC protocols and persisted-data schemas remain independently numbered by
their owning contracts. They describe compatibility and migration, not product
or component releases, and therefore do not imply separate Core or Feishu
service versions.
