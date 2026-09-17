# Feishu application registration

KSFAssistant uses Feishu's official PersonalAgent device-registration protocol directly from the product-owned Go bridge. It does not execute or package `lark-cli`, does not install Feishu Skills, and does not expose an arbitrary OpenAPI proxy.

## One-scan flow

The desktop exposes one entry: **扫码连接飞书**. Shared Core starts the official registration session, validates the returned `/page/cli` contract, generates the QR locally, and adds exactly these required callbacks:

- `im.message.receive_v1`
- `card.action.trigger`

The official page remains responsible for user confirmation, tenant policy and any administrator approval. Windows and macOS use this same Core flow; neither host carries separate permission or registration logic.

Registration requests the current user's `open_id` together with the application credentials. A successful result must contain both. Core then stores an app-bound operator plus the `我` message target, so application setup, user binding, bot messaging and task-card callbacks complete in one scan. A result without the user identity fails closed and requires a fresh scan; there is no second user-OAuth repair flow.

## Credential ownership

Application metadata lives under the product's private Feishu data root. The App Secret is protected for the current OS user:

- Windows: DPAPI-encrypted payload in `credentials/official-sdk.json`.
- macOS: secret in Keychain service `com.ksfassistant.feishu`, with only its account reference in `credentials/official-sdk.json`.

Existing installations may read the previous managed-CLI credential once and copy it into the native store. Production transport subsequently reads the native store only; the old CLI binary and profile are not runtime dependencies.

## Runtime boundary

The official Go SDK owns one WebSocket connection and registers only the two callbacks above. Product-owned OpenAPI code is restricted to the task-message and CardKit operations accepted by the internal transport contract. It does not provide generic document, calendar, approval or other Agent business operations.

Configuration evidence validates the native credential, bot identity, app-bound operator and event connection. Windows uses DPAPI and macOS uses Keychain, but all business decisions and DTOs remain shared Core behavior.

## Cancellation and logout

Only one registration session exists per data root. Repeated starts reuse that session; cancellation stops polling and clears transient QR data. Cancellation cannot delete an application that may already have been created on Feishu, so an unknown outcome must be checked in the developer console before retrying.

**注销并清除飞书** disconnects active task links without stopping Codex tasks and removes product-owned credentials, operator/target bindings, setup state and private recovery data. It does not delete the remote Feishu application or unrelated external CLI credentials.

## Verification

Automated coverage verifies URL/addon constraints, native credential protection, operator binding, internal SDK/OpenAPI packaging and the absence of CLI/Skill artifacts. Release acceptance still requires a user-approved real QR scan, bot message send, task connection, card update and card callback on Windows and macOS hardware.
