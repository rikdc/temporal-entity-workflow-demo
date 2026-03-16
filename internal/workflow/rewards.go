package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Tier levels
const (
	TierBasic    = "basic"
	TierGold     = "gold"
	TierPlatinum = "platinum"

	GoldThreshold     = 500
	PlatinumThreshold = 1000

	// Continue-as-new after this many signal events to keep history bounded
	MaxHistoryEvents = 200

	// Points expire if no activity within this duration
	InactivityTimeout = 365 * 24 * time.Hour
)

// Workflow version constants
const (
	WorkflowVersion_Baseline = 1

	ChangeID_Baseline = "baseline"
)

// Search attribute keys for queryability in Temporal UI
var (
	tierKey   = temporal.NewSearchAttributeKeyKeyword("CustomStringField")
	pointsKey = temporal.NewSearchAttributeKeyInt64("CustomIntField")
)

// Signal and query names
const (
	SignalAddPoints = "add-points"
	SignalUnenroll  = "unenroll"
	QueryGetStatus  = "get-status"
)

// PointEvent represents an incoming points signal
type PointEvent struct {
	DeduplicationKey string `json:"deduplication_key,omitempty"` // Optional idempotency key
	Activity         string `json:"activity"`
	Points           int    `json:"points"`
}

// CustomerStatus is returned by the query handler
type CustomerStatus struct {
	CustomerID string `json:"customer_id"`
	Tier       string `json:"tier"`
	Points     int    `json:"points"`
	EventCount int    `json:"event_count"`
}

// RewardsState holds all mutable workflow state
type RewardsState struct {
	CustomerID      string
	Points          int
	Tier            string
	EventCount      int
	Done            bool
	Enrolled        bool            // Track if enrollment activity has been called
	ProcessedKeys   map[string]bool // Track processed deduplication keys for idempotency
	WorkflowVersion int             `json:"workflow_version,omitempty"` // Track which version created this state
	LastActivityAt  time.Time       `json:"last_activity_at,omitempty"`
}

