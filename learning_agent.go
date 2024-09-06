package topdown

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
)

// StartServer starts the HTTP server that handles GET and SET requests for metrics and rate limits.
func (rl *TopDownRL) StartServer(portn int) error {
	http.HandleFunc("/metrics", rl.HandleGetMetrics)    // Handles GET requests to fetch metrics
	http.HandleFunc("/set_rate", rl.HandleSetRateLimit) // Handles POST requests to set the rate limit

	portStr := fmt.Sprintf(":%d", portn)
	log.Println("Starting Topdown RL agent server on", portStr)
	if err := http.ListenAndServe(portStr, nil); err != nil {
		log.Fatalf("Could not start server: %s\n", err)
		return err
	}
	return nil
}

// SetRateLimit sets the rate limit (token bucket refill rate) from an external source.
func (rl *TopDownRL) SetRateLimit(method string, rateLimit float64) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	// rl.refillRate = int64(rateLimit)

	if metrics, exists := rl.interfaces[method]; exists {
		metrics.RefillRate = int64(rateLimit)
		if rl.Debug {
			log.Printf("[DEBUG] Set new rate limit for method '%s': %f\n", method, rateLimit)
		}
	} else {
		log.Printf("[ERROR] Method '%s' not found when trying to set rate limit\n", method)
	}
}

// GetMetrics returns the current goodput and latency.
func (rl *TopDownRL) GetMetrics(method string) (float64, float64) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	if metrics, exists := rl.interfaces[method]; exists {
		return float64(atomic.LoadInt64(&metrics.CurrentGoodput)), float64(metrics.LastTailLatency95th)
	}

	log.Printf("[ERROR] Method '%s' not found when trying to get metrics\n", method)
	return 0, 0
}

// handleSetRateLimit handles the SET requests to update the rate limit.
func (rl *TopDownRL) HandleSetRateLimit(w http.ResponseWriter, r *http.Request) {
	if rl.Debug {
		log.Println("[DEBUG] HandleSetRateLimit called")
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Extract the method from query parameters
	method := r.URL.Query().Get("method")
	if method == "" {
		http.Error(w, "Missing 'method' parameter", http.StatusBadRequest)
		return
	}

	var data struct {
		RateLimit float64 `json:"rate_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	if rl.Debug {
		log.Printf("[DEBUG] Received new rate limit: %f\n", data.RateLimit)
	}

	rl.SetRateLimit(method, data.RateLimit)
	w.WriteHeader(http.StatusOK)
}

// handleGetMetrics handles the GET requests to return goodput and latency.
func (rl *TopDownRL) HandleGetMetrics(w http.ResponseWriter, r *http.Request) {
	if rl.Debug {
		log.Println("[DEBUG] HandleGetMetrics called")
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Extract the method from query parameters
	method := r.URL.Query().Get("method")
	if method == "" {
		http.Error(w, "Missing 'method' parameter", http.StatusBadRequest)
		return
	}

	goodput, latency := rl.GetMetrics(method)

	if rl.Debug {
		log.Printf("[DEBUG] Returning metrics: Goodput=%f, Latency=%f\n", goodput, latency)
	}

	response := struct {
		Goodput float64 `json:"goodput"`
		Latency float64 `json:"latency"`
	}{
		Goodput: goodput,
		Latency: latency,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
