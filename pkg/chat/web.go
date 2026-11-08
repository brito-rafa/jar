package chat

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web/index.html
var chatHTML string

//go:embed web/app.js
var chatJS string

//go:embed web/style.css
var chatCSS string

type webBackend interface {
	handle(context.Context, string) (chatReply, error)
	complete(string) []string
}

func webHandler(session webBackend, publicURLs ...string) http.Handler {
	publicURL := ""
	if len(publicURLs) > 0 {
		publicURL = publicURLs[0]
	}
	return webHandlerPolicy(session, publicURL, false)
}
func webHandlerPolicy(session webBackend, publicURL string, allowAnyHost bool) http.Handler {
	public, publicErr := parsePublicURL(publicURL)
	mux := http.NewServeMux()
	// A single in-flight request owns the local conversation and model contexts.
	gate := make(chan struct{}, 1)
	withSession := func(w http.ResponseWriter, r *http.Request, fn func()) {
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
			if r.Context().Err() != nil {
				return
			}
			fn()
		case <-r.Context().Done():
		}
	}
	jsonReply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, chatHTML)
	})
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, chatJS)
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		io.WriteString(w, chatCSS)
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, map[string]string{"help": slashHelp}) })
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var body struct {
			Message string `json:"message"`
		}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&body); err != nil {
			http.Error(w, "Invalid or oversized message", http.StatusBadRequest)
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "One JSON message required", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Message) == "" {
			http.Error(w, "Message is empty", http.StatusBadRequest)
			return
		}
		withSession(w, r, func() {
			if strings.Contains(r.Header.Get("Accept"), "application/x-ndjson") {
				w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
				w.Header().Set("X-Accel-Buffering", "no")
				ctx, cancel := context.WithCancel(r.Context())
				defer cancel()
				encoder := json.NewEncoder(w)
				send := func(event any) {
					if err := encoder.Encode(event); err != nil {
						cancel()
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						cancel()
					}
				}
				var reply chatReply
				var err error
				if progress, ok := session.(interface {
					handleProgress(context.Context, string, func(chatProgress)) (chatReply, error)
				}); ok {
					reply, err = progress.handleProgress(ctx, body.Message, func(p chatProgress) { send(map[string]any{"type": "progress", "progress": p}) })
				} else {
					reply, err = session.handle(ctx, body.Message)
				}
				if err != nil {
					send(map[string]any{"type": "error", "error": err.Error(), "timing": reply.Timing})
					return
				}
				send(map[string]any{"type": "result", "result": reply})
				return
			}
			reply, err := session.handle(r.Context(), body.Message)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				jsonReply(w, map[string]any{"error": err.Error(), "timing": reply.Timing})
				return
			}
			jsonReply(w, reply)
		})
	})
	mux.HandleFunc("GET /api/complete", func(w http.ResponseWriter, r *http.Request) {
		line := r.URL.Query().Get("line")
		if len(line) > 4096 {
			http.Error(w, "Completion input too long", http.StatusBadRequest)
			return
		}
		withSession(w, r, func() { jsonReply(w, map[string]any{"candidates": session.complete(line)}) })
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicErr != nil {
			http.Error(w, "Invalid public URL configuration", http.StatusInternalServerError)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		local := host == "127.0.0.1" || host == "localhost" || host == "::1"
		configured := public != nil && strings.EqualFold(r.Host, public.Host)
		if !local && !configured && !allowAnyHost {
			http.Error(w, "Host is not allowed; configure -public-url for a reverse proxy", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			allowed := false
			if err == nil && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				allowed = local && u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)
				if allowAnyHost {
					allowed = (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
				}
				// Some reverse proxies preserve the public Host; others rewrite
				// it to loopback. Only the explicitly configured origin is trusted.
				if public != nil && u.Scheme == public.Scheme && strings.EqualFold(u.Host, public.Host) {
					allowed = true
				}
			}
			if !allowed {
				http.Error(w, "Same origin required", http.StatusForbidden)
				return
			}
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	})
}
func parsePublicURL(value string) (*url.URL, error) {
	if value == "" {
		return nil, nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid public URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Host, "* ") {
		return nil, fmt.Errorf("public URL must be an exact http(s) origin without credentials, path, query, or fragment")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid public URL port")
		}
	}
	u.Path = ""
	return u, nil
}
func serveWeb(session webBackend, port int, openBrowser bool, publicURL, bindHost string, allowAnyHost bool) error {
	if _, err := parsePublicURL(publicURL); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("start web chat: %w", err)
	}
	browserHost := bindHost
	if browserHost == "" || browserHost == "0.0.0.0" {
		browserHost = "127.0.0.1"
	} else if browserHost == "::" {
		browserHost = "::1"
	}
	address := "http://" + net.JoinHostPort(browserHost, strconv.Itoa(port))
	fmt.Printf("Listening on %s\n", listener.Addr())
	if allowAnyHost {
		fmt.Println("Any hostname accepted. No application authentication; anyone who can reach this port can use chat.")
	}
	fmt.Printf("JAR browser chat: %s\nPress Ctrl+C to stop. One conversation is shared by browser tabs.\n", address)
	server := &http.Server{Handler: webHandlerPolicy(session, publicURL, allowAnyHost), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		// Request contexts are cancelled first. Wait for handlers to release
		// native model contexts before Run unloads those models.
		_ = server.Shutdown(context.Background())
		close(shutdownDone)
	}()
	defer func() { stop(); <-shutdownDone }()
	if openBrowser {
		// macOS is the supported development host. Start only after binding succeeds.
		cmd := exec.Command("open", address)
		if err := cmd.Run(); err != nil {
			fmt.Printf("Could not open browser: %v. Open %s manually.\n", err, address)
		}
	}
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
