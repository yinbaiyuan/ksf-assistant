# Feishu application registration

KSFAssistant delegates application registration to the bundled official
managed `lark-cli 1.0.93-ksfassistant.1` (upstream `1.0.93`); there is no SDK registration client or second credential store.
This is the upstream PersonalAgent device-registration flow, not an API for
silently creating arbitrary enterprise applications. The user completes the
official page; tenant policy and upstream approval still apply.

## Desktop flow

The desktop exposes one entry: **扫码连接飞书**. The official Feishu page owns both
creating a new application and choosing an existing application; the desktop no
longer accepts an App ID/Secret and does not expose separate reuse or operator-bind
steps. Existing active connections block replacement, while inactive task history
does not block a fresh isolated connection.

The controlled CLI may return `registrationUser.openId` and
`registrationUser.tenantBrand` in its final private result. After validating that
the application, brand, and user belong to the same registration result, the
bridge writes an app-bound operator and the `我` message target. The raw Open ID
never enters CLI logs, desktop state, public receipts, or the CLI user list.

If the upstream result omits the Open ID, the only fallback is **补充本人授权**.
It requests exactly `contact:user.base:readonly`, binds the returned identity, and
immediately logs out that temporary user token. Ordinary messaging, cards,
callbacks, and attachments use the bot identity and do not require user OAuth.

## Process and configuration ownership

The Feishu service owns one in-memory session per data root, with the existing
authorization/execution lease and a ten-minute deadline. Repeated starts return
the same session, not another registration. Cancel and process shutdown terminate
the managed process tree and release the lease. Restart invalidates the session;
old OAuth state cannot prove that a registration succeeded.

The fixed command is `config init --new --name default --brand feishu --lang zh_cn
--json`, under the fixed `default` profile. The managed configuration root is
`~/.config/feishu-bridge/lark-cli`; the secure-storage namespace is
`ksfassistant-lark-cli`. Every managed process filters external Lark/Feishu
configuration variables before injecting this location. App Secret remains owned
by the controlled CLI and is never returned to the desktop.

The pinned upstream emits the verification link on stderr and its final masked
JSON on stdout. The adapter bounds both streams, accepts only the fixed
`https://open.feishu.cn/page/cli` URL and versioned query contract, and never logs
raw streams. The URL and QR are transient UI data, not persisted setup data.
Unexpected output, nonzero exit, timeout, or cancellation is not success.

After successful exit, the single-profile generated configuration is validated,
synced, and published with a same-filesystem, no-replace hard link. A private
recovery record covers the crash window between configuration publication and
operator binding; it is deleted immediately after the binding is durable. A
binding is never reused when its App ID differs from the current profile.

Cancellation cannot delete an application that may already have been created on
Feishu. An unknown outcome requires checking the developer console before a new
attempt. This release has no automated recovery UI for retained stages and no
remote application deletion or automatic retry.

## Progressive authorization and logout

User-identity capabilities derive their exact scopes from the reviewed managed
command descriptor. Core persists one app-bound authorization request and gives
the desktop only its ID, purpose, and scopes. The authorization flow accepts the
existing grants plus that request's missing scopes; it cannot accept an arbitrary
scope string from the host. Successful authorization does not replay the original
operation.

**注销并清除飞书** acquires the exclusive execution lease, disconnects active
task links without stopping Codex tasks, cancels registration/OAuth sessions, and
best-effort revokes user tokens. It then removes the isolated profile, App Secret,
UAT, TAT, dedicated master key/Windows DPAPI storage, app-bound operator, message
target binding, setup state, recovery data, and authorization requests. Task
history, secret-free audit, installed Skills, Codex tasks, and the application in
Feishu's developer console remain. A failed local stage remains fail-closed and
offers **继续清理**; an unconfirmed remote revocation is reported after local data
has still been deleted.

## Verification and acceptance

`Core/internal/feishu/auth_app_session_test.go` uses an isolated fake CLI and
configuration; it never contacts Feishu or a real credential store. Coverage
includes registration with and without a user, private recovery, brand and App ID
binding, URL/output bounds, authorization leases, cancellation, changed
configuration, and competing writes. Service and host tests cover the single
desktop entry and the exceptional fallback only.

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
