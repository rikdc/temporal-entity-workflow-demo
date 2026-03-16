# Change Log

## Drain buffered signals before Continue-as-New

**File:** `internal/workflow/rewards.go`

### Problem

When `ContinueAsNew` is called while signals are pending in a channel buffer, those signals are silently dropped. The selector loop processes one event per iteration — if multiple `add-points` signals arrive while the workflow is executing an activity (e.g. `NotifyTierChange`), they queue up in the buffer. If `EventCount` hits the `MaxHistoryEvents` threshold on that iteration, `ContinueAsNew` would fire immediately, losing all buffered signals.

### Changes

**Extracted `applyPointEvent` helper**

Point processing logic was extracted from the `AddReceive` selector callback into a standalone `applyPointEvent(ctx, state, event, workflowID)` function. This allows the same logic to be called from both the selector callback and the drain loop without duplication. Timer lifecycle (`cancelTimer` / re-arm) was intentionally left in the selector callback and not included in the helper.

**Added drain loop before `ContinueAsNew`**

Before calling `ContinueAsNew`, the workflow now drains all pending `add-points` signals using `ReceiveAsync`:

```go
for {
    var buffered PointEvent
    if !pointsCh.ReceiveAsync(&buffered) {
        break
    }
    applyPointEvent(ctx, &state, buffered, info.WorkflowExecution.ID)
}
```

**Guard against pending unenroll**

`ContinueAsNew` is skipped if an `unenroll` signal is pending (`unenrollCh.Len() == 0` guard). This prevents abandoning an in-flight unenroll by handing off to a new run that would re-enroll the customer. The pending unenroll is processed normally by the selector on the next iteration.

---

## Inactivity timer does not survive Continue-as-New

**File:** `internal/workflow/rewards.go`

### Problem

When `ContinueAsNew` is called, all pending timers from the previous run are cancelled. The new run unconditionally created a fresh 365-day inactivity timer, resetting the clock regardless of how much time had already elapsed. A customer inactive for 364 days who triggered `ContinueAsNew` would get another full year before their points expired.

### Changes

**Added `LastActivityAt` to `RewardsState`**

`time.Time` field carried forward through `ContinueAsNew`, recording when the last `add-points` signal was processed.

**Extracted `createInactivityTimer`**

Single authoritative function for timer creation used at all three sites (initial setup, re-arm on signal, re-arm after inactivity reset):

```go
// createInactivityTimer returns a timer for the remaining inactivity duration.
// If LastActivityAt is zero (first run), the full InactivityTimeout is used.
// If the timeout has already elapsed (e.g. after a long ContinueAsNew gap), the timer fires immediately.
func createInactivityTimer(ctx workflow.Context, timerCtx workflow.Context, state RewardsState) workflow.Future {
    if state.LastActivityAt.IsZero() {
        return workflow.NewTimer(timerCtx, InactivityTimeout)
    }
    elapsed := workflow.Now(ctx).Sub(state.LastActivityAt)
    remaining := max(InactivityTimeout-elapsed, 0)
    return workflow.NewTimer(timerCtx, remaining)
}
```

**`LastActivityAt` updated on every re-arm**

Set to `workflow.Now(ctx)` before calling `createInactivityTimer` at each re-arm site, so the computed remaining duration is always accurate. `workflow.Now` is used rather than `time.Now` to ensure deterministic replay.
