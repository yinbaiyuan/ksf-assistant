# Local Token accounting

The production meter is `Core/internal/tokens`. `Service.mergeLocalTokens` and `TokenHistory` rescan local logs; project usage consumes the same normalized session deltas. Both desktop hosts consume the Core result. The retained Swift `LocalTokenUsageReader` helper uses the same synthetic contract, but does not own the production refresh path.

## Identity and data flow

1. Enumerate active and archived JSONL files. Select one copy per session filename identity, preferring the larger file and then the newer modification time. Modification time limits usage scans, but source filenames remain discoverable outside the window; event time determines the natural local day.
2. Read each selected file in stream order. The first valid `session_meta.id` identifies the session. Subsequent metadata and `token_usage_record.thread_id` identify event ownership. A request's `session_id` can be shared by multiple agents and is never a meter identity.
3. Prefer `token_usage_record.usage`: it is an exact request amount. Require a valid timestamp, nonnegative total, thread ID and response ID. Deduplicate by `thread_id + response_id` in memory. Do not use `thread_token_usage` as an amount and do not add the matching `token_count` snapshot. Real local logs contain request usage absent from the older cumulative snapshots.
4. Preserve legacy `token_count` deltas before the first valid own request record. From that record onward, request amounts own accounting for that session; snapshot events no longer contribute, including repeated snapshots and counter resets. This supports a session upgraded from an older log format without adding two representations of the same requests. Malformed or identity-only request records do not activate this mode.
5. Count each top-level thread and child agent independently. Matching values, shared session IDs, parent IDs and fork ancestry never merge consumption. Forks replay copied metadata and records with rewritten timestamps: skip events owned by another thread. An explicit fork starts in inherited scope, including counters before the first copied metadata. Older forks may never re-emit their own metadata: lazily read the source’s turn identities (`turn_context` or `task_started`) to recognize copied turns and new execution. Legacy inherited counter samples remain baseline-only, so the first new legacy sample subtracts the inherited baseline; a lower cumulative total starts a fresh segment.
6. Sum own events into natural-day buckets. Project attribution applies the nearest ancestor binding timeline to these same deltas. Exact request records can be counted after a binding without an earlier counter baseline. Legacy samples retain the existing conservative binding-baseline check.

Parse only ownership/request/turn identifiers, timestamps and token amounts. Do not persist raw events or request identities. Ordinary input is input minus cached input; cached input and output use their own amounts. Legacy cumulative reclassifications preserve signed component deltas. The final bucket must be nonnegative and reconcile with its total before composition/cost is available.

## Coverage and recovery

The request-era format is expected to continue emitting request records after its first valid own record; subsequent snapshots cannot be substituted without a request identity because that could double count or silently hide missing requests. No cross-session maximum, fuzzy numeric match or persisted conversation ledger is used.

Explicit foreign ownership excludes replay even when the source is missing. Resolving old forks with stale metadata additionally requires the source’s turn identities; if unavailable, inherited counters remain excluded until positive ownership evidence appears. Without the original file, copied events with rewritten timestamps cannot reconstruct their original daily usage. Legacy logs without fork or ownership markers remain independent cumulative streams; copied history is never guessed from coincidentally equal numbers. Old snapshot-only eras retain the precision limits of their source data.

Production Go history is recomputed from logs and held in memory for ten seconds. This correction needs no schema migration and does not modify Codex logs or old standalone Swift history caches. Deploying the new Core allows the next fresh scan to recompute available history. Logs that no longer exist cannot be recovered by this change.

## Verification

`Tests/Fixtures/token-accounting.json` is a shared synthetic contract. It covers independent/nested agents, coincident counters, child resets, natural midnight boundaries, fork replay with rewritten timestamps, missing fork sources, nested forks, inherited context, legacy prefixes preceding foreign metadata, live turns without renewed metadata, ancestors outside the daily scan window, legacy logs, partial JSONL tails, request usage missing from snapshots, request deduplication, request-only logs, malformed records and format upgrades. Go checks local daily/history and project accounting; Swift checks daily/history against the same values. Existing cases cover active/archive copies and component reclassification.

The previous family-maximum implementation fails the independent-agent and separate-midnight-baseline regressions. The subsequent metadata-only implementation fails all four legacy-fork regressions: it charges a copied opening balance and can then exclude a new child turn because copied parent metadata remains in effect. An independent sum of unique own request amounts from local parent, child and fork logs also validates the new reader without committing real logs, task identities or response IDs.

Run `go test ./internal/tokens` in `Core` and `swift test --filter LocalTokenUsageReaderTests` at the repository root, followed by the repository's Go, Swift and Windows regression/build checks. Installing or restarting the user's application is separate from building the corrected artifact.
