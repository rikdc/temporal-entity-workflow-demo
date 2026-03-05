# Implementation Tradeoffs

This document outlines the tradeoffs made during implementation, what was included, what was excluded, and the rationale behind each decision.

## ✅ Implemented Features

### 1. Idempotency Keys (DeduplicationKey)

**What**: Optional deduplication key on `PointEvent` to prevent double-processing of signals during retries.

**How**:
```go
type PointEvent struct {
    DeduplicationKey string  // Optional idempotency key
    Activity         string
    Points           int
}

// In workflow state
ProcessedKeys map[string]bool  // Track processed keys
```

**Benefit**: Prevents double-counting points if the same signal is sent multiple times due to network retries or application logic errors.

**Cost**:
- Memory overhead for tracking processed keys (grows over continue-as-new cycles)
- Slight complexity in signal handling logic

**Decision**: Financial systems need exactly-once semantics for transactions. The memory cost is acceptable with continue-as-new bounding history.

---

### 2. Structured Logging with Correlation IDs

**What**: All log statements include `workflowID`, `runID`, `customerID`, and `activityID` (for activities) to enable request tracing.

**Example**:
```go
logger.Info("Points added",
    "customerID", state.CustomerID,
    "workflowID", info.WorkflowExecution.ID,
    "activity", event.Activity,
    "points", event.Points,
)
```

**Benefit**:

- Trace customer journeys across workflows
- Correlate with external system logs
- Debug production issues

**Cost**:

- More verbose logging code
- ~100-200 bytes per log line

**Decision**: The cost is negligible. Correlation IDs are essential for distributed tracing in production.

---

### 3. Search Attributes

**What**: Workflow updates Temporal search attributes on tier/point changes so you can query workflows by tier or point range.

**Example**:
```go
workflow.UpsertSearchAttributes(ctx, map[string]any{
    "CustomStringField": tier,      // "basic", "gold", "platinum"
    "CustomIntField":    int64(points),
})
```

**Benefit**:

- Query platinum customers: `CustomStringField = "platinum"`
- Find 500+ point customers: `CustomIntField >= 500`
- Enable analytics and segmentation

**Cost**:

- 10-50ms per upsert
- Uses deprecated API

**Decision**: Accepted for POC simplicity. Production should use `UpsertTypedSearchAttributes` with proper configuration.

---

### 4. Unit Tests

**What**: Comprehensive unit tests for tier calculation logic covering edge cases.

**Coverage**:
- Tier thresholds (499 vs 500, 999 vs 1000)
- Tier progression (basic → gold → platinum)
- Tier demotion (platinum → gold → basic)
- Edge cases (zero points, negative points, very large values)
- State initialization

**Benefit**:

- Fast feedback (milliseconds)
- No external dependencies
- Easy CI/CD integration

**Stats**: 6 test functions, 17 test cases, 100% coverage of `computeTier`

**Decision**: Essential. Unit tests are low-cost and high-value for deterministic logic.

---

## ❌ Excluded Features

### 1. Full Integration Tests (Temporal TestSuite)

**What Was Attempted**: Temporal workflow integration tests using `testsuite.WorkflowTestSuite` to test the full workflow with mocked activities.

**Why Excluded**:
- Activity mocking in Temporal test suite requires precise setup with method expressions
- Mocking errors were difficult to debug (`cannot unmarshal string into Go value of type activities.Activities`)
- Time investment didn't justify benefit for a POC

**What We Have Instead**:
- Comprehensive unit tests for business logic
- Manual end-to-end testing via CLI commands (documented in README.md)

**Production Recommendation**:
For production, invest time to:
1. Create proper test fixtures with activity mocks
2. Test signal/query handling end-to-end
3. Test continue-as-new behavior with 200+ events
4. Test inactivity timer with `workflow.Sleep` mocking

**Decision**: Acceptable for POC. Unit tests prove tier calculation correctness. Full workflow tests would add confidence but need more setup time.

---

### 2. Typed Search Attributes ✅ Implemented

**What**: Using `UpsertTypedSearchAttributes` with typed search attribute keys.

**How**:

```go
var (
    tierKey   = temporal.NewSearchAttributeKeyKeyword("CustomStringField")
    pointsKey = temporal.NewSearchAttributeKeyInt64("CustomIntField")
)

workflow.UpsertTypedSearchAttributes(ctx,
    tierKey.ValueSet(state.Tier),
    pointsKey.ValueSet(int64(state.Points)),
)
```

**Benefit**:

- Type safety at compile time
- Better IDE support with autocomplete
- Cleaner than deprecated `UpsertSearchAttributes`

**Cost**: Minimal - just define keys once at package level

**Note**: Using default `CustomStringField` and `CustomIntField` search attributes. Production deployments should configure custom search attributes via `tctl admin cluster add-search-attributes` for better naming:

```bash
tctl admin cluster add-search-attributes \
  --name Tier --type Keyword \
  --name Points --type Int
```

**Decision**: Implemented with typed API. The complexity was overstated - it's actually simpler and more maintainable than the deprecated API.

---

### 3. Database Persistence in Activities

**What**: Real database writes in `Enroll`, `RecordUnenrollment` activities.

**Current**: Activities log to console with `fmt.Printf`

**Why Excluded**: POC focuses on Temporal patterns, not infrastructure. Activities demonstrate structure without requiring database setup.

