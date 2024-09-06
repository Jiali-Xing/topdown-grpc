package topdown

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/metadata"
)

// calculateTailLatency95th calculates the 95th percentile tail latency from the current latency history.
func (rl *TopDownRL) calculateTailLatency95th(methodName string) time.Duration {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	metrics := rl.interfaces[methodName]

	// do the same thing as in the original code but with the metrics
	if len(metrics.LatencyHistory) == 0 {
		return 0 // No data, return 0 or a default value
	}

	// Sort the latencies to find the 95th percentile
	sort.Slice(metrics.LatencyHistory, func(i, j int) bool {
		return metrics.LatencyHistory[i] < metrics.LatencyHistory[j]
	})

	index := int(float64(len(metrics.LatencyHistory)) * 0.95)
	if index >= len(metrics.LatencyHistory) {
		index = len(metrics.LatencyHistory) - 1
	}

	// Update the last 95th percentile latency and clear the history for the next second
	metrics.LastTailLatency95th = metrics.LatencyHistory[index]
	metrics.LatencyHistory = metrics.LatencyHistory[:0] // Clear the latency history

	return metrics.LastTailLatency95th
}

// getMethodName extracts the method name from the gRPC metadata.
func getMethodName(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if methodNames, exists := md["method"]; exists && len(methodNames) > 0 {
		return methodNames[0]
	}
	log.Panicf("Method name not found in metadata: %v", md)
	return ""
}

// saveMetrics saves the current goodput and latency before resetting the counters.
func (rl *TopDownRL) saveMetrics(methodName string) {
	metrics := rl.interfaces[methodName]

	metrics.CurrentGoodput = atomic.SwapInt64(&metrics.GoodputCounter, 0)
	if rl.Debug {
		fmt.Printf("[DEBUG] Goodput for this interval: %d\n", metrics.CurrentGoodput)
	}
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
