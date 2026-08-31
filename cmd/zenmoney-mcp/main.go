package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/abrekhov/zenmoney-mcp/internal/mcpserver"
	"github.com/abrekhov/zenmoney-mcp/internal/oauthserver"
	"github.com/abrekhov/zenmoney-mcp/internal/zenmoney"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	token := strings.TrimSpace(os.Getenv("ZENMONEY_TOKEN"))
	if token == "" {
		return fmt.Errorf("ZENMONEY_TOKEN is required; obtain one from https://zerro.app/token")
	}
	apiBase := strings.TrimSpace(os.Getenv("ZENMONEY_API_BASE_URL"))
	var api *zenmoney.Client
	if apiBase == "" {
		api = zenmoney.NewClient(token, nil)
	} else {
		api = zenmoney.NewClientWithBaseURL(apiBase, token, nil)
	}
	state := zenmoney.NewState(api)
	server := mcpserver.New(api, state)

	transport := strings.ToLower(strings.TrimSpace(os.Getenv("MCP_TRANSPORT")))
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio":
		slog.Info("starting ZenMoney MCP", "transport", "stdio", "version", mcpserver.Version)
		return server.Run(context.Background(), &mcp.StdioTransport{})
	case "http":
		return runHTTP(server)
	default:
		return fmt.Errorf("unsupported MCP_TRANSPORT %q; use stdio or http", transport)
	}
}

func runHTTP(mcpServer *mcp.Server) error {
	baseURL := strings.TrimSpace(os.Getenv("MCP_BASE_URL"))
	oauth, err := oauthserver.New(oauthserver.Config{
		BaseURL: baseURL, Password: os.Getenv("MCP_OAUTH_PASSWORD"), SigningKey: os.Getenv("MCP_SIGNING_KEY"),
	})
	if err != nil {
		return fmt.Errorf("configure OAuth: %w", err)
	}

	mux := http.NewServeMux()
	oauth.Register(mux)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	mux.Handle("/mcp", oauth.Protect(mcpHandler))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": mcpserver.Version})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "zenmoney-mcp", "version": mcpserver.Version, "mcp": baseURL + "/mcp"})
	})

	address := strings.TrimSpace(os.Getenv("HTTP_ADDR"))
	if address == "" {
		address = ":8080"
	}
	httpServer := &http.Server{
		Addr: address, Handler: securityHeaders(requestLog(mux)),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 310 * time.Second, IdleTimeout: 120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("starting ZenMoney MCP", "transport", "streamable-http", "address", address, "base_url", baseURL, "version", mcpserver.Version)
	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
