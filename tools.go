//go:build tools

package tools

import (
	_ "github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/spf13/cobra"
	_ "go.opentelemetry.io/otel"
	_ "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	_ "go.opentelemetry.io/otel/sdk"
	_ "go.opentelemetry.io/otel/trace"
	_ "google.golang.org/grpc"
	_ "google.golang.org/protobuf/proto"
	_ "pgregory.net/rapid"
)
