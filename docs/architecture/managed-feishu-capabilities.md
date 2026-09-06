# Managed Feishu execution contracts

Target release: `0.11.0-preview.3`. Implementation and validation status are recorded separately; this design is not a production acceptance record.

## Boundary

The bundled official CLI and official Skills remain pinned to `v1.0.93`. KSFAssistant adapts execution and owns approval, not Feishu business implementation. All entry points retain existing disabled capabilities and platform permission checks. User reads are allowed by default without a running desktop. User writes need one native decision. Bot operations do not acquire an additional desktop approval requirement and retain existing confirmation rules.

The fixed release binary's offline `schema` is the typed-command inventory. Its 250 entries are not interchangeable with the empty fallback metadata in a bare source checkout. Shortcut registration, fixed-binary introspection and reviewed source semantics complete the inventory. Skill examples are coverage inputs, not authoritative execution instructions or automatic permission grants. Schema, source archive, binary and execution contract identities are bound to the packaged toolchain.

## One execution description

The shared command adapter resolves command aliases, field types, conditional requirements, identity, full effects, input transport and result handling. Launcher arguments and the frozen compatibility service both use it. The compatibility service keeps its existing public capability IDs and published surface; new official capability support does not expand that legacy API.

Internal bot transport send/reply/card patch/read/media calls also enter the same business executor and retain their legacy policy IDs. They are not exemptions from policy or input freezing. The remaining direct internal CLI runner accepts only the exact owned event status/stop lifecycle contracts, never arbitrary business commands. Authentication and health inspection keep their separate managed control boundary.

Execution preparation freezes the approved bytes before the UI. It does not assume `--json` is boolean, `--format` selects output, or every `content` flag supports stdin. Business resource tokens and case-sensitive JSON object keys are not credential overrides. Control envelopes, actual credentials, authorization context and execution overrides remain strictly validated. Unsupported or unbounded effects are rejected before any remote write.

`internal/capabilitypolicy` owns the existing `feishu-capability-policy-v1.json` contract. Both the service and standalone launcher load the same policy; there is no second store and no desktop dependency for policy reads. Invalid policy data does not silently fall back to defaults. Approval independently reconstructs the reviewed command and checks its policy before granting and consuming a one-use decision.

The policy/approval data root is bound during explicit toolchain installation and covered by the ownership receipt. Business invocation environment variables cannot select a fresh empty policy directory. Existing `confirm_each` overrides remain confirmation requirements; an unprompted read/bot path cannot silently turn them into `allowed`.

All business executions acquire the same authorization/execution lease before their final identity check, including reads and bot calls. This prevents managed login/logout/configuration changes from switching the selected identity during execution, without requiring a desktop for reads or bot calls. Contention returns busy; a user approval never waits while holding this lease. Internal preflight reads reuse only the executor-owned context for the already-held lease.

The inventory is available through `ksfas-lark managed capabilities --json`. It is local diagnostics, not an approval endpoint. Help and schema discovery do not grant permission to execute a business operation. Unknown API paths are not inferred safe from their HTTP methods.

## Explicit limitations

- Script execution, dynamically expanding write targets, workflow activation and independent event consumers require a separate execution/lifecycle boundary; discovery alone does not enable them.
- Existing input limits remain: 2 MiB frozen content and 3 MiB encoded command envelope. Over-limit input is rejected rather than truncated for approval.
- The existing native approval review additionally limits displayed content to 256 KiB and each target/header field to 64 KiB. Oversized user writes fail before approval; file bytes remain represented by names, lengths and hashes rather than expanded attachment content.
- Downloads and exports must preserve the caller's explicit destination and report delivered artifacts, never temporary paths deleted on return. A remote export-job creation is an effect distinct from local download.
- Artifact-producing calls publish only after a successful CLI exit. JSON object results gain canonical `artifacts` (absolute path, size, SHA256) and `artifactDelivery`; other output is preserved as `upstreamOutput` inside a versioned JSON object. Original upstream paths name removed staging data and are not delivery paths. Existing destination files are never overwritten. Captured output is limited to 8 MiB, artifacts to 512 MiB and 1,000 files; excess returns an explicit failure, not partial success.
- An unknown result after execution begins is not retry authorization. Do not replay a partially completed compound operation or restore old queues.
- Multi-file delivery is atomic per file, not a filesystem-wide transaction. A late conflict reports already published paths as partial artifacts and fails the invocation; it never deletes published paths that may have been edited by the user. Re-running the business operation is not a recovery mechanism.
- Output companion paths belong to the descriptor, not a guessed filename prefix. Base NDJSON explicitly binds both `name.ndjson` and `name.manifest.json` using the pinned exporter's suffix rule; either file missing prevents publication of the incomplete pair.
- A same-user process can still bypass the managed product outside this boundary. Native approval is not a sandbox or proof of a physical human click.

## Upgrade and verification

Toolchain status checks the installed launcher against the current bundled manager, in addition to the official CLI version, ownership receipt, Skills and execution manifest. An unchanged, owned launcher can be upgraded; user-edited files stop the transaction. Old receipts remain readable for an explicit upgrade but cannot authorize execution under a mismatched manifest. No queue or credential migration is introduced.

Release verification must cover every official command and Skill invocation with supported behavior or an explicit reviewed restriction. It must exercise parameters, complete domain read/write flows, policy parity across entry points, single-use approval, content mutation, lifecycle failure, output delivery, and pinned upstream offline contracts. Passing a catalog-count test does not prove an operation works.

The local preview requires separately recorded real read and user-clicked approval evidence. Other domains without authorized test resources and Windows without hardware validation remain explicitly unverified. No public release, credential export, automatic approval click, unrelated resource creation or business-queue restoration is part of this change.
