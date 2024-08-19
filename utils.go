package topdown

import (
	"context"
	"fmt"
	"sort"
	"time"

	"google.golang.org/grpc/metadata"
)

// // getGoodput calculates the goodput based on the request and response.
func (rl *TopDownRL) getGoodput() int64 {
	if rl.Debug {
		fmt.Println("[DEBUG] getGoodput called, currentGoodput=", rl.currentGoodput)
	}
	return rl.currentGoodput
}

// func (rl *TopDownRL) calculateSloViolation() int64 {
// 	if len(rl.sloViolationHistory) < 2 {
// 		return 0 // Not enough data, return 0 or a default value
// 	}
// 	return rl.sloViolationHistory[len(rl.sloViolationHistory)-2]
// }

// getTailLatency95th returns the most recently calculated 95th percentile tail latency.
func (rl *TopDownRL) getTailLatency95th() time.Duration {
	if rl.Debug {
		fmt.Println("[DEBUG] getTailLatency95th called, LastTailLatency95th=", rl.LastTailLatency95th)
	}
	return rl.LastTailLatency95th
}

// calculateTailLatency95th calculates the 95th percentile tail latency from the current latency history.
func (rl *TopDownRL) calculateTailLatency95th() time.Duration {
	if len(rl.latencyHistory) == 0 {
		return 0 // No data, return 0 or a default value
	}

	// Sort the latencies to find the 95th percentile
	sort.Slice(rl.latencyHistory, func(i, j int) bool {
		return rl.latencyHistory[i] < rl.latencyHistory[j]
	})

	// Calculate the 95th percentile index
	index := int(float64(len(rl.latencyHistory)) * 0.95)
	if index >= len(rl.latencyHistory) {
		index = len(rl.latencyHistory) - 1
	}

	// Update the last 95th percentile latency and clear the history for the next second
	rl.LastTailLatency95th = rl.latencyHistory[index]
	rl.latencyHistory = rl.latencyHistory[:0] // Clear the latency history

	return rl.LastTailLatency95th
}

// getMethodName extracts the method name from the gRPC metadata.
func getMethodName(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if methodNames, exists := md["method"]; exists && len(methodNames) > 0 {
		return methodNames[0]
	}
	return ""
}

// saveMetrics saves the current goodput and latency before resetting the counters.
func (rl *TopDownRL) saveMetrics() {
	rl.currentGoodput = rl.goodputCounter
	// reset the goodput counter
	rl.goodputCounter = 0
}

// extractStartTime extracts the start time from the gRPC metadata.
func extractStartTime(ctx context.Context) time.Time {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return time.Now()
	}

	timestamp, exists := md["timestamp"]
	if !exists || len(timestamp) == 0 {
		return time.Now()
	}

	// Parse the timestamp string to time.Time
	startTime, err := time.Parse(time.RFC3339, timestamp[0])
	if err != nil {
		return time.Now()
	}

	return startTime
}

// CalculateResponseLatency calculates the response latency using the start time.
func CalculateResponseLatency(startTime time.Time) time.Duration {
	return time.Since(startTime)
}

// min is a helper function to get the minimum of two floats.
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func intMin(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
