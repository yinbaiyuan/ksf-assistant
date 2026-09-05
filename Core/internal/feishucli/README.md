# Native Feishu CLI boundary

`Run(ctx, root, args, stdin, stdout)` is the client entry point. It parses a fixed
command grammar, reads only explicitly supplied input files or the injected stdin,
and forwards a typed `Request` to the application's `cli/execute` local IPC method.
It does not load settings, credentials, SDKs, task stores, or queues. An unavailable
application returns `service unavailable`; the client never starts the application.

`Parse` and `Validate` share command-specific allowlists. `DecodeRequest` also
rejects unknown JSON fields, duplicate keys, trailing JSON, and oversized input.
Flag keys have no `--` prefix. Nested actions use `/`, such as `comments/add` and
`directory/search`. Input bytes use the original flag name as their payload key;
the input path is never sent. At most one file flag can consume `-` stdin.

Manifest-declared input-file fields in capability JSON payloads are also read on
the client and replaced by typed byte payloads. The daemon rejects wire payloads
that still contain these input paths. Explicit `--<input-field>` upload flags can
be used instead; only manifest-declared file fields are accepted.
Safe filename extensions are carried as `Options["input-extension-<file-flag>"]`
so uploads retain their media type without exposing a client path or basename.

The Core gateway owns `task-link`. All other executable commands go to the daemon's
`bridge/client/execute`, which calls `feishucommands.Execute` with the existing
process-owned capability service. The daemon validates the same request again.

Help, version, profile/event catalogs, and capability catalog/get are available
offline. `catalog.json` is a generated read-only projection of the reviewed runtime
manifest, not an independently edited authority. Generate its replacement from the
Core directory with `go run ./internal/feishucommands/cataloggen`; its stdout is the
replacement JSON. `TestStaticCatalogMatchesRuntimeManifest` detects drift.

Ordinary private inputs remain bounded to 4 MiB in total. `send --media-file`
accepts the previous 30 MiB maximum, including a file supplied via stdin. Parsing
and logical request validation are independent of transport frame size. The
maximum assembled JSON request is 44 MiB; oversized input is never truncated.

`Run` opens one `localipc.Session` and uses `CallRequest`. Small requests use the
normal method; larger JSON requests use the bounded protocol documented in
`UPLOAD.md`. Every individual IPC frame remains within the existing 4 MiB limit.
No offline staging, queue, storage, network backend, or application startup is
used as a fallback. A running Core gateway and daemon must both install
`NewUploadHandler`, with the forwarding caller pinned to the same daemon epoch.

An authorization-required RPC error (`-32063`) retains its structured Data JSON
on stdout, including the original public operation and challenge. `Run` still
returns the error for a nonzero exit. Neither confirmation nor business-request
replay is automatic.
