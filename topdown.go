package topdown

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
)

// TopDownRL is the RL-based rate limiter for the gRPC server.
type TopDownRL struct {
	maxTokens           int64
	tokens              int64
	refillRate          int64
	lastRefill          time.Time
	slo                 map[string]time.Duration
	goodputCounter      int64
	sloViolationCounter int64
	currentGoodput      int64
	// sloViolationHistory []int64
	latencyHistory      []time.Duration
	LastTailLatency95th time.Duration // Stores the 95th percentile latency
	mutex               sync.Mutex
	Debug               bool
}

// NewTopDownRL creates a new TopDownRL with the specified parameters.
func NewTopDownRL(maxTokens, refillRate int64, slo map[string]time.Duration, debug bool) *TopDownRL {
	rl := &TopDownRL{
		maxTokens:      maxTokens,
		tokens:         maxTokens,
		refillRate:     refillRate,
		lastRefill:     time.Now(),
		slo:            slo,
		goodputCounter: 0,
		latencyHistory: make([]time.Duration, 0),
		Debug:          debug,
	}

	rl.StartMetricsCollection()
	return rl
}

// Allow checks if a request is allowed to proceed based on the token bucket algorithm.
func (rl *TopDownRL) Allow(ctx context.Context) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)

	// Refill tokens based on the elapsed time and rate limit
	refillTokens := int(elapsed.Seconds()) * int(rl.refillRate)
	if refillTokens > 0 {
		rl.tokens = intMin(rl.tokens+int64(refillTokens), rl.maxTokens)
		rl.lastRefill = now
	}

	if rl.tokens > 0 {
		rl.tokens--
		return true
	}

	return false
}

// postProcess handles the logic after a request has been processed to update goodput, SLO violations, and latency.
func (rl *TopDownRL) postProcess(latency time.Duration, methodName string) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	if latency <= rl.slo[methodName] {
		atomic.AddInt64(&rl.goodputCounter, 1)
	} else {
		rl.sloViolationCounter++
	}

	rl.latencyHistory = append(rl.latencyHistory, latency)
}

// StartMetricsCollection starts a separate goroutine that saves metrics and calculates the 95th percentile tail latency every second.
func (rl *TopDownRL) StartMetricsCollection() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				rl.mutex.Lock()

				// Calculate the 95th percentile tail latency and save it
				rl.calculateTailLatency95th()

				rl.mutex.Unlock()

				// Save the metrics (goodput and latency) to history
				rl.saveMetrics()
			}
		}
	}()
}

// UnaryInterceptor is the unary gRPC interceptor function that enforces rate limiting.
func (rl *TopDownRL) UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// Extract the method name and start time
	methodName := getMethodName(ctx)
	startTime := extractStartTime(ctx)

	// Check if the request is allowed before handling it
	if !rl.Allow(ctx) {
		return nil, fmt.Errorf("rate limit exceeded")
	}

	// Proceed with the handler to get the response
	resp, err := handler(ctx, req)

	// Calculate the response latency and update metrics after handling the request
	latency := time.Since(startTime)
	rl.postProcess(latency, methodName)

	return resp, err
}