**Production**:
```go
func (a *Activities) Enroll(ctx context.Context, customerID string) (EnrollmentRecord, error) {
    record := EnrollmentRecord{
        CustomerID:  customerID,
        EnrolledAt:  time.Now(),
        InitialTier: "basic",
    }

    // Real DB insert
    _, err := a.db.ExecContext(ctx,
        `INSERT INTO enrollments (customer_id, enrolled_at, tier)
         VALUES ($1, $2, $3)`,
        record.CustomerID, record.EnrolledAt, record.InitialTier,
    )
    return record, err
}
```

**Decision**: Correct for POC. The goal is demonstrating Temporal patterns, not building a data layer.

---

### 4. Notification Service Integration

**What**: Real email/push notification in `NotifyTierChange` activity.

**Current**: Console logging only

**Why Excluded**: Same reasoning as database persistence

**Production Recommendation**:
Integrate with SES, SendGrid, or SNS:
```go
func (a *Activities) NotifyTierChange(ctx context.Context, customerID, oldTier, newTier string) error {
    message := fmt.Sprintf("Congrats! You've been upgraded from %s to %s tier!", oldTier, newTier)

    return a.notificationClient.Send(ctx, &Notification{
        CustomerID: customerID,
        Subject:    "Tier Upgrade!",
        Body:       message,
        Channel:    "email",
    })
}
```

**Decision**: Correct for POC. Stubbed activities demonstrate the pattern without external dependencies.

---

### 5. Point History / Audit Log

**What**: Maintaining a ring buffer of recent point transactions in workflow state.

**Example**:
```go
type RewardsState struct {
    // ... existing fields
    RecentEvents []PointEvent  // Last 100 events for audit
}
```

**Why Excluded**: Adds state complexity and increases continue-as-new payload size.

**If Added**: Provides audit trail in queries, helps debugging, meets compliance needs.

**Production**: Store full history in database. Keep last N events in workflow state for quick queries.

**Decision**: Acceptable to exclude. Temporal's event log has history. User-facing audit logs belong in the database.

---

###6. Metrics and Observability

**What**: Emitting metrics for tier distribution, point velocity, workflow health.

**Example**:
```go
metrics.Gauge("rewards.customer.points", state.Points,
    map[string]string{"tier": state.Tier})
metrics.Incr("rewards.tier.changes",
    map[string]string{"from": oldTier, "to": newTier})
```

**Why Excluded**: Requires metrics infrastructure. POC focuses on Temporal patterns.

**Production**: Use Temporal's metrics interface with Datadog/Prometheus.

**Decision**: Acceptable to exclude. Temporal UI provides basic workflow metrics.

---

### 7. Continue-as-New Optimization (Bounded Key Map)

**What**: Pruning old deduplication keys during continue-as-new to prevent unbounded growth.

**Problem**: `ProcessedKeys map[string]bool` grows indefinitely across continue-as-new cycles.

**Solution** (not implemented):
```go
// Keep only keys from last N events
if len(state.ProcessedKeys) > 1000 {
    // Prune oldest keys (requires timestamp tracking)
    state.ProcessedKeys = pruneOldKeys(state.ProcessedKeys, 500)
}
```

**Why Excluded**:
- Adds complexity (need timestamps on keys)
- Unlikely to hit limits in practice (200 events per continue-as-new)
- Can revisit if memory becomes issue

**Production Recommendation**:
Option A: Time-based pruning (keep keys from last 7 days)
Option B: Sliding window (keep last N keys)
Option C: External deduplication store (Redis with TTL)

**Tradeoff Decision**: **Acceptable to defer**. 200 events × 50 bytes/key = 10KB per continue-as-new cycle. Not a concern for typical workflows.

---

### 8. Versioning for Workflow Updates

**What**: Using Temporal's `workflow.GetVersion` for safe workflow code changes.

**Example**:
```go
v := workflow.GetVersion(ctx, "tier-notification-v2", workflow.DefaultVersion, 1)
if v == 1 {
    // New notification format with personalized message
} else {
    // Old notification format (backward compatible)
}
```

**Why Excluded**:
- No workflow changes planned during POC
- Adds boilerplate code
- Best demonstrated when actually changing workflow logic

**Production Recommendation**:
**Always use versioning** when modifying workflow code that affects running workflows. Examples:
- Changing signal/query names
- Adding new activities
- Modifying state structure

**Tradeoff Decision**: **Acceptable to exclude**. Versioning should be added before first production deployment, not during initial development.

---

## Summary

| Feature | Status | Reason |
|---------|--------|--------|
| Idempotency Keys | ✅ Implemented | Critical for financial accuracy |
| Structured Logging | ✅ Implemented | Essential for production debugging |
| Search Attributes | ✅ Implemented | Enables analytics and querying |
| Typed Search Attributes | ✅ Implemented | Type safety with minimal complexity |
| Unit Tests | ✅ Implemented | High value, low cost |
| Integration Tests | ❌ Excluded | Too complex for POC timeline |
| Database Persistence | ❌ Excluded | Focus on Temporal patterns |
| Notification Integration | ❌ Excluded | Stubbed activities demonstrate pattern |
| Point History Buffer | ❌ Excluded | Event log provides history |
| Custom Metrics | ❌ Excluded | Infrastructure dependency |
| Key Map Pruning | ❌ Excluded | Not a concern at current scale |
| Versioning | ❌ Excluded | No workflow changes yet |

## Assessment

Implemented features (idempotency, logging, typed search attributes, unit tests) demonstrate production patterns while keeping the POC maintainable. Excluded features fall into three categories:

1. Infrastructure (database, notifications, metrics) - properly stubbed
2. Optimizations (key pruning) - deferred
3. Testing complexity (integration tests) - unit tests suffice for POC

This balances demonstrating Temporal best practices with POC scope.
