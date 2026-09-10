# Feishu configuration lifecycle

## Authority and scope

Core composes a single configuration snapshot; hosts do not derive overall readiness from the legacy setup stage. Official CLI configuration/authentication, product access policy, runtime observations, and an active setup operation are separate authorities. This interface neither grants business permission nor makes managed read/bot commands depend on the desktop.

Configuration reads never bind an operator, change a feature mode, restart a process, start authorization, or send a test message. Official CLI credential maintenance remains upstream-owned. Missing evidence is unknown, not verified. An existing live configuration remains live when its setup record is absent, incomplete, or cancelled.

## Desktop contract v2

`feishu/configuration/read` accepts `{ "refresh": false }`; true explicitly refreshes slow checks. It returns:

- `schemaVersion: 2`, `epoch: string`, `revision: uint64`, `contextRevision: string`, `observedAt: RFC3339`, `refreshing: bool`.
- `refreshing` reports a slow identity/permission verification or configuration action, not routine fast background observations. Internal refresh coalescing remains active for both kinds; a settled page does not show perpetual progress merely because every poll starts its next quick observation.
- `summary: {state, title, detail, tone}`; tones are neutral, warning, success.
- `facts: [{id, title, state, value, source, checkedAt, stale}]`. States distinguish unknown, present, missing, failed, stale. IDs include application, user, bot, permissions, connection, operator, outbound, actionbox, desktop.
- `actions: [{id, title, enabled, reason, confirmation}]`; these are UI affordances, not authorization tokens.
- `setup`: the existing version-1 setup record, for diagnostics/legacy compatibility only.
- Optional `auth` and `overview`: existing sanitized typed contracts, observed in this read cycle; `connection`: the existing public bridge snapshot.
- Optional `flow: {id, kind, state, expiresAt, verificationURL, userCode, qrDataURL}`. QR data and links are transient; only a matching active session may supply them.
- `issues: [{component, code, message}]`; safe diagnostics only, never secrets or raw CLI output.

`feishu/configuration/action` accepts `{action, requestId, epoch, revision, contextRevision, confirm, authorizationRequestId?, targetAlias?, flowId?}`. Desktop cannot submit an App Secret or invent scope. `authorizationRequestId` must identify the app-bound request generated from a reviewed command descriptor. Request IDs are unique per user gesture. It returns `{outcome, snapshot, message}`; outcome is completed, pending, failed, or unknown. Both hosts discard late snapshots after a newer local operation or Core epoch change. An error is not an empty initial state.

正常页面只提供 `create_app`（“扫码连接飞书”）、按需出现的 `start_auth`、`finish_auth`、`finish_app`、`cancel_flow`、`logout`、`test_message` 和 `restart`。`connect_app`、`bind_operator` 及旧功能开关 action 在 v2 中明确拒绝。注册返回本人身份时，`finish_app` 只公开 `operatorBound: true`；缺失时公开 `operatorAuthorizationRequired: true`，原始 `open_id` 不离开飞书桥。

Mutations are serialized per Core configuration context, are checked again immediately before execution, and are not replayed on unknown results. Cancelling a local flow does not undo remote completion or revoke existing authorization. Starting/cancelling flows invalidates stale observation/QR responses. Old mutating endpoints must not bypass this coordinator.

## Implementation and acceptance ledger

The checked items below record implementation and automated verification, not complete real-device acceptance. The remaining real-device item is explicitly outstanding.

- [x] Authoritative snapshot, strict evidence, bounded/coalesced refresh, context revision.
- [x] Pure checking, explicit operator binding, independent enabling/testing, scoped cancellation.
- [x] macOS/Windows restoration, loading/partial/stale states, late-response protection in automated tests.
- [x] Existing live configuration with incomplete/missing setup, restart and cancellation recovery in isolated fixtures.
- [x] No false permission success for omitted fields; user and bot remain independent.
- [x] Isolated lifecycle/side-effect/concurrency regression tests and existing approval regressions.
- [x] Go full/related race/vet, Swift, Windows Node, four-target build, universal2 signature verification.
- [ ] Real local quit/relaunch/configuration-page acceptance; any real binding/auth/test requires the user's confirmation.