// RewardsWorkflow is the long-running entity workflow for a customer's rewards membership.
// It starts on enrollment, accumulates points via signals, exposes its state via queries,
// and uses Continue-as-new to keep history bounded.
//
// Search Attributes:
// - CustomStringField: Current tier (basic/gold/platinum) for filtering workflows by tier
// - CustomIntField: Current points balance for range queries
func RewardsWorkflow(ctx workflow.Context, state RewardsState) error {
	// Get workflow execution info for correlation
	info := workflow.GetInfo(ctx)

	// Create structured logger with correlation IDs
	logger := workflow.GetLogger(ctx)

	// Log with correlation context
	logger.Info("RewardsWorkflow started",
		"customerID", state.CustomerID,
		"eventCount", state.EventCount,
		"workflowVersion", state.WorkflowVersion,
		"workflowID", info.WorkflowExecution.ID,
		"runID", info.WorkflowExecution.RunID,
		"attempt", info.Attempt,
	)

	// Baseline version marker for safe workflow evolution
	_ = workflow.GetVersion(ctx, ChangeID_Baseline, workflow.DefaultVersion, WorkflowVersion_Baseline)

	// Initialize workflow version if not set
	if state.WorkflowVersion == 0 {
		state.WorkflowVersion = WorkflowVersion_Baseline
	}

	// Initialize ProcessedKeys map if nil (for backwards compatibility with continue-as-new)
	if state.ProcessedKeys == nil {
		state.ProcessedKeys = make(map[string]bool)
	}

	// Activity options with retry policy
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Call enrollment activity if this is a new customer
	if !state.Enrolled {
		var enrollmentRecord EnrollmentRecord
		err := workflow.ExecuteActivity(ctx, "Enroll", state.CustomerID).Get(ctx, &enrollmentRecord)
		if err != nil {
			logger.Error("Failed to enroll customer", "error", err)
			return err
		}
		state.Enrolled = true

		// Set initial search attributes
		err = workflow.UpsertTypedSearchAttributes(ctx,
			tierKey.ValueSet(state.Tier),
			pointsKey.ValueSet(int64(state.Points)),
		)
		if err != nil {
			logger.Warn("Failed to set initial search attributes", "error", err)
		}

		logger.Info("Customer enrolled successfully",
			"customerID", state.CustomerID,
			"workflowID", info.WorkflowExecution.ID,
		)
	}

	// --- Query Handler ---
	if err := workflow.SetQueryHandler(ctx, QueryGetStatus, func() (CustomerStatus, error) {
		return CustomerStatus{
			CustomerID: state.CustomerID,
			Tier:       state.Tier,
			Points:     state.Points,
			EventCount: state.EventCount,
		}, nil
	}); err != nil {
		return err
	}

	// --- Signal Channels ---
	pointsCh := workflow.GetSignalChannel(ctx, SignalAddPoints)
	unenrollCh := workflow.GetSignalChannel(ctx, SignalUnenroll)

	// --- Inactivity timer ---
	// If no points are earned within InactivityTimeout, points reset to 0.
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	inactivityTimer := createInactivityTimer(ctx, timerCtx, state)

	for {
		selector := workflow.NewSelector(ctx)

		// Handle incoming points
		selector.AddReceive(pointsCh, func(c workflow.ReceiveChannel, more bool) {
			var event PointEvent
			c.Receive(ctx, &event)

			applyPointEvent(ctx, &state, event, info.WorkflowExecution.ID)

			state.LastActivityAt = workflow.Now(ctx)

			// Re-arm the inactivity timer
			timerCtx, cancelTimer = workflow.WithCancel(ctx)
			inactivityTimer = createInactivityTimer(ctx, timerCtx, state)
		})

		// Handle unenroll signal
		selector.AddReceive(unenrollCh, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, nil)
			cancelTimer()

			// Call unenrollment activity
			err := workflow.ExecuteActivity(ctx, "RecordUnenrollment", state.CustomerID, state.Points).Get(ctx, nil)
			if err != nil {
				logger.Error("Failed to record unenrollment", "error", err)
				// Continue with workflow completion even if activity fails
			}

			state.Done = true
			logger.Info("Customer unenrolled",
				"customerID", state.CustomerID,
				"workflowID", info.WorkflowExecution.ID,
				"finalPoints", state.Points,
			)
		})

		// Handle inactivity timeout
		selector.AddFuture(inactivityTimer, func(f workflow.Future) {
			if err := f.Get(ctx, nil); err == nil {
				oldTier := state.Tier
				logger.Info("Inactivity timeout: resetting points",
					"customerID", state.CustomerID,
					"workflowID", info.WorkflowExecution.ID,
					"previousPoints", state.Points,
				)
				state.Points = 0
				state.Tier = TierBasic

				// Update search attributes after inactivity reset
				err := workflow.UpsertTypedSearchAttributes(ctx,
					tierKey.ValueSet(TierBasic),
					pointsKey.ValueSet(int64(0)),
				)
				if err != nil {
					logger.Warn("Failed to update search attributes after inactivity", "error", err)
				}

				// Notify if tier changed
				if oldTier != TierBasic {
					err := workflow.ExecuteActivity(ctx, "NotifyTierChange", state.CustomerID, oldTier, TierBasic).Get(ctx, nil)
					if err != nil {
						logger.Warn("Failed to notify tier change after inactivity", "error", err)
					}
				}

				// Re-arm timer
				state.LastActivityAt = workflow.Now(ctx)
				timerCtx, cancelTimer = workflow.WithCancel(ctx)
				inactivityTimer = createInactivityTimer(ctx, timerCtx, state)
			}
		})

		selector.Select(ctx)

		if state.EventCount > 0 && state.EventCount%MaxHistoryEvents == 0 && unenrollCh.Len() == 0 {
			// Drain buffered add-points signals before continuing as new.
			// Temporal drops buffered signals if ContinueAsNew is called while
			// signals are pending in the channel.
			for {
				var buffered PointEvent
				if !pointsCh.ReceiveAsync(&buffered) {
					break
				}
				applyPointEvent(ctx, &state, buffered, info.WorkflowExecution.ID)
			}

			logger.Info("Continuing as new to bound history",
				"customerID", state.CustomerID,
				"workflowID", info.WorkflowExecution.ID,
				"eventCount", state.EventCount,
				"workflowVersion", state.WorkflowVersion,
			)

			state.WorkflowVersion = WorkflowVersion_Baseline
			return workflow.NewContinueAsNewError(ctx, RewardsWorkflow, state)
		}

		if state.Done {
			logger.Info("RewardsWorkflow completed",
				"customerID", state.CustomerID,
				"workflowID", info.WorkflowExecution.ID,
			)
			return nil
		}
	}
}

