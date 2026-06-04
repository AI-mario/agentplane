package gateway

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestGRPCServer_DefaultAddr(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{})
	if srv.config.Addr != ":9090" {
		t.Fatalf("expected default addr :9090, got %s", srv.config.Addr)
	}
}

func TestGRPCServer_CustomAddr(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: ":9999"})
	if srv.config.Addr != ":9999" {
		t.Fatalf("expected addr :9999, got %s", srv.config.Addr)
	}
}

func TestGRPCServer_StartAndStop(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: "127.0.0.1:0"})

	if err := srv.StartAsync(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Stop()

	// Server should report healthy.
	if err := srv.CheckHealth(context.Background()); err != nil {
		t.Fatalf("health check failed: %v", err)
	}

	// Verify actual address is assigned.
	addr := srv.Addr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("expected resolved address, got %s", addr)
	}
}

func TestGRPCServer_HealthCheckProtocol(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: "127.0.0.1:0"})
	if err := srv.StartAsync(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Stop()

	// Connect gRPC client.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, srv.Addr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)

	// Check overall health (empty service name).
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		t.Fatalf("health check RPC failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING, got %v", resp.Status)
	}
}

func TestGRPCServer_SetServiceStatus(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: "127.0.0.1:0"})
	if err := srv.StartAsync(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Stop()

	// Register a subsystem as serving.
	srv.SetServiceStatus("registry", true)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, srv.Addr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)

	// Check named service.
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: "registry"})
	if err != nil {
		t.Fatalf("health check RPC failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING for registry, got %v", resp.Status)
	}

	// Set to not serving.
	srv.SetServiceStatus("registry", false)

	resp, err = client.Check(ctx, &healthpb.HealthCheckRequest{Service: "registry"})
	if err != nil {
		t.Fatalf("health check RPC failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("expected NOT_SERVING for registry, got %v", resp.Status)
	}
}

func TestGRPCServer_DeadlinePropagation(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: "127.0.0.1:0"})
	if err := srv.StartAsync(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Stop()

	// Use a tight deadline to verify context propagation.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := grpc.DialContext(ctx, srv.Addr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)

	// Call with context deadline — gRPC natively propagates deadlines.
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer deadlineCancel()

	resp, err := client.Check(deadlineCtx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		t.Fatalf("health check with deadline failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING, got %v", resp.Status)
	}
}

func TestGRPCServer_DoubleStartFails(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: "127.0.0.1:0"})
	if err := srv.StartAsync(); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	defer srv.Stop()

	if err := srv.StartAsync(); err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestGRPCServer_CheckHealthWhenNotRunning(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: "127.0.0.1:0"})
	if err := srv.CheckHealth(context.Background()); err == nil {
		t.Fatal("expected error when server not running")
	}
}

func TestGRPCServer_StopIdempotent(t *testing.T) {
	srv := NewGRPCServer(GRPCConfig{Addr: "127.0.0.1:0"})
	if err := srv.StartAsync(); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Multiple stops should not panic.
	srv.Stop()
	srv.Stop()
}
