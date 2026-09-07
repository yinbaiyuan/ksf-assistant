# Feishu application registration

KSFAssistant delegates application registration to the bundled official
`lark-cli 1.0.93`; there is no SDK registration client or second credential store.
This is the upstream PersonalAgent device-registration flow, not an API for
silently creating arbitrary enterprise applications. The user completes the
official page; tenant policy and upstream approval still apply.

## Desktop choices

- **扫码创建应用** starts a bounded managed registration session for first-time
  configuration. An existing `config.json`, including an unreadable or malformed
  file or other profiles, refuses creation before the registration process starts.
  This release does not implement application switching or profile merging.
  Existing enabled business channels also refuse creation; the guard is checked
  again before publication so a new app cannot silently resume old work queues.
  Existing target files, task-link/integration-event files, legacy queues, inbound
  work and workbox records also refuse creation without parsing, clearing, or
  migrating those business facts. This is intentionally stricter than merely
  checking whether an App ID is currently available.
- **接入已有应用** retains the existing private-stdin credential entry flow.
- **沿用当前应用** checks the official CLI's existing `default` Feishu profile and
  resumes the product setup wizard without copying credentials. Empty desktop
  input fields are not evidence that the CLI is unconfigured or logged out.

Application setup, user OAuth, permissions, message/card connection, and product
activation remain separate states. Creation completion transitions only to
`app_configured`. A subsequent explicit action starts user OAuth. No test message
is sent by creating or reusing an application, and no existing authorization is
revoked just because the product setup wizard is incomplete.

## Process and configuration ownership

The Feishu service owns one in-memory session per data root, with the existing
authorization/execution lease and a ten-minute deadline. Repeated starts return
the same session, not another registration. Cancel and process shutdown terminate
the managed process tree and release the lease. Restart invalidates the session;
old OAuth state cannot prove that a registration succeeded.

The fixed command is `config init --new --name default --brand feishu --lang zh_cn
--json`, under the fixed `default` profile. Only `LARKSUITE_CLI_CONFIG_DIR` is
redirected to a private `.ksfas-registration-*` directory inside the official
configuration directory. HOME and the official Keychain/Windows DPAPI location
remain unchanged. App Secret is stored by the official CLI; KSFAssistant accepts
only the expected Keychain reference in the generated configuration. It never
resolves, exports, or copies the underlying secret.

The pinned upstream emits the verification link on stderr and its final masked
JSON on stdout. The adapter bounds both streams, accepts only the fixed
`https://open.feishu.cn/page/cli` URL and versioned query contract, and never logs
raw streams. The URL and QR are transient UI data, not persisted setup data.
Unexpected output, nonzero exit, timeout, or cancellation is not success.

After successful exit, the single-profile generated configuration is validated,
synced, and published with a same-filesystem, no-replace hard link. If another
process creates the destination, it wins: KSFAssistant refuses to overwrite it.
Unsupported filesystems fail closed rather than falling back to replacement.
Successful temporary directories are removed. When failure occurs after the CLI
has produced a configuration, its private stage is retained for recovery; it is
not automatically installed, replayed, or merged. Existing configurations,
credentials, task links, and queues are not restored from stale copies.

Cancellation cannot delete an application that may already have been created on
Feishu. An unknown outcome requires checking the developer console before a new
attempt. This release has no automated recovery UI for retained stages and no
remote application deletion or automatic retry.

## Verification and acceptance

`Core/internal/feishu/auth_app_session_test.go` uses an isolated fake CLI and
configuration; it never contacts Feishu or a real credential store. Coverage
includes split stderr URL output, version/URL validation, bounded output,
registration-before-publication, authorization leases, repeated start, pending
status, cancellation, changed configuration, nonzero exit, secret-bearing output,
and competing configuration writes. Service tests cover separate creation/OAuth
stages, transient URL projection, restart invalidation, and existing-profile reuse.
Swift and Windows UI tests exercise entry selection and pending states.

The implementation and automated tests are not real registration acceptance.
Real QR scanning must be performed by the user in an explicitly chosen fresh
configuration context. Do not clear a working user's CLI configuration to make
this test possible. Windows hardware behavior remains a separate acceptance item.

Upstream review inputs: `cmd/config/init.go`, `cmd/config/init_interactive.go`,
`internal/auth/app_registration.go`, `internal/core/config.go`,
`internal/core/secret_resolve.go`, and `internal/keychain/keychain_windows.go` in
the source archive pinned by `runtime/lark-skills.json`. Changes to upstream
registration output, configuration shape, credential storage, or version require
explicit contract review; never infer compatibility from a successful QR render.
