package topdown

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type InterfaceMetrics struct {
	MaxTokens           int64
	Tokens              int64
	RefillRate          int64
	LastRefill          time.Time
	GoodputCounter      int64
	CurrentGoodput      int64
	SloViolationCounter int64
	LatencyHistory      []time.Duration
	LastTailLatency95th time.Duration
}

// TopDownRL is the RL-based rate limiter for the gRPC server.
type TopDownRL struct {
	slo        map[string]time.Duration
	interfaces map[string]*InterfaceMetrics
	mutex      sync.Mutex
	Debug      bool
	// maxTokens           int64
	// tokens              int64
	// refillRate          int64
	// lastRefill          time.Time
	// goodputCounter      int64
	// sloViolationCounter int64
	// currentGoodput      int64
	// // sloViolationHistory []int64
	// latencyHistory      []time.Duration
	// LastTailLatency95th time.Duration // Stores the 95th percentile latency
}

// NewTopDownRL creates a new TopDownRL with the specified parameters.
func NewTopDownRL(maxTokens, refillRate int64, slo map[string]time.Duration, debug bool) *TopDownRL {
	// rl := &TopDownRL{
	// 	maxTokens:      maxTokens,
	// 	tokens:         maxTokens,
	// 	refillRate:     refillRate,
	// 	lastRefill:     time.Now(),
	// 	slo:            slo,
	// 	goodputCounter: 0,
	// 	latencyHistory: make([]time.Duration, 0),
	// 	Debug:          debug,
	// }
	rl := &TopDownRL{
		slo:        slo,
		interfaces: make(map[string]*InterfaceMetrics),
		Debug:      debug,
	}

	// Initialize metrics for each API (method)
	for methodName := range slo {
		rl.interfaces[methodName] = &InterfaceMetrics{
			MaxTokens:           maxTokens,
			Tokens:              maxTokens,
			RefillRate:          refillRate,
			LastRefill:          time.Now(),
			LatencyHistory:      make([]time.Duration, 0),
			LastTailLatency95th: 0 * time.Millisecond,
			GoodputCounter:      0,
			SloViolationCounter: 0,
			CurrentGoodput:      0,
		}
	}

	rl.StartMetricsCollection()
	return rl
}

// Allow checks if a request is allowed to proceed based on the token bucket algorithm.
func (rl *TopDownRL) Allow(ctx context.Context, methodName string) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	metrics := rl.interfaces[methodName] // Get metrics for the API

	now := time.Now()
	elapsed := now.Sub(metrics.LastRefill).Seconds()

	// Calculate the number of tokens to refill (using integer arithmetic)
	refillTokens := int64(elapsed * float64(metrics.RefillRate))
	if refillTokens > 0 {
		metrics.Tokens = intMin(metrics.Tokens+refillTokens, metrics.MaxTokens)
		metrics.LastRefill = now
	}

	if metrics.Tokens > 0 {
		metrics.Tokens--
		return true
	}
	return false
}

// postProcess handles the logic after a request has been processed to update goodput, SLO violations, and latency.
func (rl *TopDownRL) postProcess(latency time.Duration, methodName string) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	metrics := rl.interfaces[methodName]

	// Update goodput and SLO violation counter
	if latency <= rl.slo[methodName] {
		atomic.AddInt64(&metrics.GoodputCounter, 1)
	} else {
		metrics.SloViolationCounter++
	}

	metrics.LatencyHistory = append(metrics.LatencyHistory, latency)
}

// StartMetricsCollection starts a separate goroutine that saves metrics and calculates the 95th percentile tail latency every second.
func (rl *TopDownRL) StartMetricsCollection() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:

				// Calculate the 95th percentile tail latency and save it
				// loop through all the methods in interface map and calculate the 95th percentile tail latency
				for methodName := range rl.interfaces {
					rl.calculateTailLatency95th(methodName)

					// Save the metrics (goodput and latency) to history
					rl.saveMetrics(methodName)
				}
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
	if !rl.Allow(ctx, methodName) {
		// ResourceExhausted: use this status code if the rate limit is exceeded
		return nil, status.Error(codes.ResourceExhausted, "Rate limit exceeded, request denied")
	}

	// Proceed with the handler to get the response
	resp, err := handler(ctx, req)

	// Calculate the response latency and update metrics after handling the request
	latency := time.Since(startTime)
	rl.postProcess(latency, methodName)

	return resp, err
}
