package gateway_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gateway "github.com/MARETU01/ledger-core/gateway/internal/gateway"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDescriptorFileRetryAndReload(t *testing.T) {
	dir := t.TempDir()
	e := gateway.New(staticSource{"backend": {backend(t, false)}}, insecure.NewCredentials(), dir)
	defer e.Close()
	cfg := services("file", "    descriptor_sets:\n      v1: service.pb\n")
	apply(t, e, cfg) // Missing files are checked on demand, so startup still succeeds.
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{}`), 503, "UNAVAILABLE")
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(fixture()), protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto)}}
	data, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(dir, "service.pb")
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{"value":"file"}`), 200, "file")
	if err := os.WriteFile(filename, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{"value":"cached"}`), 200, "cached")
	apply(t, e, services("refresh", "    descriptor_sets:\n      v1: service.pb\n"))
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{}`), 503, "UNAVAILABLE")
}

type gatedDiscovery struct {
	staticSource
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (s *gatedDiscovery) Watch(service string, update func([]string)) (func(), error) {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.staticSource.Watch(service, update)
}

func TestCanceledWaiterDoesNotCancelSharedProtocolLoad(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	source := &gatedDiscovery{staticSource: staticSource{"backend": {backend(t, true)}}, entered: make(chan struct{}), release: release}
	e := engine(t, source)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	apply(t, e, services("one", ""))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequest("GET", "/api/v1/test/Echo?value=first", nil).WithContext(ctx)
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { result := httptest.NewRecorder(); e.ServeHTTP(result, request); first <- result }()
	receive(t, source.entered, "protocol load")
	cancel()
	expect(t, receive(t, first, "canceled waiter"), 499, "CANCELED")
	second := callAsync(e, "Added", `{"value":"second"}`)
	once.Do(func() { close(release) })
	expect(t, receive(t, second, "shared load completion"), 200, "added:second")
}

func TestReloadDoesNotWaitForProtocolLoading(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	source := &gatedDiscovery{staticSource: staticSource{"backend": {backend(t, true)}}, entered: make(chan struct{}), release: release}
	e := engine(t, source)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	apply(t, e, services("first", ""))
	pending := callAsync(e, "Echo", `{"value":"old"}`)
	receive(t, source.entered, "old protocol load")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := e.Apply(ctx, services("second", "")); err != nil {
		t.Fatal(err)
	}
	if rev, ready := e.Ready(); !ready || rev != "second" {
		t.Fatal("reload blocked by load")
	}
	once.Do(func() { close(release) })
	expect(t, receive(t, pending, "old protocol completion"), 200, "old")
	expect(t, call(e, "POST", "/api/v1/test/Added", `{"value":"new"}`), 200, "added:new")
}
