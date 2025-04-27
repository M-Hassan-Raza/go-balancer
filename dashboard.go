package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// DashboardConfig stores the dashboard configuration
type DashboardConfig struct {
	Port            int
	LoadBalancerURL string
	BackendURLs     []string
	RefreshInterval time.Duration
}

// SystemStatus represents the entire system status
type SystemStatus struct {
	LoadBalancer LoadBalancerStatus
	Backends     []BackendStatus
	LastUpdated  time.Time
}

// LoadBalancerStatus represents load balancer metrics
type LoadBalancerStatus struct {
	Uptime         string
	ActiveBackends int
	TotalBackends  int
	RequestCount   int64
	IsHealthy      bool
}

// BackendStatus represents a backend server status
type BackendStatus struct {
	Name           string
	URL            string
	IsAlive        bool
	IsReady        bool
	RequestCount   int64
	ErrorCount     int64
	AvgResponseMs  float64
	LastCheckedAt  time.Time
	ResponseStatus int
}

// Dashboard handles the admin dashboard functionality
type Dashboard struct {
	config     DashboardConfig
	status     SystemStatus
	statusMux  sync.RWMutex
	httpClient *http.Client
	templates  *template.Template
}

// NewDashboard creates a new admin dashboard
func NewDashboard() *Dashboard {
	// Get port from environment or use default
	port := 8090
	if portStr := os.Getenv("DASHBOARD_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}

	// Get load balancer URL from environment or use default
	lbURL := os.Getenv("LOAD_BALANCER_URL")
	if lbURL == "" {
		lbURL = "http://localhost:8080"
	}

	// Get backend URLs from environment or use defaults
	backendURLs := []string{
		"http://localhost:8081",
		"http://localhost:8082",
		"http://localhost:8083",
	}

	// Create HTTP client with timeouts
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     60 * time.Second,
		},
	}

	// Load HTML templates
	tmpl := template.Must(template.New("dashboard").Parse(dashboardTemplate))

	return &Dashboard{
		config: DashboardConfig{
			Port:            port,
			LoadBalancerURL: lbURL,
			BackendURLs:     backendURLs,
			RefreshInterval: 5 * time.Second,
		},
		status: SystemStatus{
			Backends: make([]BackendStatus, 0),
		},
		httpClient: client,
		templates:  tmpl,
	}
}

// Start begins the dashboard service
func (d *Dashboard) Start() error {
	// Set up HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handleDashboard)
	mux.HandleFunc("/api/status", d.handleStatusAPI)
	mux.HandleFunc("/api/toggle-backend", d.handleToggleBackend)
	mux.HandleFunc("/styles.css", d.handleCSS)

	// Start collecting metrics
	d.startMetricsCollection()

	// Create HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", d.config.Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server
	log.Printf("Starting admin dashboard at http://localhost:%d", d.config.Port)
	return srv.ListenAndServe()
}

// startMetricsCollection begins periodic collection of system metrics
func (d *Dashboard) startMetricsCollection() {
	ticker := time.NewTicker(d.config.RefreshInterval)

	go func() {
		// Collect metrics immediately
		d.collectMetrics()

		for range ticker.C {
			d.collectMetrics()
		}
	}()
}

// collectMetrics gathers metrics from all components
func (d *Dashboard) collectMetrics() {
	var wg sync.WaitGroup
	status := SystemStatus{
		LastUpdated: time.Now(),
		Backends:    make([]BackendStatus, len(d.config.BackendURLs)),
	}

	// Collect load balancer metrics
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.collectLoadBalancerMetrics(&status.LoadBalancer)
	}()

	// Collect backend metrics
	for i, url := range d.config.BackendURLs {
		wg.Add(1)
		go func(idx int, backendURL string) {
			defer wg.Done()
			status.Backends[idx] = d.collectBackendMetrics(backendURL)
		}(i, url)
	}

	wg.Wait()

	// Count active backends
	activeCount := 0
	for _, backend := range status.Backends {
		if backend.IsAlive {
			activeCount++
		}
	}
	status.LoadBalancer.ActiveBackends = activeCount
	status.LoadBalancer.TotalBackends = len(status.Backends)

	// Update status with write lock
	d.statusMux.Lock()
	d.status = status
	d.statusMux.Unlock()
}

