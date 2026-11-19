package gateway_test

import (
	"context"
	"encoding/json"
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

func applyAsync(e *gateway.Engine, ctx context.Context, data []byte) <-chan error {
	result := make(chan error, 1)
	go func() { result <- e.Apply(ctx, data) }()
	return result
}

func callAsync(e *gateway.Engine, body string) <-chan *httptest.ResponseRecorder {
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- call(e, "POST", "/api/v1/echo", body) }()
	return result
}

func expectValue(t *testing.T, response *httptest.ResponseRecorder, value string) {
	t.Helper()
	expect(t, response, 200, "")
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["value"] != value {
		t.Fatalf("got response %s, want value %q", response.Body, value)
	}
}

func TestReloadKeepsInflightRPCAndReleasesRetiredConnection(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	addr := backendWithGate(t, true, entered, release)
	source := &mutableDiscovery{addresses: []string{addr}}
	e := engine(t, source)
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	apply(t, e, routes("first", "Blocked", "/api/v1/echo", "    timeout: 10s\n"))
	old := callAsync(e, `{"value":"old"}`)
	receive(t, entered, "old RPC to enter backend")

	if err := receive(t, applyAsync(e, t.Context(), routes("second", "Added", "/api/v1/echo", "")), "route switch with RPC blocked"); err != nil {
		t.Fatal(err)
	}
	if revision, ready := e.Ready(); !ready || revision != "second" {
		t.Fatalf("active revision = %q, ready = %v", revision, ready)
	}
	expect(t, receive(t, callAsync(e, `{"value":"new"}`), "new request"), 200, "added:new")
	if count := listenerCount(source); count != 2 {
		t.Fatalf("got %d subscriptions, want active and draining snapshots", count)
	}
	select {
	case response := <-old:
		t.Fatalf("old RPC ended before release: %d %s", response.Code, response.Body)
	default:
	}
	unblock.Do(func() { close(release) })
	expectValue(t, receive(t, old, "old RPC completion"), "old")
	eventually(t, func() bool { return listenerCount(source) == 1 }, "retired connection unsubscribe")
	e.Close()
	if count := listenerCount(source); count != 0 {
		t.Fatalf("subscriptions leaked after close: %d", count)
	}
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

func TestReloadDoesNotWaitForRequestBody(t *testing.T) {
	addr := backend(t, true)
	e := engine(t, staticSource{"backend": {addr}})
	release := make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	apply(t, e, routes("first", "Echo", "/api/v1/echo", ""))
	body := &gatedBody{reader: strings.NewReader(`{"value":"old"}`), entered: make(chan struct{}), release: release}
	request := httptest.NewRequest("POST", "/api/v1/echo", body)
	request.Header.Set("Content-Type", "application/json")
	old := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result := httptest.NewRecorder()
		e.ServeHTTP(result, request)
		old <- result
	}()
	receive(t, body.entered, "request body read")
	if err := receive(t, applyAsync(e, t.Context(), routes("second", "Added", "/api/v1/echo", "")), "route switch with body blocked"); err != nil {
		t.Fatal(err)
	}
	expect(t, receive(t, callAsync(e, `{"value":"new"}`), "new request with old body blocked"), 200, "added:new")
	unblock.Do(func() { close(release) })
	expectValue(t, receive(t, old, "old body request completion"), "old")
}

