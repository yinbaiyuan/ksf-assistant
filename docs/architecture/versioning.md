# Versioning

KSFAssistant maintains one product release line.

## KSFAssistant product version

`/version.json` is the only manually managed product-version source. The macOS
and Windows hosts, Shared Core, Feishu service, task CLI, managed command
client, generated compliance assets and every other KSFAssistant-owned runtime
share this version. They are not independently versioned components.

Any change to shipped KSFAssistant business, security or runtime behavior must
bump `productVersion`. `macOSBuildNumber` is a separate monotonically increasing
platform build number. Run `node scripts/sync-versions.mjs` after changing the
manifest; builds reject unsynchronized derived files.

## Feishu runtime dependencies

The product-owned Go bridge directly links the reviewed official Feishu Go SDK.
Its module version is locked by `Core/go.mod`/`Core/go.sum` and inventoried in
the generated SBOM. There is no separately versioned managed `lark-cli`
distribution and no Feishu Skill release line. Historical CLI manifests,
adapters and compatibility fixtures are not production inputs or package
artifacts.

## Compatibility versions

RPC protocols and persisted-data schemas remain independently numbered by
their owning contracts. They describe compatibility and migration, not product
or component releases, and therefore do not imply separate Core or Feishu
service versions.
