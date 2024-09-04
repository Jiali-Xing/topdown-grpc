package topdown

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

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

	methodName := r.URL.Query().Get("method")
	if methodName == "" {
		http.Error(w, "Method not specified", http.StatusBadRequest)
		return
	}

	goodput, latency := rl.GetMetrics(methodName)

	response := struct {
		Goodput int64         `json:"goodput"`
		Latency time.Duration `json:"latency"`
	}{
		Goodput: goodput,
		Latency: latency,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
