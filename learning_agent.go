package topdown

import (
	"encoding/json"
	"net/http"
)

// SetRateLimit sets the rate limit (token bucket refill rate) from an external source.
func (rl *TopDownRL) SetRateLimit(rateLimit float64) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	rl.refillRate = int64(rateLimit)
}

// GetMetrics returns the current goodput and latency.
func (rl *TopDownRL) GetMetrics() (float64, float64) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	return float64(rl.getGoodput()), float64(rl.getTailLatency95th().Milliseconds())
}

// handleSetRateLimit handles the SET requests to update the rate limit.
func (rl *TopDownRL) handleSetRateLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		RateLimit float64 `json:"rate_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	rl.SetRateLimit(data.RateLimit)
	w.WriteHeader(http.StatusOK)
}

// handleGetMetrics handles the GET requests to return goodput and latency.
func (rl *TopDownRL) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	goodput, latency := rl.GetMetrics()

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
