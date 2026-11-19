package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/discovery"
	gateway "github.com/MARETU01/ledger-core/gateway/internal/gateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// staticSource supplies local test backends without requiring Nacos.
type staticSource map[string][]string

func (s staticSource) Watch(service string, update func([]string)) (func(), error) {
	addresses := s[service]
	if len(addresses) == 0 {
		return nil, fmt.Errorf("no test addresses for %s", service)
	}
	update(addresses)
	return func() {}, nil
}

var fixtureOnce sync.Once
var fixtureFile protoreflect.FileDescriptor

// No generated business protobuf or client code is used, even by the test backend.
func fixture() protoreflect.FileDescriptor {
	fixtureOnce.Do(func() {
		optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		str := descriptorpb.FieldDescriptorProto_TYPE_STRING
		integer := descriptorpb.FieldDescriptorProto_TYPE_INT64
		repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED
		message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
		fd := &descriptorpb.FileDescriptorProto{Name: proto.String("dynamic_gateway_test.proto"), Package: proto.String("gateway.test.v1"), Syntax: proto.String("proto3"), Dependency: []string{"google/protobuf/timestamp.proto"},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("Nested"), Field: []*descriptorpb.FieldDescriptorProto{{Name: proto.String("label"), Number: proto.Int32(1), Label: &optional, Type: &str}}},
				{Name: proto.String("Payload"), Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("value"), Number: proto.Int32(1), Label: &optional, Type: &str},
					{Name: proto.String("count"), Number: proto.Int32(2), Label: &optional, Type: &integer},
					{Name: proto.String("tags"), Number: proto.Int32(3), Label: &repeated, Type: &str},
					{Name: proto.String("nested"), Number: proto.Int32(4), Label: &optional, Type: &message, TypeName: proto.String(".gateway.test.v1.Nested")},
					{Name: proto.String("created_at"), JsonName: proto.String("createdAt"), Number: proto.Int32(5), Label: &optional, Type: &message, TypeName: proto.String(".google.protobuf.Timestamp")},
				}},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Dynamic"), Method: []*descriptorpb.MethodDescriptorProto{}}},
		}
		for _, name := range []string{"Echo", "Added", "Slow", "Blocked", "Fail", "Metadata", "Stream"} {
			fd.Service[0].Method = append(fd.Service[0].Method, &descriptorpb.MethodDescriptorProto{Name: proto.String(name), InputType: proto.String(".gateway.test.v1.Payload"), OutputType: proto.String(".gateway.test.v1.Payload"), ServerStreaming: proto.Bool(name == "Stream")})
		}
		var err error
		fixtureFile, err = protodesc.NewFile(fd, protoregistry.GlobalFiles)
		if err != nil {
			panic(err)
		}
		if err := protoregistry.GlobalFiles.RegisterFile(fixtureFile); err != nil {
			panic(err)
		}
	})
	return fixtureFile
}

func backend(t *testing.T, reflected bool) string {
	t.Helper()
	return backendWithGate(t, reflected, nil, nil)
}

