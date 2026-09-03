# Calendar, tasks, structured data, meetings, Apps, Events, and workflows

## Calendar and tasks

Calendar and task operations use user identity. Reads require explicit targets and bounded date/result ranges. Writes require complete fields and timezone-aware timestamps. For recurring events, establish whether the change affects one occurrence, all occurrences, or this and following occurrences.

Supported task areas include comments, followers, parent/child tasks, tasklists, attachments, sections, custom fields, assignment, reminders, completion, and reopening. Delete, member-permission changes, and task-agent automation are unavailable.

## Sheets and Base

Sheets reads require a sheet and bounded A1 range. Base reads require the base/table, a record limit of at most 200, and only needed fields. Structured payloads come from stdin or a private file.

Sheets covers workbook import/export, sheets, dimensions, styles, images, merges, replacement, range operations, charts, pivots, conditional formatting, filters, dropdowns, sparklines, and floating images. Base covers records/history/attachments, templates/workspaces, bases/apps, tables, fields, views, forms, dashboards, pages, and blocks.

Read the real schema or revision before a write. Replacement, movement, overwriting non-empty cells, and structural updates require high-impact confirmation and reread. Deletion, clearing, sharing, permissions, Base workflows, and button automation are unavailable.

## Meetings, Note, and Minutes

Search requires a keyword, time range, owner/organizer, or participant and stays within the bridge's limits. Transcript reads use private temporary files and a character limit. Minutes supports upload, title update, summary replacement, todo add/update, word replacement, and speaker replacement. Summary, word, and speaker replacement are high impact. Live meeting control, raw media download, permission changes, and deletion are unavailable.

## Apps

Apps supports app/session/release/log/metric/trace/analytics reads and controlled app creation/update, session creation/chat/stop, and release creation. Chat and release are remote operations with fixed status polling. Access scope, members, roles, keys, environment variables, databases, automation, cache, plugins, Git credentials, and deletion are unavailable.

## Events

Only the bridge's fixed non-Approval EventKeys may be queried or watched. Events are stored in the private inbox with dedupe and redaction and never trigger business writes automatically. Do not start `lark-cli event consume` or a second SDK connection.

## Combined workflows

- `standup-report` combines bounded calendar data and incomplete tasks.
- `meeting-summary` accepts exactly one meeting-ID set, Minutes token, or exact Minutes URL and combines available meeting, Note, Minutes, and optional bounded transcript data.

Both return `publish:false`. Publishing is a separate, explicitly authorized docbox action.
