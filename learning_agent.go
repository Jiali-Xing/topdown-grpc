package topdown

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// GetMetrics retrieves goodput and 95th percentile latency for a given interface.
func (rl *TopDownRL) GetMetrics(methodName string) (int64, time.Duration) {

	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	// Add debug log
	if rl.Debug {
		log.Printf("[DEBUG] Fetching metrics for method: %s\n", methodName)
	}

	metrics := rl.getInterfaceMetrics(methodName)
	goodput := atomic.SwapInt64(&metrics.GoodputCounter, 0)

	if rl.Debug {
		// Add debug log
		log.Printf("[DEBUG] Goodput for method %s: %d, Tail Latency: %s\n", methodName, goodput, metrics.LastTailLatency95th)
	}

	return goodput, metrics.LastTailLatency95th
}

// SetRateLimit sets the rate limit for a specific interface.
func (rl *TopDownRL) SetRateLimit(methodName string, rateLimit int64) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	if rl.Debug {
		// Add debug log
		log.Printf("[DEBUG] Setting new rate limit for method: %s, rateLimit: %f\n", methodName, rateLimit)
	}
	metrics := rl.getInterfaceMetrics(methodName)
	metrics.RefillRate = rateLimit

	if rl.Debug {
		// Add debug log
		log.Printf("[DEBUG] New rate limit for method: %s, rateLimit: %f\n", methodName, metrics.RefillRate)
	}
}

// StartServer starts the HTTP server that handles GET and SET requests for metrics and rate limits.
func (rl *TopDownRL) StartServer(portn int) error {
	http.HandleFunc("/metrics", rl.HandleGetMetrics)    // Handles GET requests to fetch metrics per interface
	http.HandleFunc("/set_rate", rl.HandleSetRateLimit) // Handles POST requests to set the rate limit per interface

	portStr := fmt.Sprintf(":%d", portn)
	log.Println("Starting Topdown RL agent server on", portStr)
	if err := http.ListenAndServe(portStr, nil); err != nil {
		log.Fatalf("Could not start server: %s\n", err)
		return err
	}
	return nil
}

// handleSetRateLimit handles the POST requests to update the rate limit for a specific interface.
func (rl *TopDownRL) HandleSetRateLimit(w http.ResponseWriter, r *http.Request) {
	if rl.Debug {
		log.Println("[DEBUG] HandleSetRateLimit called")
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	methodName := r.URL.Query().Get("method")
	if methodName == "" {
		http.Error(w, "Method not specified", http.StatusBadRequest)
		return
	}

	var data struct {
		RateLimit int64 `json:"rate_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	rl.SetRateLimit(methodName, data.RateLimit)
	w.WriteHeader(http.StatusOK)
}

// handleGetMetrics handles the GET requests to return goodput and latency for a specific interface.
func (rl *TopDownRL) HandleGetMetrics(w http.ResponseWriter, r *http.Request) {
	if rl.Debug {
		log.Println("[DEBUG] HandleGetMetrics called")
	}

	if r.Method != http.MethodGet {
		log.Println("[ERROR] Invalid request method for /metrics:", r.Method)
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	methodName := r.URL.Query().Get("method")
	if methodName == "" {
		log.Println("[ERROR] No method provided in /metrics request")
		http.Error(w, "Method not specified", http.StatusBadRequest)
		return
	}

	goodput, latency := rl.GetMetrics(methodName)

	if rl.Debug {
		log.Printf("[DEBUG] Returning metrics for method=%s: Goodput=%f, Latency=%f\n", methodName, goodput, latency)
	}

	response := struct {
		Goodput int64         `json:"goodput"`
		Latency time.Duration `json:"latency"`
	}{
		Goodput: goodput,
		Latency: latency,
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Println("[ERROR] Failed to encode response:", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
	if rl.Debug {
		log.Println("[DEBUG] Successfully returned metrics for method", methodName)
	}
}
