package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/MARETU01/ledger-core/gateway/internal/descriptor"
	"github.com/MARETU01/ledger-core/gateway/internal/discovery"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials"
)

type Engine struct {
	source        discovery.Source
	credentials   credentials.TransportCredentials
	descriptorDir string
	mu            sync.RWMutex
	reloadMu      sync.Mutex
	current       *snapshot
	closed        bool
}
type snapshot struct {
	mux         *http.ServeMux
	connections map[string]*grpc.ClientConn
	revision    string
}

func New(source discovery.Source, creds credentials.TransportCredentials, descriptorDir string) *Engine {
	return &Engine{source: source, credentials: creds, descriptorDir: descriptorDir}
}

// Apply fully builds and validates an immutable snapshot before replacing the old one.
// Existing requests finish on the old snapshot before its connections are closed.
func (e *Engine) Apply(ctx context.Context, data []byte) error {
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()
	e.mu.RLock()
	closed := e.closed
	e.mu.RUnlock()
	if closed {
		return fmt.Errorf("gateway closed")
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return err
	}
	next := &snapshot{mux: http.NewServeMux(), connections: map[string]*grpc.ClientConn{}, revision: cfg.Revision}
	committed := false
	defer func() {
		if !committed {
			next.close()
		}
	}()
	for _, route := range cfg.Routes {
		conn := next.connections[route.Service]
		if conn == nil {
			conn, err = grpc.NewClient("ledger:///"+route.Service,
				grpc.WithResolvers(discovery.Builder{Source: e.source}),
				grpc.WithTransportCredentials(e.credentials.Clone()),
				grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
				grpc.WithDisableRetry(),
				grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(1<<20)))
			if err != nil {
				return fmt.Errorf("route %s: %w", route.ID, err)
			}
			next.connections[route.Service] = conn
		}
		filename := route.DescriptorSet
		if filename != "" && !filepath.IsAbs(filename) {
			filename = filepath.Join(e.descriptorDir, filename)
		}
		loadCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		method, loadErr := descriptor.Load(loadCtx, conn, route.GRPCMethod, filename)
		cancel()
		if loadErr != nil {
			return fmt.Errorf("route %s descriptor: %w", route.ID, loadErr)
		}
		timeout, _ := time.ParseDuration(route.Timeout)
		handler, err := newRouteHandler(route, method, conn, timeout)
		if err != nil {
			return fmt.Errorf("route %s: %w", route.ID, err)
		}
		if err := register(next.mux, route.HTTPMethod+" "+route.HTTPPath, handler); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	old := e.current
	e.current = next
	e.mu.Unlock()
	committed = true
	if old != nil {
		old.close()
	}
	slog.Info("gateway routes applied", "revision", cfg.Revision, "routes", len(cfg.Routes))
	return nil
}

func register(mux *http.ServeMux, pattern string, handler http.Handler) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("invalid HTTP route %q: %v", pattern, value)
		}
	}()
	mux.Handle(pattern, handler)
	return nil
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.current == nil {
		writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "No active routing configuration")
		return
	}
	_, pattern := e.current.mux.Handler(r)
	if pattern == "" {
		allowed := []string{}
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			copy := r.Clone(r.Context())
			copy.Method = method
			if _, p := e.current.mux.Handler(copy); p != "" {
				allowed = append(allowed, method)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeError(w, r, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
		} else {
			writeError(w, r, 404, "NOT_FOUND", "Route not found")
		}
		return
	}
	e.current.mux.ServeHTTP(w, r)
}

func (e *Engine) Ready() (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.current == nil {
		return "", false
	}
	return e.current.revision, true
}

func (s *snapshot) close() {
	for _, conn := range s.connections {
		_ = conn.Close()
	}
}
func (e *Engine) Close() {
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	if e.current != nil {
		e.current.close()
		e.current = nil
	}
}
