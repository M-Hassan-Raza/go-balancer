# Go HTTP Load Balancer

A lightweight HTTP load balancer implementation built in Go, designed to distribute traffic efficiently across multiple backend servers.

## Overview

This project demonstrates how to create a production-ready HTTP load balancer with minimal code, leveraging Go's powerful standard library. It consists of two main components:

1. **Load Balancer** - Distributes incoming HTTP requests across multiple backend servers using a round-robin algorithm while performing health checks.

2. **Backend Server** - Handles HTTP requests and provides health endpoints for monitoring.

## Features

- **Efficient Round-Robin Load Balancing** - Distributes traffic evenly
- **Health Monitoring** - Automatically detects and routes around failed servers
- **Metrics Collection** - Tracks response times and request counts
- **Graceful Shutdown** - Handles termination signals properly
- **Production-Ready Timeouts** - Configurable connection handling
- **Kubernetes-Compatible** - Health endpoints follow standard patterns

## Technical Details

The load balancer utilizes Go's `net/http/httputil` package to create reverse proxies to backend servers. Each backend server is continuously monitored through TCP health checks to ensure availability. When a server becomes unavailable, it is automatically removed from the pool until it recovers.

The system achieves high performance through Go's concurrency model and efficient standard library implementations. The entire load balancer core functionality is implemented in under 300 lines of code, demonstrating Go's simplicity and power for network services.

## Configuration

Both the load balancer and backend servers can be configured through environment variables or command-line flags:

- `PROXY_PORT` - Port for the load balancer (default: 8080)
- `SERVER_PORT` - Port for backend servers (default: 8081)
- `SERVER_NAME` - Custom identifier for backend servers

## Usage

Start the load balancer:

```
go run loadbalancer.go
```

Start multiple backend servers on different ports:

```
SERVER_PORT=8081 go run backend.go
SERVER_PORT=8082 go run backend.go
SERVER_PORT=8083 go run backend.go
```

The load balancer will distribute incoming requests across all available backend servers, automatically detecting and routing around any that become unavailable.

## Use Cases

- Development and testing environments
- Microservices architecture
- Small to medium production deployments
- Educational purposes for understanding load balancing concepts