// collectLoadBalancerMetrics gathers metrics from the load balancer
func (d *Dashboard) collectLoadBalancerMetrics(status *LoadBalancerStatus) {
	// For now, just mark as healthy if we can connect
	status.IsHealthy = d.checkEndpoint(d.config.LoadBalancerURL)
	
	// In a real implementation, you would have a metrics endpoint
	// on the load balancer to collect actual metrics
	status.Uptime = "Unknown" // This would come from the load balancer
	status.RequestCount = 0   // This would come from the load balancer
}

// collectBackendMetrics gathers metrics from a backend server
func (d *Dashboard) collectBackendMetrics(backendURL string) BackendStatus {
	status := BackendStatus{
		URL:           backendURL,
		Name:          extractServerName(backendURL),
		LastCheckedAt: time.Now(),
		IsAlive:       false,
		IsReady:       false,
	}

	// Check liveness
	resp, err := d.httpClient.Get(backendURL + "/healthz")
	if err == nil {
		status.ResponseStatus = resp.StatusCode
		status.IsAlive = resp.StatusCode == http.StatusOK
		resp.Body.Close()
	}

	// Check readiness
	resp, err = d.httpClient.Get(backendURL + "/readyz")
	if err == nil {
		status.IsReady = resp.StatusCode == http.StatusOK
		resp.Body.Close()
	}

	// Get metrics if server is alive
	if status.IsAlive {
		resp, err := d.httpClient.Get(backendURL + "/metrics")
		if err == nil && resp.StatusCode == http.StatusOK {
			var metrics map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&metrics); err == nil {
				if reqCount, ok := metrics["requests"].(float64); ok {
					status.RequestCount = int64(reqCount)
				}
				if errCount, ok := metrics["errors"].(float64); ok {
					status.ErrorCount = int64(errCount)
				}
				if avgRespTime, ok := metrics["avg_response_time"].(string); ok {
					duration, _ := time.ParseDuration(avgRespTime)
					status.AvgResponseMs = float64(duration) / float64(time.Millisecond)
				}
			}
			resp.Body.Close()
		}
	}

	return status
}

// checkEndpoint simply checks if an endpoint is accessible
func (d *Dashboard) checkEndpoint(url string) bool {
	resp, err := d.httpClient.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

// handleDashboard renders the dashboard HTML
func (d *Dashboard) handleDashboard(w http.ResponseWriter, r *http.Request) {
	d.statusMux.RLock()
	status := d.status
	d.statusMux.RUnlock()

	w.Header().Set("Content-Type", "text/html")
	d.templates.ExecuteTemplate(w, "dashboard", status)
}

// handleStatusAPI returns the system status as JSON
func (d *Dashboard) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	d.statusMux.RLock()
	status := d.status
	d.statusMux.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleToggleBackend toggles a backend server's readiness state
func (d *Dashboard) handleToggleBackend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request
	var req struct {
		URL    string `json:"url"`
		Enable bool   `json:"enable"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Send toggle request to backend
	payload := map[string]bool{"ready": req.Enable}
	jsonData, _ := json.Marshal(payload)

	resp, err := d.httpClient.Post(
		req.URL+"/admin/ready/",
		"application/json",
		bytes.NewBuffer(jsonData),
	)

	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to toggle backend", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Force metrics refresh
	go d.collectMetrics()

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"success": true}`)
}

// handleCSS serves the CSS for the dashboard
func (d *Dashboard) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css")
	fmt.Fprint(w, dashboardCSS)
}

