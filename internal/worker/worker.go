package worker

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/rikdc/temporal-entity-workflow-demo/internal/activities"
	"github.com/rikdc/temporal-entity-workflow-demo/internal/workflow"
)

const TaskQueue = "rewards-task-queue"

// Run starts the Temporal worker
func Run(c client.Client) error {
	w := worker.New(c, TaskQueue, worker.Options{})

	// Register workflow
	w.RegisterWorkflow(workflow.RewardsWorkflow)

	// Register activities
	a := &activities.Activities{}
	w.RegisterActivity(a.Enroll)
	w.RegisterActivity(a.NotifyTierChange)
	w.RegisterActivity(a.RecordUnenrollment)

	log.Println("Starting rewards worker on task queue:", TaskQueue)
	return w.Run(worker.InterruptCh())
}