func backendWithGate(t *testing.T, reflected bool, entered chan<- struct{}, release <-chan struct{}) string {
	t.Helper()
	file := fixture()
	input := file.Messages().ByName("Payload")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	service := grpc.ServiceDesc{ServiceName: "gateway.test.v1.Dynamic", HandlerType: (*interface{})(nil), Metadata: "dynamic_gateway_test.proto"}
	for _, name := range []string{"Echo", "Added", "Slow", "Blocked", "Fail", "Metadata"} {
		service.Methods = append(service.Methods, grpc.MethodDesc{MethodName: name, Handler: func(_ any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			message := dynamicpb.NewMessage(input)
			if err := decode(message); err != nil {
				return nil, err
			}
			value := input.Fields().ByName("value")
			switch name {
			case "Added":
				message.Set(value, protoreflect.ValueOfString("added:"+message.Get(value).String()))
			case "Slow":
				select {
				case <-ctx.Done():
					return nil, status.FromContextError(ctx.Err()).Err()
				case <-time.After(150 * time.Millisecond):
				}
			case "Blocked":
				if entered != nil {
					entered <- struct{}{}
					select {
					case <-ctx.Done():
						return nil, status.FromContextError(ctx.Err()).Err()
					case <-release:
					}
				}
			case "Fail":
				return nil, status.Error(codes.InvalidArgument, "private upstream error detail")
			case "Metadata":
				md, _ := metadata.FromIncomingContext(ctx)
				if len(md.Get("x-user-id")) > 0 {
					return nil, status.Error(codes.Internal, "untrusted identity forwarded")
				}
				message.Set(value, protoreflect.ValueOfString(strings.Join(md.Get("x-request-id"), ",")))
			}
			return message, nil
		}})
	}
	server.RegisterService(&service, struct{}{})
	if reflected {
		reflection.Register(server)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	return listener.Addr().String()
}

func routes(revision, method, path, options string) []byte {
	return []byte(fmt.Sprintf("version: 1\nrevision: %s\nroutes:\n  - id: test\n    http_method: POST\n    http_path: %s\n    target_service: backend\n    grpc_method: /gateway.test.v1.Dynamic/%s\n%s", revision, path, method, options))
}

func engine(t *testing.T, source discovery.Source) *gateway.Engine {
	t.Helper()
	e := gateway.New(source, insecure.NewCredentials(), t.TempDir())
	t.Cleanup(e.Close)
	return e
}
func apply(t *testing.T, e *gateway.Engine, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Apply(ctx, data); err != nil {
		t.Fatal(err)
	}
}
func call(e http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	result := httptest.NewRecorder()
	gateway.Middleware(e, "").ServeHTTP(result, req)
	return result
}
func expect(t *testing.T, result *httptest.ResponseRecorder, code int, fragment string) {
	t.Helper()
	if result.Code != code || !strings.Contains(result.Body.String(), fragment) {
		t.Fatalf("got %d %s, want %d containing %q", result.Code, result.Body.String(), code, fragment)
	}
}

func TestReflectionDynamicReloadAndRejectedUpdate(t *testing.T) {
	addr := backend(t, true)
	e := engine(t, staticSource{"backend": {addr}})
	apply(t, e, routes("one", "Echo", "/api/v1/echo", ""))
	expect(t, call(e, "POST", "/api/v1/echo", `{"value":"hello","createdAt":"2026-11-17T00:00:00Z"}`), 200, `2026-11-17T00:00:00Z`)
	apply(t, e, routes("two", "Added", "/api/v1/new-method", ""))
	expect(t, call(e, "POST", "/api/v1/new-method", `{"value":"hello"}`), 200, `added:hello`)
	expect(t, call(e, "POST", "/api/v1/echo", `{}`), 404, "NOT_FOUND")
	for _, bad := range [][]byte{
		routes("bad", "DoesNotExist", "/api/v1/new-method", ""),
		routes("bad", "Stream", "/api/v1/new-method", ""),
		routes("bad", "Echo", "/api/v1/new-method", "    query_fields:\n      value: nonexistent\n"),
		[]byte("version: 1\nrevision: bad\nunknown_key: true\nroutes: []\n"),
	} {
		if err := e.Apply(context.Background(), bad); err == nil {
			t.Fatal("invalid configuration accepted")
		}
		if revision, ready := e.Ready(); !ready || revision != "two" {
			t.Fatalf("lost active revision: %s", revision)
		}
		expect(t, call(e, "POST", "/api/v1/new-method", `{"value":"still-working"}`), 200, "added:still-working")
	}
}

func TestBindingsValidationAndBodyLimits(t *testing.T) {
	addr := backend(t, true)
	e := engine(t, staticSource{"backend": {addr}})
	cfg := routes("bindings", "Echo", "/api/v1/items/{id}", "    path_fields:\n      id: value\n    query_fields:\n      count: count\n      tag: tags\n      label: nested.label\n")
	apply(t, e, cfg)
	result := call(e, "POST", "/api/v1/items/path-value?count=9223372036854775807&tag=a&tag=b&label=nested", `{"value":"body-value"}`)
	expect(t, result, 200, "path-value")
	var data map[string]any
	if err := json.Unmarshal(result.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["count"] != "9223372036854775807" || len(data["tags"].([]any)) != 2 {
		t.Fatalf("bad typed mapping: %v", data)
	}
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/api/v1/items/a?count=9223372036854775808", `{}`, 400},
		{"/api/v1/items/a?count=1&count=2", `{}`, 400},
		{"/api/v1/items/a?unmapped=1", `{}`, 400},
		{"/api/v1/items/a?count=%ZZ", `{}`, 400},
		{"/api/v1/items/a", `{"unknown":1}`, 400},
		{"/api/v1/items/a", `{"value":"` + strings.Repeat("x", 1<<20) + `"}`, 413},
	} {
		expect(t, call(e, "POST", tc.path, tc.body), tc.code, "requestId")
	}
	expect(t, call(e, "GET", "/api/v1/items/a", ""), 405, "METHOD_NOT_ALLOWED")
	cfg = bytes.ReplaceAll(cfg, []byte("http_method: POST"), []byte("http_method: GET"))
	apply(t, e, cfg)
	expect(t, call(e, "GET", "/api/v1/items/get-value?count=12", ""), 200, "get-value")
	expect(t, call(e, "GET", "/api/v1/items/a", `{}`), 400, "GET request body")
}

func TestErrorsDeadlineAndMetadata(t *testing.T) {
	addr := backend(t, true)
	e := engine(t, staticSource{"backend": {addr}})
	apply(t, e, routes("deadline", "Slow", "/api/v1/echo", "    timeout: 20ms\n"))
	expect(t, call(e, "POST", "/api/v1/echo", `{}`), 504, "DEADLINE_EXCEEDED")
	apply(t, e, routes("error", "Fail", "/api/v1/echo", ""))
	result := call(e, "POST", "/api/v1/echo", `{}`)
	expect(t, result, 400, "INVALID_ARGUMENT")
	if strings.Contains(result.Body.String(), "private") {
		t.Fatal("upstream private error exposed")
	}
	apply(t, e, routes("metadata", "Metadata", "/api/v1/echo", ""))
	request := httptest.NewRequest("POST", "/api/v1/echo", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "request-123")
	request.Header.Set("X-User-ID", "forged-user")
	result = httptest.NewRecorder()
	gateway.Middleware(e, "").ServeHTTP(result, request)
	expect(t, result, 200, "request-123")
	result = httptest.NewRecorder()
	gateway.Middleware(e, "secret").ServeHTTP(result, request)
	expect(t, result, 401, "UNAUTHENTICATED")
	request.Header.Set("Authorization", "Bearer secret")
	result = httptest.NewRecorder()
	gateway.Middleware(e, "secret").ServeHTTP(result, request)
	expect(t, result, 200, "request-123")
}

func TestDescriptorFileWithoutReflection(t *testing.T) {
	addr := backend(t, false)
	dir := t.TempDir()
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(fixture()), protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto)}}
	data, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service.pb"), data, 0600); err != nil {
		t.Fatal(err)
	}
	e := gateway.New(staticSource{"backend": {addr}}, insecure.NewCredentials(), dir)
	defer e.Close()
	apply(t, e, routes("offline", "Echo", "/api/v1/echo", "    descriptor_set: service.pb\n"))
	expect(t, call(e, "POST", "/api/v1/echo", `{"value":"offline"}`), 200, "offline")
}

