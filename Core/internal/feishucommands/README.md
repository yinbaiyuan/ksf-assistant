# Daemon command adapter

`Execute(ctx, root, capability, request) (any, error)` adapts the native CLI grammar
to the existing process-owned `CapabilityService`. The caller must decode with
`feishucli.DecodeRequest`; `Execute` independently validates typed requests too.
It returns the existing JSON response objects and never writes process stdout or
reads process stdin. Request-local payloads and context propagate through handlers,
including bounded polling and workflow reads.

This package has no Core integration or task-store dependency. Task-link execution
is rejected before settings/state access. Status, snapshot, and doctor report only
Feishu state; the Core gateway is responsible for adding task-link projections.

Event-role writes are retired. `profile show` and `profile catalog` only expose
fixed service-managed event intent; catalog returns no selectable profiles.
`profile set` is rejected before state access. Status/doctor no longer use legacy
`manual-only` to suppress inbound health or credential readiness checks. Actual
message/card connection status remains independent of the compatibility profile
fields; these commands never start an additional listener. See the
[lifecycle contract](../feishu/supervisor-lifecycle.md).

Legacy `auth` mutations now return `configuration_desktop_required` before
executing the CLI or touching configuration. App setup, OAuth and remote-operator
binding belong to the owning desktop configuration coordinator. Ordinary message
target edits cannot rebind or remove an alias admitted as a remote operator;
unprotected business aliases retain their existing interface. This is a managed
entry boundary, not a restriction on arbitrary same-user programs or direct APIs.

The private-file compatibility flags resolve only to transferred payload bytes.
Actual upload inputs are staged under daemon-owned private media storage, never
opened from a client-provided payload path. Capabilities still pass through the
runtime manifest, policy, confirmation, and operation handlers. No new queue
consumer or capability executor is constructed by this adapter.

The old command helpers and their pure behavior tests live here. Integration tests
in `feishucli` exercise an isolated local IPC endpoint and temporary data roots;
they do not start the desktop application or contact Feishu.
