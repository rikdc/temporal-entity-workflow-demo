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
