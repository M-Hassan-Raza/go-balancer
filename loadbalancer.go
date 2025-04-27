package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ServerNode represents an individual backend server in our pool
type ServerNode struct {
	endpoint    *url.URL
	proxy       *httputil.ReverseProxy
	healthy     bool
	statusMutex sync.RWMutex
}

// IsHealthy reports the current health status of the server
func (s *ServerNode) IsHealthy() bool {
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()
	return s.healthy
}

// UpdateHealth sets the health status of the server
func (s *ServerNode) UpdateHealth(status bool) {
	s.statusMutex.Lock()
	s.healthy = status
	s.statusMutex.Unlock()
}

// Proxy forwards the request to this server node
func (s *ServerNode) Proxy(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

// ProxyCluster manages a collection of backend servers
type ProxyCluster struct {
	nodes       []*ServerNode
	selector    *RoundRobinSelector
	healthTimer *time.Ticker
	stopChan    chan struct{}
}

// RoundRobinSelector implements server selection logic
type RoundRobinSelector struct {
	position int
	mutex    sync.Mutex
}

// Next selects the next available server using round robin
func (r *RoundRobinSelector) Next(nodes []*ServerNode) *ServerNode {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Try up to N times where N is the number of servers
	startPos := r.position
	for i := 0; i < len(nodes); i++ {
		// Move to next position with wrap-around
		r.position = (r.position + 1) % len(nodes)

		// Found a healthy server
		if nodes[r.position].IsHealthy() {
			return nodes[r.position]
		}

		// If we've checked all servers and come back to where we started
		if r.position == startPos {
			break
		}
	}
	return nil // No healthy servers found
}

// NewProxyCluster creates a new cluster with the given server endpoints
func NewProxyCluster(endpoints []string) *ProxyCluster {
	cluster := &ProxyCluster{
		nodes:    make([]*ServerNode, 0, len(endpoints)),
		selector: &RoundRobinSelector{},
		stopChan: make(chan struct{}),
	}

	for _, endpoint := range endpoints {
		parsedURL, err := url.Parse(endpoint)
		if err != nil {
			log.Printf("Invalid server URL %s: %v", endpoint, err)
			continue
		}

		// Configure a reverse proxy for this server
		proxy := httputil.NewSingleHostReverseProxy(parsedURL)
		
		// Customize the director function
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Header.Add("X-Forwarded-By", "GoDynamicProxy")
		}

		// Handle errors when backend fails
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Error proxying to %s: %v", parsedURL.String(), err)
			http.Error(w, "Backend Service Error", http.StatusBadGateway)
		}

		// Create the server node
		node := &ServerNode{
			endpoint: parsedURL,
			proxy:    proxy,
			healthy:  true, // Assume healthy initially, will be checked soon
		}

		cluster.nodes = append(cluster.nodes, node)
		log.Printf("Added backend server: %s", parsedURL.String())
	}

	return cluster
}

// CheckServerHealth determines if a server is responding
func CheckServerHealth(serverURL *url.URL) bool {
	conn, err := net.DialTimeout("tcp", serverURL.Host, 3*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// StartHealthChecks begins periodic health checks of all servers
func (c *ProxyCluster) StartHealthChecks(interval time.Duration) {
	// Run an immediate health check
	c.CheckAllServers()
	
	c.healthTimer = time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-c.healthTimer.C:
				c.CheckAllServers()
			case <-c.stopChan:
				c.healthTimer.Stop()
				return
			}
		}
	}()
}

// CheckAllServers performs a health check on all backend servers
func (c *ProxyCluster) CheckAllServers() {
	for _, node := range c.nodes {
		isHealthy := CheckServerHealth(node.endpoint)
		previousStatus := node.IsHealthy()
		
		// Only log if status changed
		if isHealthy != previousStatus {
			if isHealthy {
				log.Printf("Server %s is now UP", node.endpoint)
			} else {
				log.Printf("Server %s is now DOWN", node.endpoint)
			}
		}
		
		node.UpdateHealth(isHealthy)
	}
}

// StopHealthChecks stops the health check routine
func (c *ProxyCluster) StopHealthChecks() {
	close(c.stopChan)
}

// ServeHTTP implements the http.Handler interface
func (c *ProxyCluster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Choose a backend server
	server := c.selector.Next(c.nodes)
	if server == nil {
		http.Error(w, "No available backends", http.StatusServiceUnavailable)
		return
	}

	log.Printf("Routing %s %s to %s", r.Method, r.URL.Path, server.endpoint)
	server.Proxy(w, r)
}

func main() {
	// Setup logging
	log.SetPrefix("[PROXY] ")
	
	// Get configuration from environment or use defaults
	listenPort := 8080
	if portStr := os.Getenv("PROXY_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			listenPort = port
		}
	}
	
	// Define default backend servers if not specified
	serverEndpoints := []string{
		"http://localhost:8081",
		"http://localhost:8082",
		"http://localhost:8083",
	}
	
	// Custom endpoints from environment
	if envEndpoints := os.Getenv("BACKEND_SERVERS"); envEndpoints != "" {
		// Parse comma-separated list of endpoints
		// (In a production system, you might use a more robust config approach)
	}
	
	// Create and configure the proxy cluster
	cluster := NewProxyCluster(serverEndpoints)
	
	// Start health checks (every 30 seconds)
	cluster.StartHealthChecks(30 * time.Second)
	defer cluster.StopHealthChecks()
	
	// Create the HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", listenPort),
		Handler:      cluster,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	
	// Setup graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	
	// Start the server in a goroutine
	go func() {
		log.Printf("Load balancer listening on port %d", listenPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()
	
	// Wait for shutdown signal
	<-stop
	log.Println("Shutting down gracefully...")
	
	// Give connections 5 seconds to finish
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}
	
	log.Println("Load balancer stopped")
}