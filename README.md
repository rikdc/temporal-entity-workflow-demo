# Temporal Rewards Program — Entity Workflow

A Go implementation of a customer loyalty rewards program built with the [Temporal](https://temporal.io) SDK, demonstrating the **Entity Workflow** pattern for long-running, stateful business processes.

## Architecture

![Architecture Diagram](./diagram.svg)

## Project Structure

```text
temporal-challenge/
├── cmd/
│   └── rewards/
│       └── main.go              # Single CLI entrypoint
├── internal/
│   ├── workflow/
│   │   ├── rewards.go           # Core entity workflow
│   │   └── rewards_test.go      # Unit tests for tier logic
│   ├── activities/
│   │   └── activities.go        # Activities (enroll, notify, unenroll)
│   └── worker/
│       └── worker.go            # Worker setup logic
├── go.mod
├── go.sum
├── README.md
├── ARCHITECTURE.md              # Detailed architecture documentation
├── TRADEOFFS.md                 # Implementation tradeoffs and decisions
└── diagram.svg
```

## Key Design Decisions

### Entity Workflow Pattern

Customer rewards memberships run for months or years. They accumulate state continuously and respond to events at any time. The entity workflow pattern handles this by creating one long-running workflow instance per customer.

- **One workflow per customer**: `WorkflowID = "rewards-{customerID}"`
- **Event-driven**: The workflow waits for signals instead of polling a database
- **Durable**: Temporal's event sourcing persists state across crashes and deployments

### Signals

Points are added via `add-points` signals. Temporal queues signals durably, so if a worker is down when a signal arrives, it gets delivered when the worker restarts.

The `unenroll` signal triggers a clean workflow exit after calling the unenrollment activity.

### Queries

The `get-status` query returns the customer's current tier and point balance. Queries read directly from in-memory workflow state without database lookups.

### Activities

The workflow calls three activities:

- **Enrollment**: When a customer joins
- **Tier Change Notifications**: When crossing thresholds (500 for Gold, 1000 for Platinum)
- **Unenrollment**: When a customer leaves

Activities retry with exponential backoff (1s initial, 2x multiplier, 5 max attempts).

### Continue-as-New

Workflows that run for years accumulate unbounded event history. To prevent this, the workflow calls `ContinueAsNew` every 200 events:

1. Carries forward essential state (customer ID, points, tier, enrollment status, processed keys)
2. Starts a fresh event history
3. Keeps the same WorkflowID

### Inactivity Timer

Points reset to zero after 365 days of inactivity. The workflow cancels and restarts the timer on each `add-points` signal. When points reset, the workflow sends a tier change notification if the customer drops tiers.

### Activity Retries

Activities handle side effects (email, database writes) with automatic retries. Transient failures don't cause the workflow to lose progress.

### Idempotency

Point events support optional deduplication keys:

```bash
./rewards add-points customer-42 purchase 300 txn-12345
```

The workflow tracks processed keys and skips duplicates. This prevents double-counting points when signals retry.

### Structured Logging

Logs include correlation IDs (workflowID, runID, activityID) for tracing:

```text
Points added customerID=customer-42 workflowID=rewards-customer-42 activity=purchase points=300 tier=gold
```

### Search Attributes

Workflows update search attributes when tiers or points change:

```sql
CustomStringField = "platinum"  # All platinum customers
CustomIntField >= 500           # Customers with 500+ points
```

## Running Locally

**Prerequisites:** [Temporal CLI](https://docs.temporal.io/cli) and Go 1.26+

### 1. Build the Application

```bash
go build -o rewards ./cmd/rewards
```

### 2. Start Temporal Server

```bash
temporal server start-dev
```

### 3. Start the Worker

In a new terminal:

```bash
./rewards worker
```

### 4. Interact with Customers

In another terminal:

```bash
# Enroll a customer
./rewards enroll customer-42

# Add points (this will trigger tier changes)
./rewards add-points customer-42 purchase 300
./rewards add-points customer-42 referral 250

# Query status
./rewards status customer-42
# Output:
# {
#   "customer_id": "customer-42",
#   "tier": "gold",
#   "points": 550,
#   "event_count": 2
# }

# Add more points to reach platinum
./rewards add-points customer-42 shopping 500

./rewards status customer-42
# {
#   "customer_id": "customer-42",
#   "tier": "platinum",
#   "points": 1050,
#   "event_count": 3
# }

# Unenroll
./rewards unenroll customer-42
```

## Tier Thresholds

| Tier     | Points Required |
|----------|----------------|
| Basic    | 0              |
| Gold     | 500            |
| Platinum | 1000           |

## CLI Commands

```
Usage: rewards <command> [args]

Commands:
  worker                                    Start the Temporal worker
  enroll <customerID>                       Enroll a customer in the rewards program
  add-points <customerID> <activity> <pts>  Add points to a customer's account
  status <customerID>                       Query customer's current status
  unenroll <customerID>                     Unenroll a customer from the program
```

## Testing

### Run Unit Tests

```bash
go test ./... -v
```

**Test Coverage**:

- ✅ Tier calculation logic (17 test cases)
- ✅ Tier progression and demotion
- ✅ Edge cases (zero, negative, large values)
- ✅ Point event deduplication
- ✅ State initialization

**Test Output**:

```text
=== RUN   TestComputeTier
--- PASS: TestComputeTier (0.00s)
=== RUN   TestTierThresholds
--- PASS: TestTierThresholds (0.00s)
...
PASS
ok      github.com/rikdc/temporal-entity-workflow-demo/internal/workflow
```

See [TRADEOFFS.md](TRADEOFFS.md) for discussion on integration testing approach.

## Features

This implementation covers the core requirements plus production patterns:

1. ✅ **Entity Workflow Pattern** - One long-running workflow per customer
2. ✅ **Enrollment Activity** - Customers are enrolled via activity when workflow starts
3. ✅ **Point Accumulation** - Signals add points from various activities
4. ✅ **Tier Calculation** - Automatic tier promotion based on point thresholds
5. ✅ **Tier Change Notifications** - Activities notify customers when tiers change
6. ✅ **Query Support** - Real-time status queries without database calls
7. ✅ **Unenrollment** - Clean exit with unenrollment activity
8. ✅ **Inactivity Handling** - Points reset after 365 days of inactivity
9. ✅ **History Management** - Continue-as-new prevents unbounded history growth
10. ✅ **Resilience** - Activity retries with exponential backoff
11. ✅ **Idempotency** - Deduplication keys prevent double-processing of signals
12. ✅ **Structured Logging** - Correlation IDs for distributed tracing
13. ✅ **Search Attributes** - Query workflows by tier or point range
14. ✅ **Unit Tests** - Comprehensive test coverage for tier calculation logic

## Production Gaps

See [TRADEOFFS.md](TRADEOFFS.md) for detailed rationale.

**Stubbed for POC**:

- Database persistence (activities log to console)
- Notification service (activities log to console)
- Integration tests (unit tests only)
- Custom metrics
- Point history audit log
