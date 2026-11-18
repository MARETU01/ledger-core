package discovery

import (
	"sort"
	"sync"

	"google.golang.org/grpc/resolver"
)

// Watch must publish its initial view before returning. Cancel stops updates.
type Source interface {
	Watch(service string, update func([]string)) (cancel func(), err error)
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
