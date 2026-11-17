package discovery

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"

	"google.golang.org/grpc/resolver"
)

// Watch must publish its initial view before returning. Cancel stops updates.
type Source interface {
	Watch(service string, update func([]string)) (cancel func(), err error)
}

type Static map[string][]string

func (s Static) Watch(service string, update func([]string)) (func(), error) {
	addresses := s[service]
	if len(addresses) == 0 {
		return nil, fmt.Errorf("no static addresses for %s", service)
	}
	update(addresses)
	return func() {}, nil
}

// ParseStatic accepts service=host:port;service=host:port,host:port.
func ParseStatic(value string) (Static, error) {
	targets := Static{}
	for entry := range strings.SplitSeq(value, ";") {
		service, addresses, ok := strings.Cut(entry, "=")
		if !ok || service == "" || len(targets[service]) != 0 {
			return nil, fmt.Errorf("invalid static target %q", entry)
		}
		for address := range strings.SplitSeq(addresses, ",") {
			if _, _, err := net.SplitHostPort(address); err != nil {
				return nil, fmt.Errorf("invalid static address: %w", err)
			}
			targets[service] = append(targets[service], address)
		}
	}
	return targets, nil
}

type Builder struct{ Source Source }

func (b Builder) Scheme() string { return "ledger" }
func (b Builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	r := &watchResolver{}
	cancel, err := b.Source.Watch(target.Endpoint(), func(addresses []string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed {
			return
		}
		addresses = append([]string(nil), addresses...)
		sort.Strings(addresses)
		state := resolver.State{}
		for _, address := range addresses {
			state.Addresses = append(state.Addresses, resolver.Address{Addr: address})
		}
		// Publishing an empty view is intentional: removed endpoints must stop receiving calls.
		if err := cc.UpdateState(state); err != nil {
			cc.ReportError(err)
		}
	})
	if err != nil {
		return nil, err
	}
	r.cancel = cancel
	return r, nil
}

type watchResolver struct {
	mu     sync.Mutex
	closed bool
	cancel func()
}

func (*watchResolver) ResolveNow(resolver.ResolveNowOptions) {}
func (r *watchResolver) Close() {
	r.mu.Lock()
	r.closed = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
