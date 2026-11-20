package gateway_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gateway "github.com/MARETU01/ledger-core/gateway/internal/gateway"
)

func receive[T any](t *testing.T, results <-chan T, operation string) T {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
		var zero T
		return zero
	}
}
func eventually(t *testing.T, condition func() bool, operation string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", operation)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func listenerCount(source *mutableDiscovery) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.listeners)
}
func callAsync(e *gateway.Engine, method, body string) <-chan *httptest.ResponseRecorder {
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- call(e, "POST", "/api/v1/test/"+method, body) }()
	return result
}

func TestReloadAndCloseDrainRequests(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	source := &mutableDiscovery{addresses: []string{backendWithGate(t, true, entered, release)}}
	e := engine(t, source)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	apply(t, e, services("first", "    timeout: 10s\n"))
	old := callAsync(e, "Blocked", `{"value":"old"}`)
	receive(t, entered, "old RPC")
	apply(t, e, services("second", "    timeout: 10s\n"))
	expect(t, call(e, "POST", "/api/v1/test/Added", `{"value":"new"}`), 200, "added:new")
	if listenerCount(source) != 2 {
		t.Fatal("old snapshot closed during inflight call")
	}
	current := callAsync(e, "Blocked", `{"value":"current"}`)
	receive(t, entered, "current RPC")
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	eventually(t, func() bool { _, ready := e.Ready(); return !ready }, "stop accepting")
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{}`), 503, "UNAVAILABLE")
	select {
	case <-closed:
		t.Fatal("close returned before draining")
	default:
	}
	once.Do(func() { close(release) })
	expect(t, receive(t, old, "old completion"), 200, "old")
	expect(t, receive(t, current, "current completion"), 200, "current")
	receive(t, closed, "close completion")
	if listenerCount(source) != 0 {
		t.Fatal("subscriptions leaked")
	}
	if err := e.Apply(t.Context(), services("after-close", "")); err == nil {
		t.Fatal("closed engine accepted config")
	}
	e.Close()
}

func TestInvalidAndCanceledUpdatesKeepSnapshot(t *testing.T) {
	e := engine(t, staticSource{"backend": {backend(t, true)}})
	apply(t, e, services("first", ""))
	for _, bad := range [][]byte{
		[]byte("version: 1\nrevision: old\nroutes: []"),
		[]byte(strings.ReplaceAll(string(services("bad", "")), "v1: gateway.test.v1.Dynamic", "v1: invalid/service")),
		services("bad", "    timeout: 0s\n"),
	} {
		if err := e.Apply(t.Context(), bad); err == nil {
			t.Fatal("invalid update accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := e.Apply(ctx, services("canceled", "")); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if rev, ready := e.Ready(); !ready || rev != "first" {
		t.Fatal("lost original snapshot")
	}
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{"value":"still-working"}`), 200, "still-working")
}

type gatedBody struct {
	reader  io.Reader
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (b *gatedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.reader.Read(p)
}

func TestReloadDoesNotWaitForBody(t *testing.T) {
	source := &mutableDiscovery{addresses: []string{backend(t, true)}}
	e := engine(t, source)
	apply(t, e, services("first", ""))
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	body := &gatedBody{reader: strings.NewReader(`{"value":"old"}`), entered: make(chan struct{}), release: release}
	request := httptest.NewRequest("POST", "/api/v1/test/Echo", body)
	request.Header.Set("Content-Type", "application/json")
	old := make(chan *httptest.ResponseRecorder, 1)
	go func() { result := httptest.NewRecorder(); e.ServeHTTP(result, request); old <- result }()
	receive(t, body.entered, "body read")
	apply(t, e, services("second", ""))
	expect(t, call(e, "POST", "/api/v1/test/Added", `{"value":"new"}`), 200, "added:new")
	once.Do(func() { close(release) })
	expect(t, receive(t, old, "old body completion"), 200, "old")
	eventually(t, func() bool { return listenerCount(source) == 1 }, "retired unsubscribe")
}

func TestConcurrentReloadAndRequests(t *testing.T) {
	e := engine(t, staticSource{"backend": {backend(t, true)}})
	apply(t, e, services("first", ""))
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 12 {
				result := call(e, "POST", "/api/v1/test/Echo", `{"value":"concurrent"}`)
				if result.Code != 200 {
					t.Errorf("reload interrupted call: %d %s", result.Code, result.Body)
				}
			}
		})
	}
	apply(t, e, services("second", ""))
	wg.Wait()
}
