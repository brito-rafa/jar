// Package mcpserver exposes the existing JAR index to agents as read-only
// Model Context Protocol (MCP) tools over Streamable HTTP. It never indexes,
// rebuilds metadata, resets tables, or calls a chat model.
package mcpserver

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/brito-rafa/jar/pkg/connection"
	"github.com/brito-rafa/jar/pkg/duck"
	"github.com/brito-rafa/jar/pkg/inference"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

// Run serves the MCP endpoint until interrupted.
func Run(args []string) error {
	f := flag.NewFlagSet("jar-mcp", flag.ContinueOnError)
	var c connection.Flags
	if err := c.Register(f); err != nil {
		return err
	}
	host := f.String("host", "127.0.0.1", "Listen address; 0.0.0.0 listens on all IPv4 interfaces")
	port := f.Int("port", 8090, "MCP TCP port (1-65535)")
	path := f.String("path", "/mcp", "HTTP path of the Streamable HTTP MCP endpoint")
	embeddingURL := f.String("embedding-url", os.Getenv("EMBEDDING_URL"), "OpenAI-compatible embedding endpoint (required)")
	embeddingAPIModel := f.String("embedding-api-model", os.Getenv("EMBEDDING_API_MODEL"), "Embedding API model ID; must serve the indexed embedding model")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if !strings.HasPrefix(*path, "/") || strings.ContainsAny(*path, "?# ") {
		return fmt.Errorf("path must be an absolute URL path such as /mcp")
	}
	if strings.TrimSpace(*embeddingURL) == "" {
		return fmt.Errorf("embedding-url is required (or set EMBEDDING_URL)")
	}
	db, err := c.Open()
	if err != nil {
		return err
	}
	defer db.Close()
	state, err := duck.Status(db)
	if err != nil {
		return err
	}
	if !state.Ready || state.Chunks == 0 {
		return fmt.Errorf("database has no ready JAR index; run jar-index first")
	}
	embedder, err := inference.OpenEmbedding(context.Background(), *embeddingURL, *embeddingAPIModel)
	if err != nil {
		return err
	}
	defer embedder.Unload(context.Background())

	server := newServer(duckBackend{db: db}, embedder, state.Dimensions, version)
	return serve(server, *host, *port, *path)
}

// Handler serves the MCP server statelessly, so a restarted pod needs no
// client session recovery, and rejects cross-origin browser requests.
func Handler(server *mcp.Server, path string) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.Handle(path, http.NewCrossOriginProtection().Handler(streamable))
	return mux
}

func serve(server *mcp.Server, host string, port int, path string) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("start MCP server: %w", err)
	}
	fmt.Printf("JAR MCP server (read-only tools) listening on http://%s%s\n", listener.Addr(), path)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		fmt.Println("No application authentication; anyone who can reach this port can read indexed text.")
	}
	httpServer := &http.Server{Handler: Handler(server, path), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
		ErrorLog: log.New(os.Stderr, "http: ", log.LstdFlags)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpServer.BaseContext = func(net.Listener) context.Context { return ctx }
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		// Let in-flight tool calls finish before Run closes the database and embedder.
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
		close(shutdownDone)
	}()
	defer func() { stop(); <-shutdownDone }()
	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
