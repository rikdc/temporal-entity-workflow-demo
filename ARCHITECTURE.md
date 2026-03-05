# Architecture Documentation

## System Overview

Customer loyalty rewards program built with Temporal's entity workflow pattern. Manages long-running membership states, accumulates points, calculates tiers, and handles enrollment/unenrollment.

## Entity Workflow Pattern

One long-running workflow instance per customer:

- Runs for months or years
- Accumulates state (points, tier, event count)
- Responds to events via signals
- Exposes state via queries (no database reads)
- Survives crashes via Temporal's event sourcing

### vs Traditional Approaches

Traditional systems need database polling, separate event processors, and cache invalidation. Entity workflows eliminate this:

- Workflows wait for signals instead of polling
- Event sourcing persists state
- Signals queue durably (no message loss)
- Single workflow definition (no distributed state)

## System Components

### 1. CLI Entry Point ([cmd/rewards/main.go](cmd/rewards/main.go))

Single binary with multiple subcommands:

```text
rewards worker          # Start Temporal worker
rewards enroll <id>     # Enroll new customer
rewards add-points      # Add points via signal
rewards status <id>     # Query current state
rewards unenroll <id>   # Unenroll customer
```

Responsibilities: Parse CLI args, create Temporal client, route commands, format responses.

**Workflow ID**: `rewards-{customerID}`

### 2. Workflow Definition ([internal/workflow/rewards.go](internal/workflow/rewards.go))

The core entity workflow implementing the entire customer rewards lifecycle.

#### Workflow State

```go
type RewardsState struct {
    CustomerID string
    Points     int
    Tier       string  // basic, gold, platinum
    EventCount int
    Done       bool
    Enrolled   bool
}
```

#### Lifecycle Phases

**Phase 1: Initialization**

- Set up activity options with retry policy (exponential backoff, max 5 attempts)
- Call `Enroll` activity if `state.Enrolled == false`
- Register query handler for `get-status` queries

**Phase 2: Event Loop**

- Create signal channels for `add-points` and `unenroll`
- Create inactivity timer (365 days)
- Use `workflow.Selector` to wait for events

**Phase 3: Event Handling**

