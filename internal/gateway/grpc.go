// Package gateway provides protocol adapters for external and internal communication.
// This file implements the internal gRPC server used for inter-component communication,
// health checking, and future microservice decomposition.
package gateway

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// GRPCConfig holds configuration for the internal gRPC server.
type GRPCConfig struct {
	// Addr is the listen address for the gRPC server. Default ":9090".
	Addr string
}

// GRPCServer wraps the internal gRPC server providing health checking
// and a foundation for inter-service communication.
type GRPCServer struct {
	config       GRPCConfig
	server       *grpc.Server
	healthServer *health.Server
	listener     net.Listener

	mu      sync.Mutex
	running bool
}

// NewGRPCServer creates a new internal gRPC server with health checking enabled.
// The server supports context deadline propagation natively through gRPC.
func NewGRPCServer(config GRPCConfig) *GRPCServer {
	if config.Addr == "" {
		config.Addr = ":9090"
	}

	srv := grpc.NewServer()
	healthSrv := health.NewServer()

	// Register the standard gRPC health check service.
	healthpb.RegisterHealthServer(srv, healthSrv)

	// Enable reflection for debugging and tooling (grpcurl, etc.).
	reflection.Register(srv)

	// Set overall server status to SERVING by default.
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	return &GRPCServer{
		config:       config,
		server:       srv,
		healthServer: healthSrv,
	}
}

// Server returns the underlying grpc.Server for registering additional services.
// Use this to add custom service implementations before calling Start().
func (s *GRPCServer) Server() *grpc.Server {
	return s.server
}

// HealthServer returns the health server for updating subsystem health status.
func (s *GRPCServer) HealthServer() *health.Server {
	return s.healthServer
}

// SetServiceStatus updates the health status of a named service.
// Use this to report subsystem health (e.g., "registry", "scheduler", "policy").
func (s *GRPCServer) SetServiceStatus(service string, serving bool) {
	status := healthpb.HealthCheckResponse_NOT_SERVING
	if serving {
		status = healthpb.HealthCheckResponse_SERVING
	}
	s.healthServer.SetServingStatus(service, status)
}

// Start begins listening and serving gRPC requests.
// This call blocks until the server is stopped.
func (s *GRPCServer) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("grpc server already running")
	}

	lis, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("grpc listen on %s: %w", s.config.Addr, err)
	}
	s.listener = lis
	s.running = true
	s.mu.Unlock()

	log.Printf("gRPC server listening on %s", s.config.Addr)
	return s.server.Serve(lis)
}

// StartAsync starts the gRPC server in a background goroutine.
// Returns an error if the listener cannot be created.
func (s *GRPCServer) StartAsync() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("grpc server already running")
	}

	lis, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("grpc listen on %s: %w", s.config.Addr, err)
	}
	s.listener = lis
	s.running = true
	s.mu.Unlock()

	go func() {
		log.Printf("gRPC server listening on %s", s.config.Addr)
		if err := s.server.Serve(lis); err != nil {
			log.Printf("gRPC server stopped: %v", err)
		}
	}()
	return nil
}

// Stop gracefully stops the gRPC server, allowing in-flight RPCs to complete.
func (s *GRPCServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	// Transition health to NOT_SERVING before stopping.
	s.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	s.server.GracefulStop()
	s.running = false
}

// ForceStop immediately stops the gRPC server without waiting for in-flight RPCs.
func (s *GRPCServer) ForceStop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	s.server.Stop()
	s.running = false
}

// Addr returns the actual address the server is listening on.
// Useful when configured with ":0" for dynamic port assignment.
func (s *GRPCServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.config.Addr
}

// CheckHealth implements the HealthChecker interface for integration with the REST gateway.
func (s *GRPCServer) CheckHealth(ctx context.Context) error {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running {
		return fmt.Errorf("grpc server not running")
	}
	return nil
}