type mutableDiscovery struct {
	mu        sync.Mutex
	addresses []string
	listeners map[int]func([]string)
	next      int
}

func (s *mutableDiscovery) Watch(_ string, update func([]string)) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listeners == nil {
		s.listeners = map[int]func([]string){}
	}
	s.next++
	id := s.next
	s.listeners[id] = update
	update(s.addresses)
	return func() { s.mu.Lock(); delete(s.listeners, id); s.mu.Unlock() }, nil
}
func (s *mutableDiscovery) set(addresses ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addresses = addresses
	for _, update := range s.listeners {
		update(addresses)
	}
}

func TestDiscoveryRemovalAndRecovery(t *testing.T) {
	addr := backend(t, true)
	source := &mutableDiscovery{addresses: []string{addr}}
	e := engine(t, source)
	apply(t, e, routes("discovery", "Echo", "/api/v1/echo", ""))
	expect(t, call(e, "POST", "/api/v1/echo", `{}`), 200, "{}")
	source.set()
	expect(t, call(e, "POST", "/api/v1/echo", `{}`), 503, "UNAVAILABLE")
	source.set(addr)
	deadline := time.Now().Add(3 * time.Second)
	for {
		result := call(e, "POST", "/api/v1/echo", `{}`)
		if result.Code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery did not recover: %s", result.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReloadWhileServing(t *testing.T) {
	addr := backend(t, true)
	e := engine(t, staticSource{"backend": {addr}})
	apply(t, e, routes("first", "Echo", "/api/v1/echo", ""))
	server := httptest.NewServer(gateway.Middleware(e, ""))
	defer server.Close()
	var requests sync.WaitGroup
	for range 4 {
		requests.Go(func() {
			for range 12 {
				response, err := http.Post(server.URL+"/api/v1/echo", "application/json", strings.NewReader(`{"value":"test"}`))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if response.StatusCode != 200 {
					t.Errorf("reload disrupted call: %d", response.StatusCode)
				}
			}
		})
	}
	apply(t, e, routes("second", "Added", "/api/v1/echo", ""))
	requests.Wait()
	expect(t, call(e, "POST", "/api/v1/echo", `{"value":"new"}`), 200, "added:new")
}