1. **Add Points Signal** ([rewards.go:113-142](internal/workflow/rewards.go#L113-L142))
   - Cancel inactivity timer
   - Add points to balance
   - Increment event count
   - Calculate new tier
   - If tier changed: execute `NotifyTierChange` activity
   - Re-arm inactivity timer

2. **Unenroll Signal** ([rewards.go:145-158](internal/workflow/rewards.go#L145-L158))
   - Cancel inactivity timer
   - Execute `RecordUnenrollment` activity
   - Set `state.Done = true`
   - Exit workflow

3. **Inactivity Timeout** ([rewards.go:161-180](internal/workflow/rewards.go#L161-L180))
   - Reset points to 0
   - Downgrade tier to basic
   - If tier changed: execute `NotifyTierChange` activity
   - Re-arm timer

**Phase 4: Continue-as-New**

- Every 200 events, workflow calls `Continue-as-New`
- Carries forward essential state (points, tier, enrollment status)
- Starts fresh event history
- Prevents unbounded history growth for long-running workflows

#### Tier Calculation

```
Points >= 1000  → Platinum
Points >= 500   → Gold
Points < 500    → Basic
```

Calculation is deterministic and happens synchronously in the workflow.

### 3. Activities ([internal/activities/activities.go](internal/activities/activities.go))

Activities are idempotent side effects executed outside the workflow.

```go
type Activities struct{}
```

**Enroll(customerID) → EnrollmentRecord**

- Creates enrollment record with timestamp
- Would persist to database in production
- Returns confirmation record

**NotifyTierChange(customerID, oldTier, newTier)**

- Sends notification when customer changes tiers
- Would integrate with email/push service in production
- Non-blocking (workflow continues even if notification fails)

**RecordUnenrollment(customerID, finalPoints)**

- Archives membership on exit
- Persists final state for analytics
- Called during graceful shutdown

All activities have:

- 10 second timeout
- Exponential backoff retry (2x multiplier, max 30s interval)
- Maximum 5 retry attempts

### 4. Worker Process ([internal/worker/worker.go](internal/worker/worker.go))

Temporal worker that polls for workflow and activity tasks.

```go
func Run(c client.Client) error {
    w := worker.New(c, TaskQueue, worker.Options{})

    w.RegisterWorkflow(workflow.RewardsWorkflow)

    a := &activities.Activities{}
    w.RegisterActivity(a.Enroll)
    w.RegisterActivity(a.NotifyTierChange)
    w.RegisterActivity(a.RecordUnenrollment)

    return w.Run(worker.InterruptCh())
}
```

**Task Queue**: `rewards-task-queue`

Worker responsibilities:

- Poll Temporal server for workflow tasks
- Poll Temporal server for activity tasks
- Execute workflow code deterministically
- Execute activity code with retries

## Data Flow

### Enrollment Flow

```
Client                Temporal              Worker               Activities
  |                      |                    |                      |
  |--ExecuteWorkflow---->|                    |                      |
  |   (RewardsWorkflow)  |                    |                      |
  |                      |---WorkflowTask---->|                      |
  |                      |                    |                      |
  |                      |                    |--Enroll(customerID)->|
  |                      |                    |                      |
  |                      |                    |<--EnrollmentRecord---|
  |                      |<--TaskCompleted----|                      |
  |<--WorkflowExecution--|                    |                      |
  |   (WorkflowID, RunID)|                    |                      |
```

### Add Points Flow

```
Client                Temporal              Worker               Activities
  |                      |                    |                      |
  |--SignalWorkflow----->|                    |                      |
  |   (add-points)       |                    |                      |
  |                      |---WorkflowTask---->|                      |
  |                      |   (signal buffered)|                      |
  |                      |                    | Process signal       |
  |                      |                    | Update state         |
  |                      |                    | Check tier change    |
  |                      |                    |                      |
  |                      |                    |--NotifyTierChange--->|
  |                      |                    |                      |
  |                      |<--TaskCompleted----|                      |
  |<--SignalAck----------|                    |                      |
```

### Query Status Flow

```
Client                Temporal              Worker
  |                      |                    |
  |--QueryWorkflow------>|                    |
  |   (get-status)       |                    |
  |                      |---QueryTask------->|
  |                      |                    | Read in-memory state
  |                      |<--CustomerStatus---|
  |<--QueryResult--------|                    |
```

**Important**: Queries don't modify state and are answered from in-memory workflow state. No database lookup required.

## Key Design Decisions

### 1. Signals for State Changes

Signals (`add-points`, `unenroll`) provide **durable, at-least-once delivery**:

- Temporal buffers signals if worker is down
- No message loss during deployments or crashes
- External caller gets immediate acknowledgment

Alternative (database writes) would require:

- Separate event processor polling database
- Race conditions between writers
- Complex error handling

### 2. Queries for State Reads

Queries (`get-status`) provide **real-time, consistent reads**:

- No eventual consistency issues
- No cache invalidation needed
- Read directly from workflow memory

Alternative (database reads) would require:

- Separate write model and read model
- Synchronization complexity
- Potential staleness

### 3. Continue-as-New for History Management

Every 200 events, workflow continues-as-new:

- Prevents unbounded event history (which would slow replays)
- Transparent to external callers (WorkflowID unchanged)
- Carries forward only essential state

Without continue-as-new:

- Event history grows indefinitely
- Workflow replay becomes slower over time
- Potential memory issues for very long-running workflows

### 4. Inactivity Timer (365 days)

Single timer resets points if no activity:

- No cron jobs or schedulers required
- Timer is cancelled and re-armed on each `add-points` signal
- Deterministic - replay produces same results

Alternative (scheduled job) would require:

- External scheduler infrastructure
- Last-activity timestamp in database
- Query all customers periodically

### 5. Activity Retries with Exponential Backoff

Activities handle non-deterministic operations (I/O, network):

- Automatic retries on transient failures
- Exponential backoff prevents cascading failures
- Workflow makes progress even if activities fail temporarily

Retry policy:

```go
InitialInterval:    1s
BackoffCoefficient: 2.0
MaximumInterval:    30s
MaximumAttempts:    5
```

## Failure Scenarios and Recovery

### Worker Crash During Point Addition

1. Signal arrives at Temporal server
2. Worker processes signal, adds points
3. Worker crashes before completing task
4. Temporal automatically retries workflow task
5. Workflow replays from history
6. Signal is reprocessed (points already added in history)
7. State restored correctly

**Key**: Workflow code is deterministic. Replay produces same result.

### Activity Failure (Notification Service Down)

1. Workflow executes `NotifyTierChange` activity
2. Activity fails (service unavailable)
3. Temporal retries with exponential backoff (1s, 2s, 4s, 8s, 16s)
4. After 5 attempts, activity returns error
5. Workflow logs warning but continues
6. Points and tier are updated correctly

**Key**: Workflow doesn't fail if non-critical activities fail.

### Database Unavailable During Enrollment

1. New workflow starts
2. `Enroll` activity fails (database down)
3. Temporal retries activity (up to 5 times)
4. If all retries fail, workflow returns error
5. Client receives failure
6. Workflow never starts (no partial state)

**Key**: Critical activities (enrollment) can fail workflow startup.

### Deployment During Active Workflow

1. Workflow is waiting for signals
2. Worker is shut down for deployment
3. Signal arrives at Temporal server
4. Signal is buffered by Temporal
5. New worker starts up
6. Workflow continues from last checkpoint
7. Buffered signal is delivered
8. Processing continues normally

**Key**: Temporal's durable execution ensures zero data loss during deployments.

## Production Considerations

### Currently Stubbed

1. **Database Persistence**: Activities print to console instead of persisting to PostgreSQL/DynamoDB
2. **Notification Service**: Tier change notifications are logged, not sent via email/push
3. **Authentication**: No auth on Temporal client (production would use mTLS or API keys)
4. **Idempotency Keys**: Signals could be processed multiple times in retry scenarios

### Recommended Enhancements

1. **Add Deduplication Key to PointEvent**

   ```go
   type PointEvent struct {
       DeduplicationKey string
       Activity         string
       Points           int
   }
   ```

   Track processed keys in workflow state to prevent double-counting.

2. **Add Search Attributes**
   Index tier and points as Temporal search attributes:

   ```go
   workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
       "Tier": state.Tier,
       "Points": state.Points,
   })
   ```

   Enables queries like "all platinum customers" in Temporal UI.

3. **Add Point History Ring Buffer**
   Keep last N transactions in workflow state for audit trail:

   ```go
   RecentEvents []PointEvent // max 100 entries
   ```

4. **Emit Metrics**
   Integrate with Datadog/Prometheus:

   ```go
   metrics.Gauge("rewards.customer.points", state.Points, tags)
   metrics.Incr("rewards.tier.changes", tags)
   ```

5. **Database Integration**
   Replace activity stubs with real persistence:

   ```go
   func (a *Activities) Enroll(ctx context.Context, customerID string) error {
       return a.db.Exec(
           "INSERT INTO enrollments (customer_id, enrolled_at, tier) VALUES (?, ?, ?)",
           customerID, time.Now(), "basic",
       )
   }
   ```

6. **Structured Logging with Correlation IDs**
   Add request tracing:

   ```go
   logger := workflow.GetLogger(ctx)
   correlationID := workflow.GetInfo(ctx).WorkflowExecution.ID
   logger.Info("Processing signal", "correlationID", correlationID)
   ```

## Technology Stack

- **Language**: Go 1.26
- **Orchestration**: Temporal SDK v1.28.1
- **Temporal Server**: Local dev server (via `temporal server start-dev`)
- **Task Queue**: `rewards-task-queue`
- **Workflow Type**: `RewardsWorkflow`
- **Activities**: `Enroll`, `NotifyTierChange`, `RecordUnenrollment`

## Testing Strategy

### Unit Testing Workflow Logic

- Mock Temporal SDK context
- Test tier calculation function
- Test state transitions

### Integration Testing

- Start local Temporal server
- Execute full workflows end-to-end
- Verify signals, queries, continue-as-new

### Activity Testing

- Mock external dependencies (database, email service)
- Test retry behavior
- Test idempotency

## Scalability Characteristics

- **Horizontal Scaling**: Add more workers to increase throughput
- **Per-Customer Isolation**: Each customer is independent workflow
- **No Shared State**: Workers are stateless (all state in Temporal)
- **Backpressure Handling**: Temporal rate limits task dispatch
- **History Size**: Bounded by continue-as-new (200 events)

### Estimated Capacity

With default Temporal configuration:

- **Workflows**: Millions of concurrent customers
- **Signals**: Thousands per second per workflow
- **Queries**: Unlimited (no database read)
- **Workers**: Scale horizontally based on CPU/memory

## Observability

### Temporal UI (<http://localhost:8233>)

- View workflow execution history
- See pending signals and timers
- Query workflow state
- Inspect activity retry attempts
- View stack traces for failed workflows

### Workflow Logs

```
logger.Info("Points added",
    "activity", event.Activity,
    "points", event.Points,
    "total", state.Points,
    "tier", state.Tier,
)
```

### Activity Logs

```
[activity] Enrolling customer customer-42
[activity] Notifying customer customer-42: tier changed from basic → gold
[activity] Recording unenrollment for customer-42 (final points: 1050)
```

## Summary

This architecture demonstrates a **production-ready pattern** for long-running stateful processes:

✅ **Durable execution** - survives crashes and deployments
✅ **Event-driven** - no polling or cron jobs
✅ **Consistent state** - event sourcing ensures correctness
✅ **Scalable** - horizontal worker scaling
✅ **Observable** - full execution history in Temporal UI
✅ **Maintainable** - business logic in single workflow definition

The entity workflow pattern is ideal for:

- Customer lifecycle management
- Order fulfillment tracking
- Subscription billing
- IoT device state management
- Multi-step approval workflows
- Any domain where entities have long-lived state that responds to events