No KSF governance, credential migration, Skills changes, queue restoration, public release, or unrelated task-card changes are included.

## Observation and execution details

The first read may return an empty `contextRevision` and a `checking` summary. This is a valid loading snapshot, not a protocol failure. The host must accept it for display; available actions, current context checks and native confirmation govern mutations. Facts retain their own observation times. A failed refresh cannot be interpreted as a missing application or revoked authorization. In the pinned official CLI, `available:false` also occurs with `status:verify_failed`; that combination is failed evidence, not proof of logout.

Quick observations and active-flow reads are coalesced at a 2.5-second floor. Successful slow identity/permission checks are reused for two minutes unless explicitly refreshed; failed checks retry after a ten-second backoff. They have a bounded lifetime and are cancelled when a configuration mutation or shutdown supersedes them. Quick evidence is published before slow checks finish. Wall-clock changes and successful repeated observations alone do not increment the semantic revision. Hosts stop polling when the configuration page is hidden.

Display revisions order snapshots; the context digest binds configuration gestures. An older display revision remains acceptable when the epoch and context still match and the action is still allowed. Future revisions are rejected. This prevents unrelated connection observations from dismissing an otherwise valid native confirmation. The application fact identifies the verified App ID; it does not invent an application name when the pinned upstream status command does not provide one.

基础连接只比较固定的机器人消息、卡片、回调和附件权限，不再把 119 项潜在用户权限作为接入门槛。Bot identity、消息传输、app-bound operator 与可选用户 OAuth 分别表达。用户能力执行前按描述符生成精确授权请求，只授权已有 scope 与本次缺项；成功后要求用户重新执行原操作。App creation 与 OAuth flow 使用进程内随机 ID，过期和非 pending 流程不暴露二维码。

The ordinary compatibility gateway cannot configure an app, start/finish authentication or bind the current user. These old commands return `configuration_desktop_required`; business operations retain their existing contracts. Ordinary target updates also cannot change or remove an alias already admitted as a remote operator. Configuration desktop actions are not exposed on the ordinary CLI gateway. A `confirm` flag only has meaning on the owning host control channel; it is not an Agent approval credential.

Configuration action requests are bounded and deduplicated per Core epoch. A consumed request cannot be replayed, including when its outcome is unknown. At 128 consumed configuration gestures in one Core lifetime, new gestures are refused with a restart instruction rather than evicting records and enabling replay. Restart invalidates the old epoch; it never restores an old business queue.

## Verification notes

`Core/internal/service/configuration_test.go` covers actual private-RPC reads, independent identities, legacy-stage independence, changing contexts, explicit operator binding, single-use gestures, unknown sends and late-refresh rejection. `Core/internal/feishu/configuration_evidence_test.go` uses isolated fake CLI processes, including verification failure, omitted scopes, changing configuration files, active OAuth and scoped cancellation. `Core/internal/rpc/configuration_test.go` and the compatibility executor tests exercise the disabled mutation paths rather than treating a source scan as proof.

The desktop wire fixture is generated from Core's snapshot constructor by `TestConfigurationDesktopWireFixture`; `KSF_CONFIGURATION_FIXTURE_OUTPUT` may point to an isolated `ksfas-configuration-fixture.json` artifact for review. Rendered fixtures prove layout only, not live authorization, transmission or Desktop task control. Full-suite/build results and real local switch acceptance must be recorded separately before checking the remaining acceptance boxes.

## Local candidate verification — 2026-09-06

The dirty working tree was recorded in a 659-path SHA-256 source manifest before the final build. A post-build comparison found no source changes. No Git staging, commit, push, notarization or public release was performed. Existing unrelated working-tree changes were preserved.

