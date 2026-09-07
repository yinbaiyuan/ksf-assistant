# Pinned toolchain contract

`New(Config) (*Manager, error)` accepts `ResourcesDir`, `HomeDir`, `StateDir`,
`Profile`, `ConfigDir`, `DataRoot`. Empty profile means `default`; no other profile is accepted.
Empty config directory means the official `~/.lark-cli`. No credential copying,
credential export, automatic update, PATH edit, daemon, or Core connection occurs.

`Status()`, `Install()`, `Uninstall()` return `(Status, error)`. Status is read-only.
Call mutation methods only for an explicit user action. Install manages the entire
pinned component, not arbitrary component IDs or remote source URLs.

Standalone commands: `ksf-assistant-toolchain status|install|uninstall --json`.
Optional flags: `--resources`, `--home`, `--state-dir`, `--profile`, `--config-dir`, `--data-root`.
Paths are absolute, clean, non-symlink paths. Alternate home/state flags support
isolated tests; desktop hosts must supply trusted paths, not renderer input.

Success JSON: `{schemaVersion:1,ok:true,status:{version,installed,healthy,skills,
executionManifest,skillsAdapterRevision?,skillsAdapterDigest?,launcherPath,
taskLauncherPath,profile,configDir,brand,problems}}`. Optional adapter fields describe
the currently verified bundle, not proof that it has been installed. Each skill has
`name` and `state` (`absent`, `managed`, `missing`, `collision`, `edited`, `check_failed`). Error JSON is
`{schemaVersion:1,ok:false,error:{code,message}}` with exit code 1 and no raw OS
errors, command arguments, or credentials. Brand is `feishu`. Business launch
checks only the configured profile's name/brand metadata; secret fields are not
decoded into the metadata structure. Missing/mismatched configuration fails closed.

The managed directory is `~/.local/share/ksfassistant/toolchain` on macOS and
`%USERPROFILE%/AppData/Local/KSFAssistant/toolchain` on Windows. Its `bin` contains
`ksfas-lark`, `lark-cli`, `ksf-assistant-task` (with `.exe` on Windows). Adapted official
`ksf-lark-*` Skills install into `~/.agents/skills`; new bundles contain no separate
`ksfas` integration Skill. The task CLI remains an independent installed entry.
The receipt owns exact checksummed files, including all three launcher entries.
Unowned names, added files/directories, symlinks, missing files, and user edits
refuse install/uninstall. Unrelated Skills and all other installed CLIs remain intact.
Directory inventory matters even when every file hash matches: an extra empty
`lark-sheets/scripts/__pycache__` directory is an ownership conflict. Do not
automatically delete it, run recursive cleanup, regenerate the receipt or ignore
the conflict. Keep installation paused; obtain explicit approval for the exact
empty-directory removal, then use only `rmdir` and recheck. A nonempty directory
needs separate inspection and authorization; failed `rmdir` is not permission to
delete its contents. Python helpers should run with `-B` to prevent new caches.

Staging and backup directories are siblings of each destination, permitting
same-filesystem renames. An exclusive state lock serializes mutations. Every source
and previous installation is checked before committing; failures roll back already
replaced entries. A journal and backups remain if interrupted or rollback fails;
subsequent mutations and launch fail closed instead of guessing ownership. Do not
delete an interrupted lock or backup without inspecting/recovering its journal.

Launchers resolve `Resources/runtime/lark-cli/<platform>` and
`Resources/runtime/task/<platform>` from the installed app's saved Resources path,
validate bundled binary hashes, and forward stdio/exit status without Core. macOS
also bundles universal manager/task binaries in `Contents/MacOS`. Changing the app
location requires explicit toolchain reinstall; replacing the app at the same path
uses the new bundle's signed manifest. Reinstall never overrides edited local files.

CLI and 28 official Skills are pinned to `v1.0.93`; the source archive, binaries,
each Skill file, and MIT license have SHA256 provenance. macOS packaging records
both upstream executable hashes and post-signing hashes in the signed bundle.
CLI-embedded Go modules are upstream supply-chain facts, not Core dependencies.

## Skills adaptation and upgrades

The immutable upstream lock is `runtime/lark-skills.json`. Staging verifies its
source/archive, license and upstream file hashes before adapting an isolated copy.
The adapter emits `adaptation-report.json` and a staged `manifest.json` with final
file hashes. Manifest schema stays `1`, upstream CLI/Skills version stays `1.0.93`.
Optional `adaptation` metadata has `schemaVersion:1`, `revision`, `digest`,
`upstreamVersion` and `upstreamManifestSha256`; `digest` is SHA-256 of the exact
report bytes, not a reserialized JSON object. Report digests are checked when
adaptation metadata exists. A missing, edited or symlinked report fails closed.

Receipts record this optional metadata and the final installed file hashes.
Legacy bundles/receipts without adaptation remain readable for explicit rollback.
Changing adaptation identity, even with identical CLI version and Skill bytes,
makes status nonhealthy (`skills_adapter_changed`) until explicit installation.
An upgrade removes old owned `ksfas` only if its complete tree is unchanged;
unowned `ksfas` is never cleanup material. Rollback to a legacy bundle follows
the same ownership and collision checks; no fallback bypasses user modifications.

Developer review (offline; writes JSON only to stdout):

```sh
node scripts/review-lark-skills-upgrade.mjs --before /path/old/adaptation-report.json --after /path/new/adaptation-report.json
node --test scripts/test-lark-skills-upgrade.mjs
```

The comparison lists file/hash changes, recorded Markdown/Python command-entry
changes, unresolved calls and restrictions on both sides. Exit zero only means
comparison completed, not support or approval. It never updates source locks,
invokes adapters, downloads resources or edits installed Skills. Detailed review,
Python structure/hash gates and release steps are in
[`managed-feishu-skills.md`](../../../docs/architecture/managed-feishu-skills.md).

The explicit install binds the shared policy/approval data root, by default
`~/.config/feishu-bridge`. Business commands cannot override it through environment
variables. Every business command checks the same policy even without Core.
`ksfas-lark managed capabilities --json` reports the compiled execution manifest
without reading authorization or invoking Feishu. The receipt and launcher bind
its digest. Status also compares all managed executables against the current app
manager; unchanged CLI version alone does not establish an up-to-date gate.

The mandatory packaging check is `scripts/check-managed-feishu-contract.sh`.
It uses Python 3, Go and Node at build time, a hash-pinned source archive and the
host's fixed CLI with isolated configuration and remote metadata disabled.

Namespaced bundles map upstream Skill identity `lark-*` to `ksf-lark-*` in directory names, frontmatter, cross-Skill references and helper paths. Resource filenames, API and CLI identifiers remain unchanged. Adaptation reports retain upstreamPath beside final path. Status returns installationState/title/action/details; missing owned files may be repaired only after every surviving file and directory is verified. Edits, unowned entries and IO failures block installation. Transactions compare exact pre-change files and directory inventory and preserve the original missing state on rollback.

Before replacing an existing installation, a private ZIP archive of all owned pre-install trees (including empty directories) is saved beside the state directory in `toolchain.backups`. This is separate from transaction rollback and contains no Codex credentials. Rollback to the previous app must restore matching skill and launcher trees from the same archive.
