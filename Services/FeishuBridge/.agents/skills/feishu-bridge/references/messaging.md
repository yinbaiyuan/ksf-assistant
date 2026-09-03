# Messaging, directories, cards, and task links

## Outbound

The bridge sends text, Markdown, cards, images, and files. Content must come from stdin or a file, never be interpolated into a shell command.

Targets may be a configured alias, explicit target from the current request, unique exact person name, or unique exact group name. Person and group caches are read-only. A group-name target is limited to groups the bot has joined.

Always perform a dry-run with the same target, format, and content before a real send. The real send receives a new request ID. Outbound has no local target allowlist, but current-request authorization, queueing, redaction, dedupe, and audit remain mandatory.

## Reads

- Group message list: exact group, timezone-aware start/end, at most 50 results and 31 days.
- Group message search: the same boundaries plus a keyword, at most 20 results.
- Thread read: explicit message ID through stdin/private file, at most 50 results.
- Do not enable automatic pagination or background history crawling.

## Inbound and Codex

Authorized direct chat supports text, image, file, audio, video, and post. Attachments are staged privately, size-limited, and removed after processing. Group attachments are not sent to Codex.

The bridge supports local commands, new Codex tasks, resumed tasks, and a default conversation. Processing and terminal output update the same Feishu card when possible. Card actions are fixed; they cannot run arbitrary commands or create broader authorization.

Task links can observe, continue, answer, interrupt, or release an already-bound top-level Codex task. Long-text/attachment mode grants one authorized direct-chat message within the configured short window. Do not claim Desktop task control is available unless `task-link protocol` reports it ready.

## Ambiguity

For duplicate names, show the minimum person department path or group description/external marker plus redacted fingerprints. Bind only the user's selected candidate. Never choose the first result automatically.