// extractServerName extracts a readable name from a URL
func extractServerName(url string) string {
	// Parse the URL to extract the port
	parts := bufio.NewScanner(bytes.NewBufferString(url))
	parts.Split(bufio.ScanRunes)
	var port bytes.Buffer

	foundColon := false
	for parts.Scan() {
		if foundColon {
			if parts.Text() >= "0" && parts.Text() <= "9" {
				port.WriteString(parts.Text())
			} else {
				break
			}
		} else if parts.Text() == ":" {
			foundColon = true
		}
	}

	if port.Len() > 0 {
		return "Backend " + port.String()
	}
	return "Unknown Backend"
}

func main() {
	// Set up logging
	log.SetPrefix("[Dashboard] ")
	log.SetFlags(log.Ldate | log.Ltime)

	// Create and start dashboard
	dashboard := NewDashboard()

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start dashboard in a goroutine
	go func() {
		if err := dashboard.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Dashboard error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-stop
	log.Println("Shutting down dashboard...")

	// Allow any in-flight operations to complete
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Give time for in-flight operations
	<-ctx.Done()
	log.Println("Dashboard stopped")
}

// HTML template for the dashboard
const dashboardTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Go Load Balancer Dashboard</title>
    <link rel="stylesheet" href="/styles.css">
    <meta http-equiv="refresh" content="5">
</head>
<body>
    <header>
        <h1>Go Load Balancer Dashboard</h1>
        <div class="last-updated">Last updated: {{.LastUpdated.Format "15:04:05"}}</div>
    </header>
    
    <main>
        <section class="system-overview">
            <h2>System Overview</h2>
            <div class="stats-container">
                <div class="stat-card {{if .LoadBalancer.IsHealthy}}healthy{{else}}unhealthy{{end}}">
                    <h3>Load Balancer</h3>
                    <div class="stat">
                        <span class="label">Status:</span>
                        <span class="value">{{if .LoadBalancer.IsHealthy}}Healthy{{else}}Unhealthy{{end}}</span>
                    </div>
                    <div class="stat">
                        <span class="label">Active Backends:</span>
                        <span class="value">{{.LoadBalancer.ActiveBackends}}/{{.LoadBalancer.TotalBackends}}</span>
                    </div>
                </div>
            </div>
        </section>
        
        <section class="backends">
            <h2>Backend Servers</h2>
            <div class="backend-grid">
                {{range .Backends}}
                <div class="backend-card {{if .IsAlive}}alive{{else}}dead{{end}} {{if .IsReady}}ready{{else}}not-ready{{end}}">
                    <h3>{{.Name}}</h3>
                    <div class="status-indicator">
                        {{if .IsAlive}}
                            {{if .IsReady}}
                                <span class="status ready">Ready</span>
                            {{else}}
                                <span class="status not-ready">Not Ready</span>
                            {{end}}
                        {{else}}
                            <span class="status dead">Offline</span>
                        {{end}}
                    </div>
                    <div class="stats">
                        <div class="stat">
                            <span class="label">URL:</span>
                            <span class="value">{{.URL}}</span>
                        </div>
                        <div class="stat">
                            <span class="label">Requests:</span>
                            <span class="value">{{.RequestCount}}</span>
                        </div>
                        <div class="stat">
                            <span class="label">Errors:</span>
                            <span class="value">{{.ErrorCount}}</span>
                        </div>
                        <div class="stat">
                            <span class="label">Avg Response:</span>
                            <span class="value">{{printf "%.2f" .AvgResponseMs}} ms</span>
                        </div>
                    </div>
                    {{if .IsAlive}}
                    <div class="actions">
                        <button class="toggle-btn" data-url="{{.URL}}" data-ready="{{if .IsReady}}false{{else}}true{{end}}">
                            {{if .IsReady}}Mark Not Ready{{else}}Mark Ready{{end}}
                        </button>
                    </div>
                    {{end}}
                </div>
                {{end}}
            </div>
        </section>
    </main>
    
    <script>
        document.addEventListener('DOMContentLoaded', function() {
            const toggleButtons = document.querySelectorAll('.toggle-btn');
            
            toggleButtons.forEach(btn => {
                btn.addEventListener('click', function() {
                    const url = this.getAttribute('data-url');
                    const enable = this.getAttribute('data-ready') === 'true';
                    
                    fetch('/api/toggle-backend', {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json'
                        },
                        body: JSON.stringify({
                            url: url,
                            enable: enable
                        })
                    })
                    .then(response => {
                        if (response.ok) {
                            // Refresh the page after toggle
                            setTimeout(() => window.location.reload(), 500);
                        }
                    });
                });
            });
        });
    </script>
