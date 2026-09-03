# Docs, Wiki, comments, Whiteboard, Mindnotes, and Markdown

## Docs and knowledge

- Knowledge search needs a keyword and a result limit.
- Knowledge read needs a target. Default to outline; use keyword or section scope when possible. Use full only when explicitly requested.
- Existing Docx updates must inspect first and use official version protection.
- Append is the default. Overwrite and exact replacement are high-impact writes and require confirmation.
- Comment creation and replies use actionbox. Delete is unavailable.

## Identity by location

- Personal cloud Docs and Markdown: user identity.
- Wiki nodes, Wiki-contained documents, and bridge-managed Wiki Mindnotes: bot identity.
- A Wiki must explicitly grant the bot edit access. A `131006` resource error means the user must add the bot as an editable Wiki member; do not retry as user.
- Personal-cloud Mindnote writes are unavailable when the bridge cannot prove location.

## Registered content features

- Docs: history, rollback, media preview/download/upload/insert, resource read/update, draft preflight, narrow Whiteboard insertion.
- Wiki: space/node/member reads plus space creation, node creation, and node copy.
- Comments: list, replies, reply add/update, resolve/restore, reply reaction.
- Whiteboard: preview/SVG/source/raw export, append, and confirmed full overwrite.
- Mindnotes: node list, child creation, and confirmed node update. Independent Mindnote creation is not exposed; a Wiki Mindnote can be created as a Wiki node.
- Markdown: create, fetch, diff, confirmed overwrite, and confirmed patch with before/after verification.

Arbitrary block XML, permissions, member changes, Wiki moves, deletion, and Drive removal are unavailable.
