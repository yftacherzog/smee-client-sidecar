package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// --- Configuration from Environment Variables ---
	downstreamServiceURL string
	smeeChannelURL       string

	// --- Prometheus Metrics ---
	forwardAttempts = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "smee_events_relayed_total",
			Help: "Total number of regular events relayed by the sidecar.",
		},
	)

	// --- Shared State for Health Checks ---
	healthCheckIDs = make(map[string]bool)
	mutex          = &sync.Mutex{}
)

// HealthCheckPayload defines the structure of our self-test event
type HealthCheckPayload struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// forwardHandler remains the same as the last correct version
func forwardHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "cannot read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var healthCheck HealthCheckPayload
	if r.Header.Get("Content-Type") == "application/json" {
		if json.Unmarshal(body, &healthCheck) == nil && healthCheck.Type == "health-check" {
			mutex.Lock()
			healthCheckIDs[healthCheck.ID] = true
			mutex.Unlock()
			log.Printf("Received health check event with ID: %s", healthCheck.ID)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Health check received"))
			return
		}
	}

	forwardAttempts.Inc()
	log.Printf("Relaying regular event to: %s%s", downstreamServiceURL, r.URL.Path)

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, downstreamServiceURL+r.URL.String(), bytes.NewReader(body))
	if err != nil {
		log.Printf("Error creating proxy request: %v", err)
		http.Error(w, "error creating proxy request", http.StatusInternalServerError)
		return
	}

	proxyReq.Header = make(http.Header)
	for h, val := range r.Header {
		proxyReq.Header[h] = val
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		log.Printf("Error forwarding request: %v", err)
		http.Error(w, "error forwarding request", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	for h, val := range resp.Header {
		w.Header()[h] = val
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("Error writing response body: %v", err)
	}
}

// runHealthCheckLoop has the corrected loop structure
func runHealthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	healthFilePath := "/tmp/health/live"

	// Define the check logic as a local function to avoid repetition
	runCheck := func() {
		log.Println("Health Check: Running self-test...")
		testID := uuid.New().String()
		payload := HealthCheckPayload{Type: "health-check", ID: testID}
		payloadBytes, _ := json.Marshal(payload)

		// 1. Post the test event
		req, err := http.NewRequestWithContext(context.Background(), "POST", smeeChannelURL, bytes.NewBuffer(payloadBytes))
		if err != nil {
			log.Printf("Health Check Loop Error: could not create request: %v", err)
			return // Exit this check run
		}
		req.Header.Set("Content-Type", "application/json")

		// Important: Create a new client for each check to avoid issues with proxies and TLS
		client := &http.Client{Timeout: 5 * time.Second}
		if _, err = client.Do(req); err != nil {
			log.Printf("Health Check Loop Error: could not post to Smee server: %v", err)
			os.Remove(healthFilePath) // If we can't even post, remove the file to signal failure
			return
		}

		// 2. Wait up to 20 seconds for the event to be received
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		found := false
	checkLoop:
		for {
			select {
			case <-ctx.Done(): // Timeout reached
				break checkLoop
			default:
				mutex.Lock()
				if healthCheckIDs[testID] {
					delete(healthCheckIDs, testID)
					found = true
				}
				mutex.Unlock()
				if found {
					break checkLoop
				}
				time.Sleep(100 * time.Millisecond)
			}
		}

		// 3. Update the shared health file based on the result
		if found {
			log.Println("Health Check: PASSED. Updating heartbeat file with current timestamp.")
			timestamp := fmt.Sprintf("%d", time.Now().Unix())
			if err := os.WriteFile(healthFilePath, []byte(timestamp), 0644); err != nil {
				log.Printf("Health Check Warning: could not write to heartbeat file: %v", err)
			}
		} else {
			log.Println("Health Check: FAILED. Removing heartbeat file to trigger probe failure.")
			os.Remove(healthFilePath)
		}
	}

	// --- This is the corrected loop structure ---
	// Run the check once immediately on startup.
	runCheck()

	// Then, run it again on every tick from the ticker's channel.
	for range ticker.C {
		runCheck()
	}
}

func main() {
	log.Println("Starting Smee instrumentation sidecar...")

	// --- Load Configuration ---
	downstreamServiceURL = os.Getenv("DOWNSTREAM_SERVICE_URL")
	smeeChannelURL = os.Getenv("SMEE_CHANNEL_URL")
	if downstreamServiceURL == "" || smeeChannelURL == "" {
		log.Fatal("FATAL: DOWNSTREAM_SERVICE_URL and SMEE_CHANNEL_URL environment variables must be set.")
	}

	// --- Register Prometheus Metrics ---
	prometheus.MustRegister(forwardAttempts)

	// --- Start the background health check loop ---
	go runHealthCheckLoop()

	// --- Start Relay Server (on port 8080) ---
	relayMux := http.NewServeMux()
	relayMux.HandleFunc("/", forwardHandler)
	go func() {
		log.Println("Relay server listening on :8080")
		if err := http.ListenAndServe(":8080", relayMux); err != nil {
			log.Fatalf("FATAL: Relay server failed: %v", err)
		}
	}()

	// --- Start Management Server (on port 9100) ---
	mgmtMux := http.NewServeMux()
	mgmtMux.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Println("Management server (metrics) listening on :9100")
		if err := http.ListenAndServe(":9100", mgmtMux); err != nil {
			log.Fatalf("FATAL: Management server failed: %v", err)
		}
	}()

	// Keep the main goroutine alive forever
	select {}
}
