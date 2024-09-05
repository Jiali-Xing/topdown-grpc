package topdown

import (
	"context"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// InterfaceMetrics stores metrics for each interface (e.g., method).
type InterfaceMetrics struct {
	MaxTokens           int64
	Tokens              int64
	RefillRate          int64
	LastRefill          time.Time
	GoodputCounter      int64
	SloViolationCounter int64
	LatencyHistory      []time.Duration
	LastTailLatency95th time.Duration
}

// TopDownRL manages rate limiting and metrics for multiple interfaces.
type TopDownRL struct {
	interfaces map[string]*InterfaceMetrics // Track metrics per interface
	slo        map[string]time.Duration     // SLO per interface
	mutex      sync.Mutex
	Debug      bool
}

// NewTopDownRL creates a new TopDownRL with the specified SLOs, max tokens, refill rate, and debug flag.
func NewTopDownRL(slo map[string]time.Duration, maxTokens, refillRate int64, debug bool) *TopDownRL {
	rl := &TopDownRL{
		interfaces: make(map[string]*InterfaceMetrics),
		slo:        slo,
		Debug:      debug,
	}

	// Initialize each interface with the provided maxTokens and refillRate
	for methodName := range slo {
		rl.interfaces[methodName] = &InterfaceMetrics{
			MaxTokens:      maxTokens,
			Tokens:         maxTokens,
			RefillRate:     refillRate,
			LastRefill:     time.Now(),
			LatencyHistory: make([]time.Duration, 0),
		}
	}

	rl.StartMetricsCollection()
	return rl
}

// getInterfaceMetrics retrieves metrics for an existing interface.
// Returns nil if the interface does not exist.
func (rl *TopDownRL) getInterfaceMetrics(methodName string) *InterfaceMetrics {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	// Check if the metrics exist for the method
	if metrics, exists := rl.interfaces[methodName]; exists {
		return metrics
	}

	// If the metrics do not exist, return nil or handle the error accordingly
	if rl.Debug {
		log.Printf("[DEBUG] Metrics for method '%s' do not exist.\n", methodName)
	}
	return nil
}

// Allow checks if a request is allowed for the given interface based on the token bucket algorithm.
func (rl *TopDownRL) Allow(methodName string) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	metrics := rl.getInterfaceMetrics(methodName)
	now := time.Now()
	elapsed := now.Sub(metrics.LastRefill).Seconds()

	// Calculate the number of tokens to refill
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

// postProcess updates goodput, SLO violations, and latency for a given interface.
func (rl *TopDownRL) PostProcess(latency time.Duration, methodName string) {
	// lock the mutex to update the metrics
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	metrics := rl.getInterfaceMetrics(methodName)

	if latency <= rl.slo[methodName] {
		atomic.AddInt64(&metrics.GoodputCounter, 1)
	} else {
		metrics.SloViolationCounter++
	}

	metrics.LatencyHistory = append(metrics.LatencyHistory, latency)
}

// calculateTailLatency95th calculates the 95th percentile tail latency for a given interface.
func (rl *TopDownRL) calculateTailLatency95th(methodName string) time.Duration {
	metrics := rl.getInterfaceMetrics(methodName)
	if len(metrics.LatencyHistory) == 0 {
		return 0
	}

	// Sort latencies to find the 95th percentile
	sort.Slice(metrics.LatencyHistory, func(i, j int) bool {
		return metrics.LatencyHistory[i] < metrics.LatencyHistory[j]
	})

	index := int(float64(len(metrics.LatencyHistory)) * 0.95)
	if index >= len(metrics.LatencyHistory) {
		index = len(metrics.LatencyHistory) - 1
	}

	// Store the calculated 95th percentile latency
	metrics.LastTailLatency95th = metrics.LatencyHistory[index]
	metrics.LatencyHistory = metrics.LatencyHistory[:0] // Clear history for next second
	return metrics.LastTailLatency95th
}

// StartMetricsCollection calculates the 95th percentile tail latency for each interface every second.
func (rl *TopDownRL) StartMetricsCollection() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			rl.mutex.Lock()
			for methodName := range rl.interfaces {
				rl.calculateTailLatency95th(methodName)
			}
			rl.mutex.Unlock()
		}
	}()
}

// UnaryInterceptor is the unary gRPC interceptor function that enforces rate limiting.
func (rl *TopDownRL) UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// Extract the method name from the context

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "missing metadata")
	}

	methodName := md["method"][0]
	startTime := time.Now()

	// Check if the request is allowed to proceed
	if !rl.Allow(methodName) {
		// ResourceExhausted: use this status code if the rate limit is exceeded
		return nil, status.Error(codes.ResourceExhausted, "Rate limit exceeded, request denied")
	}

	// Proceed with the handler
	resp, err := handler(ctx, req)

	// Calculate the request latency and update metrics after the request is processed
	latency := time.Since(startTime)
	rl.PostProcess(latency, methodName)

	return resp, err
}
