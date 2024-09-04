package topdown

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
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

// getOrCreateInterfaceMetrics initializes or retrieves metrics for an interface.
func (rl *TopDownRL) getOrCreateInterfaceMetrics(methodName string) *InterfaceMetrics {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	if _, exists := rl.interfaces[methodName]; !exists {
		rl.interfaces[methodName] = &InterfaceMetrics{
			MaxTokens:      10000, // Default values, adjust as necessary
			Tokens:         100,
			RefillRate:     1000, // Default refill rate
			LastRefill:     time.Now(),
			LatencyHistory: make([]time.Duration, 0),
		}
	}
	return rl.interfaces[methodName]
}

// Allow checks if a request is allowed for the given interface based on the token bucket algorithm.
func (rl *TopDownRL) Allow(methodName string) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	metrics := rl.getOrCreateInterfaceMetrics(methodName)
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
	metrics := rl.getOrCreateInterfaceMetrics(methodName)

	if latency <= rl.slo[methodName] {
		atomic.AddInt64(&metrics.GoodputCounter, 1)
	} else {
		metrics.SloViolationCounter++
	}

	metrics.LatencyHistory = append(metrics.LatencyHistory, latency)
}

// calculateTailLatency95th calculates the 95th percentile tail latency for a given interface.
func (rl *TopDownRL) calculateTailLatency95th(methodName string) time.Duration {
	metrics := rl.getOrCreateInterfaceMetrics(methodName)
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

// GetMetrics retrieves goodput and 95th percentile latency for a given interface.
func (rl *TopDownRL) GetMetrics(methodName string) (int64, time.Duration) {
	metrics := rl.getOrCreateInterfaceMetrics(methodName)
	goodput := atomic.SwapInt64(&metrics.GoodputCounter, 0)

	return goodput, metrics.LastTailLatency95th
}

// SetRateLimit sets the rate limit for a specific interface.
func (rl *TopDownRL) SetRateLimit(methodName string, rateLimit int64) {
	metrics := rl.getOrCreateInterfaceMetrics(methodName)
	metrics.MaxTokens = rateLimit
}