func TestCloseDrainsCurrentAndRetiredSnapshots(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	addr := backendWithGate(t, true, entered, release)
	source := &mutableDiscovery{addresses: []string{addr}}
	e := engine(t, source)
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	apply(t, e, routes("first", "Blocked", "/api/v1/echo", "    timeout: 10s\n"))
	old := callAsync(e, `{"value":"old"}`)
	receive(t, entered, "old RPC")
	if err := receive(t, applyAsync(e, t.Context(), routes("second", "Blocked", "/api/v1/echo", "    timeout: 10s\n")), "route switch"); err != nil {
		t.Fatal(err)
	}
	current := callAsync(e, `{"value":"current"}`)
	receive(t, entered, "current RPC")
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	eventually(t, func() bool { _, ready := e.Ready(); return !ready }, "close to stop accepting requests")
	expect(t, call(e, "POST", "/api/v1/echo", `{}`), 503, "UNAVAILABLE")
	select {
	case <-closed:
		t.Fatal("close returned before inflight requests drained")
	default:
	}
	unblock.Do(func() { close(release) })
	expectValue(t, receive(t, old, "retired RPC during close"), "old")
	expectValue(t, receive(t, current, "current RPC during close"), "current")
	receive(t, closed, "close completion")
	if count := listenerCount(source); count != 0 {
		t.Fatalf("subscriptions leaked: %d", count)
	}
	if err := e.Apply(t.Context(), routes("after-close", "Echo", "/api/v1/echo", "")); err == nil {
		t.Fatal("closed engine accepted routes")
	}
	e.Close()
}

type gatedDiscovery struct {
	staticSource
	entered chan struct{}
	release <-chan struct{}
}

func TestCloseWaitsForRetiredSnapshotAfterCurrentIsIdle(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	addr := backendWithGate(t, true, entered, release)
	source := &mutableDiscovery{addresses: []string{addr}}
	e := engine(t, source)
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	apply(t, e, routes("first", "Blocked", "/api/v1/echo", "    timeout: 10s\n"))
	old := callAsync(e, `{"value":"old"}`)
	receive(t, entered, "old RPC")
	if err := receive(t, applyAsync(e, t.Context(), routes("second", "Echo", "/api/v1/echo", "")), "route switch"); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	eventually(t, func() bool { return listenerCount(source) == 1 }, "idle current connection to close")
	select {
	case <-closed:
		t.Fatal("close forgot a retired snapshot with an inflight RPC")
	default:
	}
	unblock.Do(func() { close(release) })
	expectValue(t, receive(t, old, "retired RPC completion"), "old")
	receive(t, closed, "retired snapshot cleanup")
	if count := listenerCount(source); count != 0 {
		t.Fatalf("subscriptions leaked: %d", count)
	}
}

func (s *gatedDiscovery) Watch(service string, update func([]string)) (func(), error) {
	if service == "paused" {
		close(s.entered)
		<-s.release
	}
	return s.staticSource.Watch(service, update)
}

func TestCanceledReloadDoesNotWaitForAnotherBuilder(t *testing.T) {
	addr := backend(t, true)
	release := make(chan struct{})
	source := &gatedDiscovery{staticSource: staticSource{"backend": {addr}, "paused": {addr}}, entered: make(chan struct{}), release: release}
	e := engine(t, source)
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	apply(t, e, routes("first", "Echo", "/api/v1/echo", ""))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	building := applyAsync(e, ctx, []byte(strings.ReplaceAll(string(routes("second", "Added", "/api/v1/echo", "")), "target_service: backend", "target_service: paused")))
	receive(t, source.entered, "first builder to pause")
	canceledCtx, cancelWaiting := context.WithCancel(t.Context())
	waiting := applyAsync(e, canceledCtx, routes("canceled", "Echo", "/api/v1/echo", ""))
	cancelWaiting()
	if err := receive(t, waiting, "canceled queued reload"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
	if revision, ready := e.Ready(); !ready || revision != "first" {
		t.Fatalf("unexpected active revision: %q", revision)
	}
	expect(t, receive(t, callAsync(e, `{"value":"available"}`), "request during build"), 200, "available")
	unblock.Do(func() { close(release) })
	if err := receive(t, building, "first builder completion"); err != nil {
		t.Fatal(err)
	}
	if revision, ready := e.Ready(); !ready || revision != "second" {
		t.Fatalf("unexpected active revision after build: %q", revision)
	}
}
