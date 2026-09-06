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
launcherPath,taskLauncherPath,profile,configDir,brand,problems}}`. Each skill has
`name` and `state` (`absent`, `managed`, `collision`, `edited`). Error JSON is
`{schemaVersion:1,ok:false,error:{code,message}}` with exit code 1 and no raw OS
errors, command arguments, or credentials. Brand is `feishu`. Business launch
checks only the configured profile's name/brand metadata; secret fields are not
decoded into the metadata structure. Missing/mismatched configuration fails closed.

The managed directory is `~/.local/share/ksfassistant/toolchain` on macOS and
`%USERPROFILE%/AppData/Local/KSFAssistant/toolchain` on Windows. Its `bin` contains
`ksfas-lark`, `lark-cli`, `ksf-assistant-task` (with `.exe` on Windows). Real official
Skills and the small `ksfas` integration Skill install into `~/.agents/skills`.
The receipt owns exact checksummed files, including all three launcher entries.
Unowned names, added files/directories, symlinks, missing files, and user edits
refuse install/uninstall. Unrelated Skills and all other installed CLIs remain intact.

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
