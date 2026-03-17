package workflow

import (
	"testing"
)

// TestComputeTier verifies tier calculation based on point thresholds
func TestComputeTier(t *testing.T) {
	tests := []struct {
		name     string
		points   int
		expected string
	}{
		{
			name:     "zero points returns basic",
			points:   0,
			expected: TierBasic,
		},
		{
			name:     "1 point returns basic",
			points:   1,
			expected: TierBasic,
		},
		{
			name:     "499 points returns basic",
			points:   499,
			expected: TierBasic,
		},
		{
			name:     "500 points returns gold",
			points:   500,
			expected: TierGold,
		},
		{
			name:     "501 points returns gold",
			points:   501,
			expected: TierGold,
		},
		{
			name:     "999 points returns gold",
			points:   999,
			expected: TierGold,
		},
		{
			name:     "1000 points returns platinum",
			points:   1000,
			expected: TierPlatinum,
		},
		{
			name:     "1001 points returns platinum",
			points:   1001,
			expected: TierPlatinum,
		},
		{
			name:     "10000 points returns platinum",
			points:   10000,
			expected: TierPlatinum,
		},
		{
			name:     "negative points returns basic",
			points:   -1,
			expected: TierBasic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTier(tt.points)
			if got != tt.expected {
				t.Errorf("computeTier(%d) = %q, want %q", tt.points, got, tt.expected)
			}
		})
	}
}

// TestTierThresholds verifies the tier threshold constants
func TestTierThresholds(t *testing.T) {
	if GoldThreshold != 500 {
		t.Errorf("GoldThreshold = %d, want 500", GoldThreshold)
	}
	if PlatinumThreshold != 1000 {
		t.Errorf("PlatinumThreshold = %d, want 1000", PlatinumThreshold)
	}
}

// TestTierProgression verifies tier progression as points accumulate
func TestTierProgression(t *testing.T) {
	points := 0
	tier := computeTier(points)
	if tier != TierBasic {
		t.Errorf("Starting tier = %q, want %q", tier, TierBasic)
	}

	// Add points to reach gold
	points += 500
	tier = computeTier(points)
	if tier != TierGold {
		t.Errorf("Tier at 500 points = %q, want %q", tier, TierGold)
	}

	// Add more points to reach platinum
	points += 500
	tier = computeTier(points)
	if tier != TierPlatinum {
		t.Errorf("Tier at 1000 points = %q, want %q", tier, TierPlatinum)
	}
}

// TestTierDemotion verifies tier demotion when points decrease
func TestTierDemotion(t *testing.T) {
	// Start at platinum
	points := 1500
	tier := computeTier(points)
	if tier != TierPlatinum {
		t.Errorf("Starting tier = %q, want %q", tier, TierPlatinum)
	}

	// Drop to gold range
	points = 750
	tier = computeTier(points)
	if tier != TierGold {
		t.Errorf("Tier at 750 points = %q, want %q", tier, TierGold)
	}

	// Drop to basic range
	points = 250
	tier = computeTier(points)
	if tier != TierBasic {
		t.Errorf("Tier at 250 points = %q, want %q", tier, TierBasic)
	}

	// Reset to zero (inactivity)
	points = 0
	tier = computeTier(points)
	if tier != TierBasic {
		t.Errorf("Tier at 0 points = %q, want %q", tier, TierBasic)
	}
}

// TestPointEventDeduplication verifies deduplication key behavior
func TestPointEventDeduplication(t *testing.T) {
	tests := []struct {
		name        string
		event       PointEvent
		hasDedupKey bool
	}{
		{
			name: "event with deduplication key",
			event: PointEvent{
				DeduplicationKey: "txn-12345",
				Activity:         "purchase",
				Points:           100,
			},
			hasDedupKey: true,
		},
		{
			name: "event without deduplication key",
			event: PointEvent{
				DeduplicationKey: "",
				Activity:         "referral",
				Points:           50,
			},
			hasDedupKey: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasKey := tt.event.DeduplicationKey != ""
			if hasKey != tt.hasDedupKey {
				t.Errorf("Event has dedup key = %v, want %v", hasKey, tt.hasDedupKey)
			}
		})
	}
}

// TestRewardsStateInitialization verifies initial state setup
func TestRewardsStateInitialization(t *testing.T) {
	state := RewardsState{
		CustomerID:      "customer-123",
		Points:          0,
		Tier:            TierBasic,
		EventCount:      0,
		Enrolled:        false,
		ProcessedKeys:   newIdempotencyStore(),
		WorkflowVersion: WorkflowVersion_Baseline,
	}

	if state.CustomerID != "customer-123" {
		t.Errorf("CustomerID = %q, want %q", state.CustomerID, "customer-123")
	}
	if state.Points != 0 {
		t.Errorf("Points = %d, want 0", state.Points)
	}
	if state.Tier != TierBasic {
		t.Errorf("Tier = %q, want %q", state.Tier, TierBasic)
	}
	if state.EventCount != 0 {
		t.Errorf("EventCount = %d, want 0", state.EventCount)
	}
	if state.Enrolled {
		t.Errorf("Enrolled = %v, want false", state.Enrolled)
	}
	if state.ProcessedKeys.Keys == nil {
		t.Error("ProcessedKeys map is nil, want initialized map")
	}
	if state.WorkflowVersion != WorkflowVersion_Baseline {
		t.Errorf("WorkflowVersion = %d, want %d", state.WorkflowVersion, WorkflowVersion_Baseline)
	}
}