// computeTier derives the tier from the current points balance.
func computeTier(points int) string {
	switch {
	case points >= PlatinumThreshold:
		return TierPlatinum
	case points >= GoldThreshold:
		return TierGold
	default:
		return TierBasic
	}
}

// createInactivityTimer returns a timer for the remaining inactivity duration.
// If LastActivityAt is zero (first run), the full InactivityTimeout is used.
// If the timeout has already elapsed (e.g. after a long ContinueAsNew gap), the timer fires immediately
func createInactivityTimer(ctx workflow.Context, timerCtx workflow.Context, state RewardsState) workflow.Future {
	if state.LastActivityAt.IsZero() {
		return workflow.NewTimer(timerCtx, InactivityTimeout)
	}

	elapsed := workflow.Now(ctx).Sub(state.LastActivityAt)
	remaining := max(InactivityTimeout-elapsed, 0)

	return workflow.NewTimer(timerCtx, remaining)
}

// applyPointEvent handles the signal indicating a point is applied.
func applyPointEvent(
	ctx workflow.Context,
	state *RewardsState,
	event PointEvent,
	workflowID string,
) {

	logger := workflow.GetLogger(ctx)

	// Check for duplicate using deduplication key
	if event.DeduplicationKey != "" {
		if state.ProcessedKeys[event.DeduplicationKey] {
			logger.Info("Skipping duplicate point event",
				"deduplicationKey", event.DeduplicationKey,
				"activity", event.Activity,
			)
			return
		}
	}

	oldTier := state.Tier
	state.Points += event.Points
	state.EventCount++
	newTier := computeTier(state.Points)

	// Update search attributes when tier changes or after every point addition
	err := workflow.UpsertTypedSearchAttributes(ctx,
		tierKey.ValueSet(newTier),
		pointsKey.ValueSet(int64(state.Points)),
	)
	if err != nil {
		logger.Warn("Failed to upsert search attributes", "error", err)
		// Don't fail the workflow on search attribute failure
	}

	state.Tier = newTier

	// Mark as processed
	if event.DeduplicationKey != "" {
		state.ProcessedKeys[event.DeduplicationKey] = true
	}

	logger.Info("Points added",
		"customerID", state.CustomerID,
		"workflowID", workflowID,
		"activity", event.Activity,
		"points", event.Points,
		"total", state.Points,
		"tier", state.Tier,
		"deduplicationKey", event.DeduplicationKey,
	)

	// If tier changed, send notification
	if oldTier != state.Tier {
		err := workflow.ExecuteActivity(ctx, "NotifyTierChange", state.CustomerID, oldTier, state.Tier).Get(ctx, nil)
		if err != nil {
			logger.Warn("Failed to notify tier change", "error", err)
			// Don't fail the workflow on notification failure
		}
	}
}

// EnrollmentRecord is persisted when a customer joins.
type EnrollmentRecord struct {
	CustomerID  string    `json:"customer_id"`
	EnrolledAt  time.Time `json:"enrolled_at"`
	InitialTier string    `json:"initial_tier"`
}