- Full Go tests and `go vet ./...` passed: local log `ksfas-lifecycle-final-go-vet.log`.
- Related lifecycle race tests passed; the final operator-binding/client-configuration/configuration race tests passed in the Feishu, service and private bridge packages: `ksfas-lifecycle-current-race.log` and `ksfas-operator-binding-race.log`.
- Swift XCTest passed 87 tests. Windows Node passed 140 tests. The retained bridge Node suite passed 284 tests. Native host pipe and isolated AppKit quit lifecycle tests also passed.
- Four Go targets built; the app and bundled helpers contain both macOS architectures. The universal2 candidate passed deep strict ad-hoc signature verification. Mandatory fixed-CLI checks covered all 776 help contracts and managed Skills provenance.
- Local installation completed at 23:53 CST. The previous application was retained as `KSFAssistant.previous.20260906-235358.84996.app`. Installed app and Core bytes match the built candidate. The existing manager reported CLI 1.0.93, 28 Skills and no toolchain problems; no Skills replacement was needed.
- The candidate launched with one observed app/Core/bridge process chain. Settings, setup state and client access-policy file hashes remained unchanged across installation and launch. This observation does not prove a healthy message connection.
- Native UI automation repeatedly timed out, including after a fresh automation session. The actual configuration page and user-driven quit/relaunch recovery are not accepted yet; a current screenshot was requested. Windows hardware acceptance remains unperformed. No real authorization, operator binding or test message was triggered.
- A one-second sample of the installed app showed its main thread predominantly waiting in the AppKit event loop, not continuously blocked in a configuration call; this is not proof of UI correctness. A real parent-only SIGTERM test left no app/Core/bridge/CLI descendants, preserved all three checked configuration file hashes, and a subsequent launch restored a single managed process chain. This verifies process-exit cleanup and relaunch, not the native menu Quit action or the restored configuration-page presentation.

Operator binding carries the confirmed application and privacy-preserving identity/context digests to the private bridge. The bridge obtains the managed execution lease and verifies identity inside the client-config compare-and-swap guard before saving the exact verified user. Changed app, user or policy returns a distinct no-write conflict; old context-free binding is rejected. No raw user identifier is returned in configuration evidence.

Rollback uses the preserved application and, only if needed, the existing managed-file ownership checks. It never restores a prior business queue or replays unknown operations. The local candidate remains **awaiting real-device acceptance**.

## Screenshot review — 2026-09-07

Two user-provided screenshots confirmed that the installed page identifies the existing application and shows a consistent connected message summary, authorized user, admitted current operator and separately scoped incomplete permissions. They also showed the Desktop control channel as reachable, without claiming individual task acceptance. The legacy `platform_pending` record no longer determines the overall state.

The screenshots exposed a presentation defect: each 2.5-second quick observation started a background refresh before returning, so hosts repeatedly received `refreshing: true` even after the prior observation had completed. Core now distinguishes slow verification from routine observation for visible progress; both remain internally coalesced. An actual private-bridge fixture tests three successive quiet observations followed by an explicit slow check and completion. Windows also keeps transient cached reads quiet. Legacy setup stage remains available in the wire contract for compatibility but is removed from normal diagnostics in both hosts.

Focused configuration tests, full Go tests, configuration Race tests, vet, Swift 87 tests and Windows 141 tests passed for this correction. Four Go targets and the universal2 package built successfully; deep strict signature verification and all 776 fixed CLI help contracts passed. The 659-path build manifest had no concurrent source changes. Local evidence logs use the `ksfas-config-screen-` prefix.

The correction was installed at 04:56 CST, retaining `KSFAssistant.previous.20260907-045612.99401.app`. Installed app/Core bytes match the candidate, the app/Core/bridge process chain started, and the three checked configuration files remained byte-identical. Toolchain status reported no problems and 28 Skills; Skills and PATH were unchanged. A real settled-page check is still required: the supplied screenshots describe the preceding candidate, not acceptance of this correction. Native menu Quit/reopen remains unverified.