</body>
</html>
`

// CSS for the dashboard
const dashboardCSS = `
:root {
    --color-bg: #f5f5f5;
    --color-text: #333;
    --color-primary: #0077cc;
    --color-secondary: #3a9fee;
    --color-success: #2ecc71;
    --color-warning: #f39c12;
    --color-danger: #e74c3c;
    --color-card-bg: #fff;
    --color-card-border: #ddd;
    --shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
}

* {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
}

body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    line-height: 1.6;
    color: var(--color-text);
    background: var(--color-bg);
    padding: 20px;
}

header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 20px;
    padding-bottom: 10px;
    border-bottom: 1px solid var(--color-card-border);
}

h1 {
    font-size: 24px;
    color: var(--color-primary);
}

.last-updated {
    font-size: 14px;
    color: #666;
}

h2 {
    font-size: 20px;
    margin-bottom: 15px;
    color: var(--color-text);
}

h3 {
    font-size: 16px;
    margin-bottom: 10px;
}

section {
    margin-bottom: 30px;
}

.stats-container {
    display: flex;
    gap: 20px;
    flex-wrap: wrap;
}

.stat-card {
    background: var(--color-card-bg);
    border-radius: 8px;
    padding: 15px;
    box-shadow: var(--shadow);
    flex: 1;
    min-width: 250px;
    border-top: 4px solid var(--color-secondary);
}

.stat-card.healthy {
    border-top-color: var(--color-success);
}

.stat-card.unhealthy {
    border-top-color: var(--color-danger);
}

.stat {
    display: flex;
    justify-content: space-between;
    margin-bottom: 8px;
    font-size: 14px;
}

.backend-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 20px;
}

.backend-card {
    background: var(--color-card-bg);
    border-radius: 8px;
    padding: 15px;
    box-shadow: var(--shadow);
    position: relative;
    border-left: 4px solid var(--color-secondary);
}

.backend-card.alive {
    border-left-color: var(--color-success);
}

.backend-card.dead {
    border-left-color: var(--color-danger);
    opacity: 0.7;
}

.backend-card.not-ready {
    border-left-color: var(--color-warning);
}

.status-indicator {
    position: absolute;
    top: 15px;
    right: 15px;
}

.status {
    display: inline-block;
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: bold;
    text-transform: uppercase;
}

.status.ready {
    background: var(--color-success);
    color: white;
}

.status.not-ready {
    background: var(--color-warning);
    color: white;
}

.status.dead {
    background: var(--color-danger);
    color: white;
}

.stats {
    margin: 15px 0;
}

.actions {
    margin-top: 15px;
    text-align: right;
}

.toggle-btn {
    background: var(--color-primary);
    color: white;
    border: none;
    padding: 8px 12px;
    border-radius: 4px;
    cursor: pointer;
    font-size: 14px;
    transition: background 0.2s;
}

.toggle-btn:hover {
    background: var(--color-secondary);
}

@media (max-width: 768px) {
    .backend-grid {
        grid-template-columns: 1fr;
    }
    
    .stats-container {
        flex-direction: column;
    }
}
`