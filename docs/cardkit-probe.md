# CardKit compatibility and formal delivery

This opt-in diagnostic has no startup worker and does not change task links or
upgrade existing cards. New explicit task connections opt into the CardKit path;
existing message bindings retain legacy delivery. `KSFASSISTANT_DISABLE_CARDKIT=1`
on Core disables new enrollment, not updates to already-bound entities.

The managed launcher supports:

```
ksfas-lark managed card-probe start --id <unique-test-id> --target-name <authorized-direct-alias> --mode native --as bot
ksfas-lark managed card-probe step --id <unique-test-id> --as bot
ksfas-lark managed card-probe progress --id <unique-test-id> --as bot
ksfas-lark managed card-probe status --id <unique-test-id> --as bot
ksfas-lark managed card-probe finish --id <unique-test-id> --as bot
```

`component` mode uses element PATCH instead of native streaming content PUT.
`formal` mode uses the actual governed native-task entity journal, inserting a
second message at sample 2 and removing the first at sample 6. It never connects
a real task, and its callbacks remain diagnostic-only.
Only fixed generated sample text can be sent. No arbitrary entity, path, payload,
sequence, or receiver ID is accepted. The existing alias authorization, bot
identity, message-send/edit policy, and governed message binding still apply.
Raw CardKit commands are refused by the managed launcher and business executor.

Run explicit steps while entering test-only Chinese text and moving the cursor.
Test both desktop and mobile; submit and stop must continue working. Status
returns the callback receipt (including submitted test text) for verification.
No callback reaches Codex. Stop is journaled without waiting for the writer;
the next step closes streaming. The diagnostic is manually driven, not a claim
that the production stop scheduler has been implemented.

Each step is serialized and persists its sequence before sending. Unknown
outcomes stop the probe: no automatic retry, recreation, or restart recovery.
CardKit creation has no UUID field in the official SDK contract; an uncertain
create therefore also stops. Test IDs cannot be reused. An explicit `finish`
can close a bound card after an unknown update, using a newer sequence and only
settings; it never replays the failed body. It is not marked finished before
the remote acknowledgment. Status retains sanitized CLI failure metadata.

CardKit waits at most five seconds for the existing local authorization lease
before dispatch. This is lock acquisition, not retrying a remote write. Explicit
pre-dispatch rejection restores the previous projection; other errors remain
unknown. CardKit uses shared authorization leases: card writers can overlap,
while authentication/identity mutation still requires an exclusive lease.

Named button form submissions without a separate `value` are accepted only when
nonempty valid form data exists. Operator authorization and persisted message
ownership checks remain downstream and unchanged.

Step results report local queue and dispatch-to-acknowledgment time (`apiMs`,
including managed preflight and any authorization-lease wait). These are synthetic
sample timings, not pure HTTP, Desktop snapshot latency or client paint latency.

References: official larksuite/oapi-sdk-go `service/cardkit/v1/resource.go` and
`model.go` (CardKit v1). No SDK transport or additional credentials are introduced.

## Local compatibility run, 2026-09-15 (incomplete gate)

- A repeated live run exposed `approval_authorization_busy` before dispatch.
  Regression tests reproduce contention and verify one remote execution after
  waiting; positively identified pre-send rejection no longer becomes unknown.
- After that correction, a native-mode mobile run acknowledged 20 operations
  without failure: dispatch-to-ack 826–1315 ms, median 984 ms. This includes
  local managed execution, not pure network or client rendering time.
- The user reported mobile interaction looked normal. Stop receipt to confirmed
  remote close took 1878 ms. Submit receipt was not independently captured in
  that run (the diagnostic retains only the latest control receipt).
- Desktop inspection verified accumulated body/footer updates preserved the
  draft `桌面验收甲乙插入丙丁` and its middle insertion. This used Unicode paste,
  not a real IME composition test. Turning native streaming off cleared the
  outstanding draft; do not claim uninterrupted draft preservation at close.
- A submit without an explicit callback did not yield a verified receipt,
  including after native streaming was disabled. Probe submit now matches the
  existing task card's explicit callback binding plus form-submit semantics.
  A later component-mode run confirmed receipt of the exact submitted test text
  `回调验收完成` after explicit callback binding was added.

### Historical gate: normal component updates failed

The desktop component-mode run reproduced input loss under normal element PATCH.
After the update loop ended, the input visibly held `单次正文更新保留草稿`
(10 characters). A single body-only update, sequence 21, returned success in
941 ms. The next UI observation showed the body growing and that same input
empty (0 characters). No input-region update or user action occurred between
those observations. This independently confirms the earlier loop observation;
component-level API addressing does not imply client-side form state isolation.

