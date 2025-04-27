# Go HTTP Load Balancer

A lightweight HTTP load balancer implementation built in Go, designed to distribute traffic efficiently across multiple backend servers.

## Overview

This project demonstrates how to create a production-ready HTTP load balancer with minimal code, leveraging Go's powerful standard library. It consists of three main components:

1. **Load Balancer** - Distributes incoming HTTP requests across multiple backend servers using a round-robin algorithm while performing health checks.

2. **Backend Server** - Handles HTTP requests and provides health endpoints for monitoring.

3. **Admin Dashboard** - Provides real-time monitoring and control of the entire system.

## Features

- **Efficient Round-Robin Load Balancing** - Distributes traffic evenly
- **Health Monitoring** - Automatically detects and routes around failed servers
- **Metrics Collection** - Tracks response times and request counts
- **Graceful Shutdown** - Handles termination signals properly
- **Production-Ready Timeouts** - Configurable connection handling
- **Kubernetes-Compatible** - Health endpoints follow standard patterns
- **Administrative Controls** - Web interface for monitoring and managing servers

## Technical Details

The load balancer utilizes Go's `net/http/httputil` package to create reverse proxies to backend servers. Each backend server is continuously monitored through TCP health checks to ensure availability. When a server becomes unavailable, it is automatically removed from the pool until it recovers.

The system achieves high performance through Go's concurrency model and efficient standard library implementations. The entire load balancer core functionality is implemented in under 300 lines of code, demonstrating Go's simplicity and power for network services.

The admin dashboard provides a real-time view of system health and performance metrics, allowing operators to monitor traffic distribution and server availability from a single interface. It also offers controls to administratively mark servers as ready or not ready for maintenance purposes.

## Configuration

All components can be configured through environment variables or command-line flags:

- `PROXY_PORT` - Port for the load balancer (default: 8080)
- `SERVER_PORT` - Port for backend servers (default: 8081)
- `SERVER_NAME` - Custom identifier for backend servers
- `DASHBOARD_PORT` - Port for the admin dashboard (default: 8090)
- `LOAD_BALANCER_URL` - URL to reach the load balancer (default: http://localhost:8080)

## Usage

Start the load balancer:

```
go run loadbalancer.go
```

Start multiple backend servers on different ports:

```
SERVER_PORT=8081 go run server.go
SERVER_PORT=8082 go run server.go
SERVER_PORT=8083 go run server.go
```

Start the admin dashboard:

```
go run dashboard.go
```

The load balancer will distribute incoming requests across all available backend servers, automatically detecting and routing around any that become unavailable. The admin dashboard will be accessible at http://localhost:8090 by default.

## Use Cases

- Development and testing environments
- Microservices architecture
- Small to medium production deployments
- Educational purposes for understanding load balancing concepts