package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/rikdc/temporal-entity-workflow-demo/internal/worker"
	"github.com/rikdc/temporal-entity-workflow-demo/internal/workflow"
)

const TaskQueue = "rewards-task-queue"

func workflowID(customerID string) string {
	return "rewards-" + customerID
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Create Temporal client
	c, err := client.Dial(client.Options{})
	if err != nil {
		log.Fatalf("failed to create Temporal client: %v", err)
	}
	defer c.Close()

	cmd := os.Args[1]

	switch cmd {
	case "worker":
		runWorker(c)
	case "enroll":
		enrollCustomer(c)
	case "add-points":
		addPoints(c)
	case "status":
		getStatus(c)
	case "unenroll":
		unenrollCustomer(c)
	default:
		fmt.Printf("Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: rewards <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  worker                                           Start the Temporal worker")
	fmt.Println("  enroll <customerID>                              Enroll a customer in the rewards program")
	fmt.Println("  add-points <customerID> <activity> <pts> [key]  Add points to a customer's account")
	fmt.Println("                                                   Optional [key] for idempotency")
	fmt.Println("  status <customerID>                              Query customer's current status")
	fmt.Println("  unenroll <customerID>                            Unenroll a customer from the program")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  rewards worker")
	fmt.Println("  rewards enroll customer-42")
	fmt.Println("  rewards add-points customer-42 purchase 300")
	fmt.Println("  rewards add-points customer-42 purchase 300 txn-12345  # with deduplication key")
	fmt.Println("  rewards status customer-42")
	fmt.Println("  rewards unenroll customer-42")
}

func runWorker(c client.Client) {
	if err := worker.Run(c); err != nil {
		log.Fatalf("worker failed: %v", err)
	}
}

func enrollCustomer(c client.Client) {
	if len(os.Args) < 3 {
		log.Fatal("Usage: rewards enroll <customerID>")
	}

	customerID := os.Args[2]
	ctx := context.Background()

	initialState := workflow.NewRewardsState(customerID, workflow.TierBasic)

	opts := client.StartWorkflowOptions{
		ID:        workflowID(customerID),
		TaskQueue: TaskQueue,
	}

	we, err := c.ExecuteWorkflow(ctx, opts, workflow.RewardsWorkflow, initialState)
	if err != nil {
		log.Fatalf("failed to start workflow: %v", err)
	}

	fmt.Printf("✓ Enrolled customer %s\n", customerID)
	fmt.Printf("  WorkflowID: %s\n", we.GetID())
	fmt.Printf("  RunID: %s\n", we.GetRunID())
}

func addPoints(c client.Client) {
	if len(os.Args) < 5 {
		log.Fatal("Usage: rewards add-points <customerID> <activity> <points> [deduplication-key]")
	}

	customerID := os.Args[2]
	activityName := os.Args[3]
	pts, err := strconv.Atoi(os.Args[4])
	if err != nil {
		log.Fatalf("invalid points value: %v", err)
	}

	// Optional deduplication key
	var dedupKey string
	if len(os.Args) >= 6 {
		dedupKey = os.Args[5]
	}

	ctx := context.Background()
	event := workflow.PointEvent{
		DeduplicationKey: dedupKey,
		Activity:         activityName,
		Points:           pts,
		SourceID:         "cli",
	}

	err = c.SignalWorkflow(ctx, workflowID(customerID), "", workflow.SignalAddPoints, event)
	if err != nil {
		log.Fatalf("failed to signal workflow: %v", err)
	}

	if dedupKey != "" {
		fmt.Printf("✓ Added %d points (%s) to customer %s [key: %s]\n", pts, activityName, customerID, dedupKey)
	} else {
		fmt.Printf("✓ Added %d points (%s) to customer %s\n", pts, activityName, customerID)
	}
}

func unenrollCustomer(c client.Client) {
	if len(os.Args) < 3 {
		log.Fatal("Usage: rewards unenroll <customerID>")
	}

	customerID := os.Args[2]
	ctx := context.Background()

	err := c.SignalWorkflow(ctx, workflowID(customerID), "", workflow.SignalUnenroll, nil)
	if err != nil {
		log.Fatalf("failed to signal unenroll: %v", err)
	}

	fmt.Printf("✓ Unenroll signal sent for customer %s\n", customerID)
}
func getStatus(c client.Client) {
	if len(os.Args) < 3 {
		log.Fatal("Usage: rewards status <customerID>")
	}

	customerID := os.Args[2]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := c.QueryWorkflow(ctx, workflowID(customerID), "", workflow.QueryGetStatus)
	if err != nil {
		if err.Error() == "context deadline exceeded" || ctx.Err() == context.DeadlineExceeded {
			log.Fatalf("query timeout - is the worker running? Start it with: ./rewards worker")
		}
		log.Fatalf("failed to query workflow: %v", err)
	}

	var status workflow.CustomerStatus
	if err := resp.Get(&status); err != nil {
		log.Fatalf("failed to decode query result: %v", err)
	}

	out, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(out))
}
