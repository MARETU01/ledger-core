package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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
	_ "google.golang.org/protobuf/types/known/timestamppb"
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
		boolean := descriptorpb.FieldDescriptorProto_TYPE_BOOL
		repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED
		message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
		fd := &descriptorpb.FileDescriptorProto{Name: proto.String("dynamic_gateway_test.proto"), Package: proto.String("gateway.test.v1"), Syntax: proto.String("proto3"), Dependency: []string{"google/protobuf/timestamp.proto"},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("Nested"), Field: []*descriptorpb.FieldDescriptorProto{{Name: proto.String("label"), Number: proto.Int32(1), Label: &optional, Type: &str}, {Name: proto.String("display_name"), JsonName: proto.String("displayName"), Number: proto.Int32(2), Label: &optional, Type: &str}}},
				{Name: proto.String("Payload"), OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice")}}, Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("value"), Number: proto.Int32(1), Label: &optional, Type: &str},
					{Name: proto.String("count"), Number: proto.Int32(2), Label: &optional, Type: &integer},
					{Name: proto.String("tags"), Number: proto.Int32(3), Label: &repeated, Type: &str},
					{Name: proto.String("nested"), Number: proto.Int32(4), Label: &optional, Type: &message, TypeName: proto.String(".gateway.test.v1.Nested")},
					{Name: proto.String("created_at"), JsonName: proto.String("createdAt"), Number: proto.Int32(5), Label: &optional, Type: &message, TypeName: proto.String(".google.protobuf.Timestamp")},
					{Name: proto.String("account_id"), JsonName: proto.String("accountId"), Number: proto.Int32(6), Label: &optional, Type: &integer},
					{Name: proto.String("search_filter"), JsonName: proto.String("searchFilter"), Number: proto.Int32(10), Label: &optional, Type: &message, TypeName: proto.String(".gateway.test.v1.Nested")},
					{Name: proto.String("enabled"), Number: proto.Int32(9), Label: &optional, Type: &boolean},
					{Name: proto.String("first"), Number: proto.Int32(7), Label: &optional, Type: &str, OneofIndex: proto.Int32(0)},
					{Name: proto.String("second"), Number: proto.Int32(8), Label: &optional, Type: &str, OneofIndex: proto.Int32(0)},
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

func services(revision, options string) []byte {
	return []byte(fmt.Sprintf("version: 1\nrevision: %s\nservices:\n  test:\n    target_service: backend\n    proto_services:\n      v1: gateway.test.v1.Dynamic\n    get_methods:\n      v1: [Echo, Added, Slow, Blocked, Fail, Metadata, Stream, Missing]\n%s", revision, options))
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

func TestAutomaticRoutingAndQuery(t *testing.T) {
	e := engine(t, staticSource{"backend": {backend(t, true)}})
	apply(t, e, services("one", ""))
	result := call(e, "GET", "/api/v1/test/Echo?value=hello&count=9223372036854775807&tags=a&tags=b&nested.label=nested&account_id=123", "")
	expect(t, result, 200, `"count":"9223372036854775807"`)
	var response map[string]any
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["accountId"] != "123" || response["value"] != "hello" || response["nested"].(map[string]any)["label"] != "nested" {
		t.Fatalf("incorrect query mapping: %v", response)
	}
	tags := response["tags"].([]any)
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("incorrect repeated mapping: %v", tags)
	}
	expect(t, call(e, "GET", "/api/v1/test/Added?value=hello", ""), 200, "added:hello")
	expect(t, call(e, "GET", "/api/v1/test/Echo?enabled=true&first=", ""), 200, `"enabled":true`)
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{"createdAt":"2026-11-17T00:00:00Z","nested":{"label":"json"}}`), 200, "2026-11-17T00:00:00Z")
	for _, path := range []string{"/api/v1/test/Missing", "/api/v2/test/Echo", "/api/v1/unknown/Echo", "/api/v1/test/Echo/extra", "/api/v1/test/echo", "/api/v1/test/Echo/"} {
		expect(t, call(e, "POST", path, `{}`), 404, "NOT_FOUND")
	}
	expect(t, call(e, "POST", "/api/v1/test/Stream", `{}`), 400, "unary")
}

func TestQueryRequiresOriginalProtoFieldNames(t *testing.T) {
	e := engine(t, staticSource{"backend": {backend(t, true)}})
	apply(t, e, services("strict", ""))
	expect(t, call(e, "GET", "/api/v1/test/Echo?account_id=123&search_filter.display_name=Alice", ""), 200, `"displayName":"Alice"`)
	for _, query := range []string{
		"accountId=123", "Account_id=123", "searchFilter.display_name=Alice", "search_filter.displayName=Alice", "account_id=123&accountId=456",
	} {
		t.Run(query, func(t *testing.T) {
			expect(t, call(e, "GET", "/api/v1/test/Echo?"+query, ""), 400, "unknown input field")
		})
	}
	// POST keeps standard ProtoJSON inputs; responses still use JSON field names.
	for _, body := range []string{
		`{"accountId":"123","searchFilter":{"displayName":"Alice"}}`,
		`{"account_id":"123","search_filter":{"display_name":"Alice"}}`,
	} {
		result := call(e, "POST", "/api/v1/test/Echo", body)
		expect(t, result, 200, `"accountId":"123"`)
		expect(t, result, 200, `"displayName":"Alice"`)
	}
}

func TestQueryValidationAndHTTPMethods(t *testing.T) {
	e := engine(t, staticSource{"backend": {backend(t, true)}})
	apply(t, e, services("one", ""))
	for _, query := range []string{
		"count=9223372036854775808", "enabled=1", "count=1&count=2", "unknown=x", "value=%ZZ", "nested=x", "createdAt=2026-11-17", "first=a&second=b", "account_id=1&accountId=2", "value=a;count=1", strings.Repeat("value=x&", 128),
	} {
		expect(t, call(e, "GET", "/api/v1/test/Echo?"+query, ""), 400, "INVALID_ARGUMENT")
	}
	expect(t, call(e, "GET", "/api/v1/test/Echo?value="+strings.Repeat("x", 17<<10), ""), 414, "INVALID_ARGUMENT")
	expect(t, call(e, "GET", "/api/v1/test/Echo", `{}`), 400, "GET request body")
	expect(t, call(e, "POST", "/api/v1/test/Echo?value=query", `{"value":"body"}`), 400, "POST accepts JSON")
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{"unknown":1}`), 400, "INVALID_ARGUMENT")
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{"value":"`+strings.Repeat("x", 1<<20)+`"}`), 413, "RESOURCE_EXHAUSTED")
	request := httptest.NewRequest("POST", "/api/v1/test/Echo", strings.NewReader(`{}`))
	result := httptest.NewRecorder()
	e.ServeHTTP(result, request)
	expect(t, result, 415, "application/json")
	expect(t, call(e, "DELETE", "/api/v1/test/Echo", ""), 405, "METHOD_NOT_ALLOWED")
	apply(t, e, []byte(strings.ReplaceAll(string(services("post-only", "")), "v1: [Echo, Added, Slow, Blocked, Fail, Metadata, Stream, Missing]", "v1: []")))
	result = call(e, "GET", "/api/v1/test/Echo", "")
	expect(t, result, 405, "METHOD_NOT_ALLOWED")
	if result.Header().Get("Allow") != "POST" {
		t.Fatal("incorrect Allow header")
	}
	expect(t, call(e, "POST", "/api/v1/test/Added", `{"value":"new"}`), 200, "added:new")
}

func TestErrorsDeadlineAndMetadata(t *testing.T) {
	e := engine(t, staticSource{"backend": {backend(t, true)}})
	apply(t, e, services("one", "    timeout: 20ms\n"))
	expect(t, call(e, "POST", "/api/v1/test/Slow", `{}`), 504, "DEADLINE_EXCEEDED")
	result := call(e, "POST", "/api/v1/test/Fail", `{}`)
	expect(t, result, 400, "INVALID_ARGUMENT")
	if strings.Contains(result.Body.String(), "private") {
		t.Fatal("upstream error exposed")
	}
	request := httptest.NewRequest("POST", "/api/v1/test/Metadata", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "request-123")
	request.Header.Set("X-User-ID", "forged-user")
	result = httptest.NewRecorder()
	gateway.Middleware(e, "secret").ServeHTTP(result, request)
	expect(t, result, 401, "UNAUTHENTICATED")
	request.Header.Set("Authorization", "Bearer secret")
	result = httptest.NewRecorder()
	gateway.Middleware(e, "secret").ServeHTTP(result, request)
	expect(t, result, 200, "request-123")
}

func TestLazyStartupFailureRetryAndDiscoveryRecovery(t *testing.T) {
	source := &mutableDiscovery{}
	e := engine(t, source)
	apply(t, e, services("offline", ""))
	if listenerCount(source) != 0 {
		t.Fatal("startup accessed discovery")
	}
	if rev, ready := e.Ready(); !ready || rev != "offline" {
		t.Fatal("offline startup not ready")
	}
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{}`), 503, "UNAVAILABLE")
	source.set(backend(t, true))
	eventually(t, func() bool { return call(e, "POST", "/api/v1/test/Echo", `{}`).Code == 200 }, "backend recovery without republishing")
	source.set()
	expect(t, call(e, "POST", "/api/v1/test/Echo", `{}`), 503, "UNAVAILABLE")
}

func TestConcurrentColdRequests(t *testing.T) {
	source := &mutableDiscovery{addresses: []string{backend(t, true)}}
	e := engine(t, source)
	apply(t, e, services("one", ""))
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			result := call(e, "GET", "/api/v1/test/Echo?value=hello", "")
			if result.Code != 200 {
				t.Errorf("cold request failed: %d %s", result.Code, result.Body)
			}
		})
	}
	wg.Wait()
	if listenerCount(source) != 1 {
		t.Fatal("concurrent requests duplicated connections")
	}
}
