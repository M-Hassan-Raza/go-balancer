package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

// ServerMetrics tracks basic server statistics
type ServerMetrics struct {
	StartTime      time.Time
	RequestCount   uint64
	ErrorCount     uint64
	ResponseTimes  []time.Duration // Last 100 response times
	responseIndex  int
	maxStoredTimes int
}

// RecordRequest tracks a successful request
func (m *ServerMetrics) RecordRequest() uint64 {
	return atomic.AddUint64(&m.RequestCount, 1)
}

// RecordError tracks a request error
func (m *ServerMetrics) RecordError() uint64 {
	return atomic.AddUint64(&m.ErrorCount, 1)
}

// RecordResponseTime stores the time taken to process a request
func (m *ServerMetrics) RecordResponseTime(duration time.Duration) {
	// Use modulo to implement a circular buffer
	idx := m.responseIndex % m.maxStoredTimes
	
	if len(m.ResponseTimes) < m.maxStoredTimes {
		m.ResponseTimes = append(m.ResponseTimes, duration)
	} else {
		m.ResponseTimes[idx] = duration
	}
	
	m.responseIndex++
}

// AverageResponseTime calculates the average response time
func (m *ServerMetrics) AverageResponseTime() time.Duration {
	if len(m.ResponseTimes) == 0 {
		return 0
	}
	
	var total time.Duration
	for _, t := range m.ResponseTimes {
		total += t
	}
	
	return total / time.Duration(len(m.ResponseTimes))
}

// Uptime returns how long the server has been running
func (m *ServerMetrics) Uptime() time.Duration {
	return time.Since(m.StartTime)
}

// BackendServer encapsulates server functionality
type BackendServer struct {
	port        int
	name        string
	metrics     *ServerMetrics
	readiness   int32 // Atomic flag for readiness state
	livenessErr error // Error that affects liveness, if any
}

// NewBackendServer creates a new server instance
func NewBackendServer() *BackendServer {
	// Get server port from environment or use default
	port := 8081
	if portStr := os.Getenv("SERVER_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}

	// Get server name from environment or use hostname
	name := os.Getenv("SERVER_NAME")
	if name == "" {
		hostname, err := os.Hostname()
		if err == nil {
			name = hostname
		} else {
			name = fmt.Sprintf("backend-%d", port)
		}
	}

	return &BackendServer{
		port:      port,
		name:      name,
		readiness: 1, // Start as ready
		metrics: &ServerMetrics{
			StartTime:      time.Now(),
			maxStoredTimes: 100,
		},
	}
}

// IsReady reports whether the server is ready to serve requests
func (s *BackendServer) IsReady() bool {
	return atomic.LoadInt32(&s.readiness) == 1
}

// SetReady updates the server's readiness state
func (s *BackendServer) SetReady(ready bool) {
	var value int32 = 0
	if ready {
		value = 1
	}
	atomic.StoreInt32(&s.readiness, value)
}

// setupRoutes configures all HTTP routes
func (s *BackendServer) setupRoutes() http.Handler {
	mux := http.NewServeMux()
	
	// Main handler for processing requests
	mux.HandleFunc("/", s.handleRequest)
	
	// Health check endpoints following Kubernetes conventions
	mux.HandleFunc("/healthz", s.livenessHandler)  // Liveness probe
	mux.HandleFunc("/readyz", s.readinessHandler)  // Readiness probe
	
	// Metrics endpoint
	mux.HandleFunc("/metrics", s.metricsHandler)
	
	// Admin endpoints
	mux.HandleFunc("/admin/ready/", s.adminReadyHandler)
	
	// Return the configured handler with middleware applied
	return s.loggingMiddleware(s.metricsMiddleware(mux))
}

// handleRequest processes the main application requests
func (s *BackendServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Simulate variable processing time
	processingTime := 50 + (time.Now().UnixNano() % 100)
	time.Sleep(time.Duration(processingTime) * time.Millisecond)
	
	// Write response with server information
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	
	response := map[string]interface{}{
		"server": map[string]interface{}{
			"name": s.name,
			"port": s.port,
			"time": time.Now().Format(time.RFC3339),
		},
		"request": map[string]interface{}{
			"path":    r.URL.Path,
			"method":  r.Method,
			"headers": r.Header,
			"remote":  r.RemoteAddr,
		},
	}
	
	json.NewEncoder(w).Encode(response)
}

// livenessHandler responds to liveness health checks
func (s *BackendServer) livenessHandler(w http.ResponseWriter, r *http.Request) {
	if s.livenessErr != nil {
		http.Error(w, s.livenessErr.Error(), http.StatusServiceUnavailable)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
}

// readinessHandler responds to readiness health checks
func (s *BackendServer) readinessHandler(w http.ResponseWriter, r *http.Request) {
	if !s.IsReady() {
		http.Error(w, "Service not ready", http.StatusServiceUnavailable)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

// metricsHandler exposes basic metrics
func (s *BackendServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	metrics := map[string]interface{}{
		"uptime":             s.metrics.Uptime().String(),
		"requests":           s.metrics.RequestCount,
		"errors":             s.metrics.ErrorCount,
		"avg_response_time":  s.metrics.AverageResponseTime().String(),
	}
	
	json.NewEncoder(w).Encode(metrics)
}

// adminReadyHandler toggles readiness state (for testing)
func (s *BackendServer) adminReadyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var readyState struct {
		Ready bool `json:"ready"`
	}
	
	err := json.NewDecoder(r.Body).Decode(&readyState)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	s.SetReady(readyState.Ready)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ready": s.IsReady()})
}

// metricsMiddleware records request metrics
func (s *BackendServer) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a response wrapper to capture status code
		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}
		
		// Process the request
		next.ServeHTTP(rw, r)
		
		// Record metrics
		duration := time.Since(start)
		s.metrics.RecordResponseTime(duration)
		s.metrics.RecordRequest()
		
		if rw.statusCode >= 400 {
			s.metrics.RecordError()
		}
	})
}

// loggingMiddleware adds request logging
func (s *BackendServer) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a response wrapper to capture status code
		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}
		
		// Process the request
		next.ServeHTTP(rw, r)
		
		// Log request details
		duration := time.Since(start)
		log.Printf(
			"[%s] %s - %s %s - %d - %s",
			s.name,
			r.RemoteAddr,
			r.Method,
			r.URL.Path,
			rw.statusCode,
			duration,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before writing it
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func main() {
	// Configure logging
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)
	log.SetPrefix("[Backend] ")
	
	// Create and configure server
	server := NewBackendServer()
	
	// Create HTTP server with reasonable timeouts
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", server.port),
		Handler:      server.setupRoutes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	
	// Start server in a goroutine
	go func() {
		log.Printf("Starting backend server '%s' on port %d", server.name, server.port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()
	
	// Set up graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	
	// Wait for termination signal
	<-stop
	log.Println("Shutdown initiated...")
	
	// First mark server as not ready to receive new requests
	server.SetReady(false)
	log.Println("Marked as not ready, waiting for in-flight requests...")
	
	// Give time for load balancer to notice the server is not ready
	time.Sleep(2 * time.Second)
	
	// Initiate graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}
	
	log.Printf("Server '%s' shutdown complete", server.name)
}