package activities

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
)

// Activities holds any dependencies (DB clients, email clients, etc.)
type Activities struct{}

// EnrollmentRecord is persisted when a customer joins.
type EnrollmentRecord struct {
	CustomerID  string    `json:"customer_id"`
	EnrolledAt  time.Time `json:"enrolled_at"`
	InitialTier string    `json:"initial_tier"`
}

// Enroll persists the customer enrollment to a datastore.
// In production this would write to a database.
func (a *Activities) Enroll(ctx context.Context, customerID string) (EnrollmentRecord, error) {
	// Get activity info for correlation
	info := activity.GetInfo(ctx)
	logger := activity.GetLogger(ctx)

	logger.Info("Enrolling customer",
		"customerID", customerID,
		"workflowID", info.WorkflowExecution.ID,
		"activityID", info.ActivityID,
	)

	// Stub: In production this would write to DB
	fmt.Printf("[activity] Enrolling customer %s\n", customerID)

	return EnrollmentRecord{
		CustomerID:  customerID,
		EnrolledAt:  time.Now(),
		InitialTier: "basic",
	}, nil
}

// NotifyTierChange sends the customer a notification when their tier changes.
func (a *Activities) NotifyTierChange(ctx context.Context, customerID, oldTier, newTier string) error {
	// Get activity info for correlation
	info := activity.GetInfo(ctx)
	logger := activity.GetLogger(ctx)

	logger.Info("Notifying tier change",
		"customerID", customerID,
		"workflowID", info.WorkflowExecution.ID,
		"activityID", info.ActivityID,
		"oldTier", oldTier,
		"newTier", newTier,
	)

	// Stub: In production this would send email/push notification
	fmt.Printf("[activity] Notifying customer %s: tier changed from %s → %s\n", customerID, oldTier, newTier)

	return nil
}

// RecordUnenrollment archives the membership record on exit.
func (a *Activities) RecordUnenrollment(ctx context.Context, customerID string, finalPoints int) error {
	// Get activity info for correlation
	info := activity.GetInfo(ctx)
	logger := activity.GetLogger(ctx)

	logger.Info("Recording unenrollment",
		"customerID", customerID,
		"workflowID", info.WorkflowExecution.ID,
		"activityID", info.ActivityID,
		"finalPoints", finalPoints,
	)

	// Stub: In production this would archive to DB
	fmt.Printf("[activity] Recording unenrollment for %s (final points: %d)\n", customerID, finalPoints)

	return nil
}