That run acknowledged 21 updates (900–1868 ms, median 1019 ms). These timings do
not override the failed interaction gate. All test cards were explicitly closed
and acknowledged. No production task was migrated or disconnected. Mobile
feedback from the native run does not count as a pass for this component variant.

### Revised user acceptance

The user explicitly accepted draft loss when streaming ends or the operation
region changes at a terminal transition, and requested formal integration.
Native streaming is selected; normal component PATCH is not an automatic
fallback. Ongoing native content updates must preserve the input region.

### Formal implementation

- Core subscribes to accepted per-thread full snapshots, retaining 3-second
  polling. Untrusted/stale pushes do not wake the observer.
- One latest-value mailbox/worker per link; 500 ms body cadence, phase changes
  bypass the cadence. Footer writes are limited to one per second.
- Per-message element IDs and a fixed `activity` footer; normal text updates do
  not include the form. Structural phase transitions use a whole-card update.
- Native entity ID, message binding, sequence and exact pending UUID persist in
  the Feishu subprocess's private store. Unknown creates never recreate cards.
- A pending write resumes with its original UUID/sequence. Final text is sent
  before streaming closes; successful remote acknowledgment precedes sync state.
- Stop controls Codex first, cancels the card worker's outstanding context, then
  converges the final card under the same per-card lock.
- `cardSync` stores snapshot-arrival, submission and acknowledgment timestamps;
  the native journal stores each submitted/acknowledged operation. Durations
  include managed preflight, not just HTTP, and do not measure client paint.

Formal-path device acceptance results are recorded below after execution; unit
tests alone do not establish interaction compatibility.

### Formal-path run, 2026-09-15

`formal-20260915-1041` sent through the governed production entity journal.
Desktop preserved the 10-character test draft across content PUT, insertion of
a second message, and removal of the original message. Mobile feedback was
“没有丢失”; the diagnostic persisted a real submit callback during the update
loop. A desktop stop callback then closed the card, with remote acknowledgment
before the probe became `finished`.

26 update projections succeeded: 859–3492 ms dispatch-to-ack, median 1017 ms.
Multiple changed components require sequential operations, explaining slower
multi-component samples. The closing projection took 2157 ms. These numbers
are not an end-to-end client paint measurement or a latency SLA. Real task
snapshot-to-send timings are available in its persisted `cardSync` state.

`formal-idle-20260915-1048` additionally verified creation with streaming off,
transition into streaming by the production replacement path (1371 ms), a
subsequent content-plus-insertion update (3159 ms), and confirmed close
(1848 ms). Both formal probes are finished; no live diagnostic loop remains.

Verification: Go full suite and vet passed, targeted race tests passed, Swift
106 tests and Windows 166 tests passed, Windows x64/ARM64 compiled, and the
macOS universal package passed signing/resource checks. One full run during
parallel packaging hit an existing one-second lifecycle test timeout; the
unchanged test passed three isolated repetitions and the subsequent full run.
The installed bridge binary matches the final packaged artifact. Existing task
cards were retained, not automatically upgraded or disconnected.

### Real-entry failure discovered after those probes

User testing of a newly connected real task demonstrated full-card replacement
and draft loss. The probes above bypassed `integration.NewRuntime`; they proved
the CardKit transport, not real connection enrollment. The event decorators
embedded only the base interfaces and dropped `NativeTaskCardsEnabled` and
`SubscribeThreadSnapshots`. Consequently real links had no native flag/entity
binding and continued using legacy patching plus polling. Earlier production
readiness claims were not supported by those probe results.

The correction explicitly forwards named optional capability interfaces across
both decorators, with compile-time checks on the service implementations and
decorators. Regression tests enter through `NewRuntime` and `CreateTaskLink`,
check both switch states and legacy-card preservation, and run the real observer
to verify wake-up before the polling interval plus subscription cleanup. Both
enrollment and subscription tests failed before the forwarding correction.
Real-device acceptance must use a newly connected task, not another probe card.

After installing the corrected build and manually reconnecting the real task,
the user requested a long response in the new card and confirmed that the
behavior was now normal. This is user-reported real-card acceptance of the
previously failing interaction, not a new instrumented client latency measurement.
The corrected build passed Go tests/vet, the entry/observer race tests, Swift
and Windows regressions, Windows cross-compiles, and macOS package validation.
