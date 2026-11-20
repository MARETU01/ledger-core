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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Engine struct {
	source        discovery.Source
	credentials   credentials.TransportCredentials
	descriptorDir string
	mu            sync.RWMutex
	reload        chan struct{}
	current       *snapshot
	snapshots     map[*snapshot]struct{}
	closed        bool
}
type snapshot struct {
	services map[string]*serviceRuntime
	revision string
	requests sync.WaitGroup
	done     chan struct{}
}

type serviceRuntime struct {
	config    config.Service
	mu        sync.Mutex
	conn      *grpc.ClientConn
	protocols map[string]*protocolLoad
}

type protocolLoad struct {
	done    chan struct{}
	service *descriptor.Service
	err     error
	expires time.Time
}

func New(source discovery.Source, creds credentials.TransportCredentials, descriptorDir string) *Engine {
	return &Engine{
		source: source, credentials: creds, descriptorDir: descriptorDir,
		reload: make(chan struct{}, 1), snapshots: make(map[*snapshot]struct{}),
	}
}

// Apply validates service configuration without contacting backends, then publishes a snapshot.
// Existing requests finish on the old snapshot before its connections are closed.
func (e *Engine) Apply(ctx context.Context, data []byte) error {
	// Serialize builders, but let canceled reloads stop waiting immediately.
	select {
	case e.reload <- struct{}{}:
		defer func() { <-e.reload }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	next := &snapshot{services: make(map[string]*serviceRuntime), revision: cfg.Revision, done: make(chan struct{})}
	for alias, svc := range cfg.Services {
		next.services[alias] = &serviceRuntime{config: svc, protocols: make(map[string]*protocolLoad)}
	}
	e.mu.Lock()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	old := e.current
	e.current = next
	e.snapshots[next] = struct{}{}
	e.mu.Unlock()
	if old != nil {
		go e.retire(old)
	}
	slog.Info("gateway services applied", "revision", cfg.Revision, "services", len(cfg.Services))
	return nil
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Acquire a snapshot lease under the same lock that publishes replacements.
	// Once a snapshot is retired no new leases can be added to its WaitGroup.
	e.mu.Lock()
	s := e.current
	if s != nil {
		s.requests.Add(1)
	}
	e.mu.Unlock()
	if s == nil {
		writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "No active routing configuration")
		return
	}
	defer s.requests.Done()
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || !config.MethodName.MatchString(parts[3]) {
		writeError(w, r, 404, "NOT_FOUND", "Method not found")
		return
	}
	version, alias, name := parts[1], parts[2], parts[3]
	svc := s.services[alias]
	if svc == nil || svc.config.ProtoServices[version] == "" {
		writeError(w, r, 404, "NOT_FOUND", "Service or version not found")
		return
	}
	allowedGET := false
	for _, method := range svc.config.GetMethods[version] {
		if method == name {
			allowedGET = true
			break
		}
	}
	if r.Method != "POST" && (r.Method != "GET" || !allowedGET) {
		allow := "POST"
		if allowedGET {
			allow = "GET, POST"
		}
		w.Header().Set("Allow", allow)
		writeError(w, r, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
		return
	}
	// Reject ambiguous inputs before doing any discovery or protocol loading.
	if r.Method == "POST" && r.URL.RawQuery != "" {
		writeError(w, r, 400, "INVALID_ARGUMENT", "POST accepts JSON body only; query parameters are not supported")
		return
	}
	if len(r.URL.RawQuery) > 16<<10 {
		writeError(w, r, 414, "INVALID_ARGUMENT", "Query exceeds 16 KiB")
		return
	}
	protocol, conn, err := e.protocol(r.Context(), s, svc, version)
	if err != nil {
		code := status.Code(err)
		if code == codes.Unknown {
			code = codes.Unavailable
		}
		slog.Warn("protocol load failed", "service", alias, "version", version, "error", err)
		writeError(w, r, httpStatus(code), codeName(code), publicMessage(code))
		return
	}
	method := protocol.Descriptor.Methods().ByName(protoreflect.Name(name))
	if method == nil {
		writeError(w, r, 404, "NOT_FOUND", "Method not found")
		return
	}
	if method.IsStreamingClient() || method.IsStreamingServer() {
		writeError(w, r, 400, "INVALID_ARGUMENT", "Only unary RPC methods are supported")
		return
	}
	timeout, _ := time.ParseDuration(svc.config.Timeout)
	h := &routeHandler{grpcMethod: "/" + svc.config.ProtoServices[version] + "/" + name,
		method: &descriptor.Method{Descriptor: method, Types: protocol.Types}, conn: conn, timeout: timeout}
	h.ServeHTTP(w, r)
}

// Each snapshot caches a complete protobuf service per version. Requests share a
// bounded background load; cancellation of one waiter cannot cancel other callers.
func (e *Engine) protocol(ctx context.Context, s *snapshot, svc *serviceRuntime, version string) (*descriptor.Service, *grpc.ClientConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, status.FromContextError(err).Err()
	}
	svc.mu.Lock()
	load := svc.protocols[version]
	if load == nil || (!load.expires.IsZero() && time.Now().After(load.expires)) {
		load = &protocolLoad{done: make(chan struct{})}
		svc.protocols[version] = load
		s.requests.Add(1) // Caller already holds a lease, so retirement cannot finish.
		go func() {
			defer s.requests.Done()
			loadCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			svc.mu.Lock()
			conn := svc.conn
			var err error
			if conn == nil {
				conn, err = grpc.NewClient("ledger:///"+svc.config.TargetService,
					grpc.WithResolvers(discovery.Builder{Source: e.source}),
					grpc.WithTransportCredentials(e.credentials.Clone()),
					grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
					grpc.WithDisableRetry(),
					grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(1<<20)))
				if err == nil {
					svc.conn = conn
				}
			}
			svc.mu.Unlock()

			filename := svc.config.DescriptorSets[version]
			if filename != "" && !filepath.IsAbs(filename) {
				filename = filepath.Join(e.descriptorDir, filename)
			}
			var protocol *descriptor.Service
			if err == nil {
				protocol, err = descriptor.LoadService(loadCtx, conn, svc.config.ProtoServices[version], filename)
			}
			svc.mu.Lock()
			load.service, load.err = protocol, err
			if err == nil {
				load.expires = time.Now().Add(time.Minute)
			}
			if err != nil {
				delete(svc.protocols, version)
			}
			close(load.done)
			svc.mu.Unlock()
		}()
	}
	svc.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, nil, status.FromContextError(ctx.Err()).Err()
	case <-load.done:
		svc.mu.Lock()
		conn := svc.conn
		svc.mu.Unlock()
		return load.service, conn, load.err
	}
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
	for _, svc := range s.services {
		if svc.conn != nil {
			_ = svc.conn.Close()
		}
	}
}

func (e *Engine) retire(s *snapshot) {
	s.requests.Wait()
	s.close()
	e.mu.Lock()
	delete(e.snapshots, s)
	close(s.done)
	e.mu.Unlock()
}

// Close stops accepting requests and waits for current and retired snapshots to drain.
func (e *Engine) Close() {
	e.reload <- struct{}{}
	defer func() { <-e.reload }()
	e.mu.Lock()
	e.closed = true
	current := e.current
	e.current = nil
	pending := make([]*snapshot, 0, len(e.snapshots))
	for s := range e.snapshots {
		pending = append(pending, s)
	}
	e.mu.Unlock()
	if current != nil {
		go e.retire(current)
	}
	for _, s := range pending {
		<-s.done
	}
}
