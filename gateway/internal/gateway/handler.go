package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/MARETU01/ledger-core/gateway/internal/descriptor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type fieldBinding struct {
	source string
	fields []protoreflect.FieldDescriptor
}
type routeHandler struct {
	route          config.Route
	method         *descriptor.Method
	conn           grpc.ClientConnInterface
	timeout        time.Duration
	paths, queries []fieldBinding
}

var pathVariable = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func newRouteHandler(route config.Route, method *descriptor.Method, conn grpc.ClientConnInterface, timeout time.Duration) (http.Handler, error) {
	h := &routeHandler{route: route, method: method, conn: conn, timeout: timeout}
	variables := map[string]bool{}
	for _, match := range pathVariable.FindAllStringSubmatch(route.HTTPPath, -1) {
		variables[match[1]] = true
	}
	if strings.ContainsAny(pathVariable.ReplaceAllString(route.HTTPPath, ""), "{}") {
		return nil, fmt.Errorf("only single-segment {name} path variables are supported")
	}
	used := map[string]bool{}
	bind := func(source, target string) (fieldBinding, error) {
		if source == "" || target == "" {
			return fieldBinding{}, fmt.Errorf("empty parameter binding")
		}
		fields := []protoreflect.FieldDescriptor{}
		message := method.Descriptor.Input()
		parts := strings.Split(target, ".")
		canonical := []string{}
		for i, part := range parts {
			field := message.Fields().ByName(protoreflect.Name(part))
			if field == nil {
				field = message.Fields().ByJSONName(part)
			}
			if field == nil {
				return fieldBinding{}, fmt.Errorf("unknown input field %s", target)
			}
			canonical = append(canonical, string(field.Name()))
			fields = append(fields, field)
			if i < len(parts)-1 {
				if field.Message() == nil || field.IsMap() || field.IsList() {
					return fieldBinding{}, fmt.Errorf("invalid nested field %s", target)
				}
				message = field.Message()
			} else if field.Message() != nil || field.IsMap() {
				return fieldBinding{}, fmt.Errorf("parameter target %s must be scalar or repeated scalar", target)
			}
		}
		key := strings.Join(canonical, ".")
		if used[key] {
			return fieldBinding{}, fmt.Errorf("duplicate target field %s", target)
		}
		used[key] = true
		return fieldBinding{source: source, fields: fields}, nil
	}
	for source, target := range route.PathFields {
		if !variables[source] {
			return nil, fmt.Errorf("path binding %s has no matching placeholder", source)
		}
		b, err := bind(source, target)
		if err != nil {
			return nil, err
		}
		if b.fields[len(b.fields)-1].IsList() {
			return nil, fmt.Errorf("path fields cannot be repeated")
		}
		h.paths = append(h.paths, b)
		delete(variables, source)
	}
	if len(variables) > 0 {
		return nil, fmt.Errorf("every path placeholder requires a path_fields binding")
	}
	for source, target := range route.QueryFields {
		b, err := bind(source, target)
		if err != nil {
			return nil, err
		}
		h.queries = append(h.queries, b)
	}
	return h, nil
}

func (h *routeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	request := dynamicpb.NewMessage(h.method.Descriptor.Input())
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			writeError(w, r, 413, "RESOURCE_EXHAUSTED", "Request body exceeds 1 MiB")
		} else {
			writeError(w, r, 400, "INVALID_ARGUMENT", "Cannot read request body")
		}
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if r.Method == http.MethodGet {
			writeError(w, r, 400, "INVALID_ARGUMENT", "GET request body is not supported")
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			writeError(w, r, 415, "INVALID_ARGUMENT", "Expected application/json")
			return
		}
		if err := (protojson.UnmarshalOptions{Resolver: h.method.Types}).Unmarshal(body, request); err != nil {
			writeError(w, r, 400, "INVALID_ARGUMENT", "Invalid JSON request: "+err.Error())
			return
		}
	}
	query, err := parseQuery(r)
	if err != nil {
		writeError(w, r, 400, "INVALID_ARGUMENT", err.Error())
		return
	}
	for _, binding := range h.queries {
		if values, ok := query[binding.source]; ok {
			if err := setField(request, binding.fields, values); err != nil {
				writeError(w, r, 400, "INVALID_ARGUMENT", binding.source+": "+err.Error())
				return
			}
			delete(query, binding.source)
		}
	}
	if len(query) > 0 {
		writeError(w, r, 400, "INVALID_ARGUMENT", "Unmapped query parameter")
		return
	}
	for _, binding := range h.paths {
		if err := setField(request, binding.fields, []string{r.PathValue(binding.source)}); err != nil {
			writeError(w, r, 400, "INVALID_ARGUMENT", binding.source+": "+err.Error())
			return
		}
	}
	response := dynamicpb.NewMessage(h.method.Descriptor.Output())
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	pairs := []string{"x-request-id", w.Header().Get("X-Request-ID")}
	for _, name := range []string{"authorization", "traceparent", "tracestate"} {
		if value := r.Header.Get(name); value != "" {
			pairs = append(pairs, name, value)
		}
	}
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(pairs...))
	if err := h.conn.Invoke(ctx, h.route.GRPCMethod, request, response); err != nil {
		code := status.Code(err)
		if errors.Is(err, context.DeadlineExceeded) {
			code = codes.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			code = codes.Canceled
		}
		slog.Warn("gRPC invocation failed", "route", h.route.ID, "request_id", w.Header().Get("X-Request-ID"), "code", code.String(), "error", err)
		writeError(w, r, httpStatus(code), codeName(code), publicMessage(code))
		return
	}
	data, err := (protojson.MarshalOptions{Resolver: h.method.Types}).Marshal(response)
	if err != nil {
		writeError(w, r, 500, "INTERNAL", "Response encoding failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func parseQuery(r *http.Request) (map[string][]string, error) {
	return url.ParseQuery(r.URL.RawQuery)
}

func setField(message protoreflect.Message, fields []protoreflect.FieldDescriptor, values []string) error {
	field := fields[0]
	if len(fields) > 1 {
		return setField(message.Mutable(field).Message(), fields[1:], values)
	}
	if !field.IsList() && len(values) != 1 {
		return fmt.Errorf("expected exactly one value")
	}
	converted := make([]protoreflect.Value, 0, len(values))
	for _, raw := range values {
		value, err := scalar(field, raw)
		if err != nil {
			return err
		}
		converted = append(converted, value)
	}
	if field.IsList() {
		list := message.Mutable(field).List()
		list.Truncate(0)
		for _, value := range converted {
			list.Append(value)
		}
	} else {
		message.Set(field, converted[0])
	}
	return nil
}

func scalar(field protoreflect.FieldDescriptor, raw string) (protoreflect.Value, error) {
	switch field.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(raw), nil
	case protoreflect.BytesKind:
		b, err := base64.StdEncoding.DecodeString(raw)
		return protoreflect.ValueOfBytes(b), err
	case protoreflect.BoolKind:
		if raw != "true" && raw != "false" {
			return protoreflect.Value{}, fmt.Errorf("expected true or false")
		}
		return protoreflect.ValueOfBool(raw == "true"), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(raw, 10, 32)
		return protoreflect.ValueOfInt32(int32(n)), err
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(raw, 10, 64)
		return protoreflect.ValueOfInt64(n), err
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(raw, 10, 32)
		return protoreflect.ValueOfUint32(uint32(n)), err
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(raw, 10, 64)
		return protoreflect.ValueOfUint64(n), err
	case protoreflect.FloatKind:
		n, err := strconv.ParseFloat(raw, 32)
		return protoreflect.ValueOfFloat32(float32(n)), err
	case protoreflect.DoubleKind:
		n, err := strconv.ParseFloat(raw, 64)
		return protoreflect.ValueOfFloat64(n), err
	case protoreflect.EnumKind:
		value := field.Enum().Values().ByName(protoreflect.Name(raw))
		if value == nil {
			n, err := strconv.ParseInt(raw, 10, 32)
			if err == nil {
				value = field.Enum().Values().ByNumber(protoreflect.EnumNumber(n))
			}
		}
		if value == nil {
			return protoreflect.Value{}, fmt.Errorf("unknown enum value")
		}
		return protoreflect.ValueOfEnum(value.Number()), nil
	}
	return protoreflect.Value{}, fmt.Errorf("unsupported parameter type")
}

func httpStatus(code codes.Code) int {
	switch code {
	case codes.InvalidArgument, codes.OutOfRange, codes.FailedPrecondition:
		return 400
	case codes.Unauthenticated:
		return 401
	case codes.PermissionDenied:
		return 403
	case codes.NotFound:
		return 404
	case codes.AlreadyExists, codes.Aborted:
		return 409
	case codes.ResourceExhausted:
		return 429
	case codes.Canceled:
		return 499
	case codes.DeadlineExceeded:
		return 504
	case codes.Unimplemented:
		return 501
	case codes.Unavailable:
		return 503
	default:
		return 500
	}
}
func publicMessage(code codes.Code) string {
	switch code {
	case codes.Unavailable:
		return "Upstream service unavailable"
	case codes.DeadlineExceeded:
		return "Upstream deadline exceeded"
	case codes.Canceled:
		return "Request canceled"
	case codes.Internal, codes.Unknown, codes.DataLoss:
		return "Upstream request failed"
	default:
		return code.String()
	}
}

func codeName(code codes.Code) string {
	names := [...]string{"OK", "CANCELED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"}
	if int(code) >= len(names) {
		return "UNKNOWN"
	}
	return names[code]
}
func writeError(w http.ResponseWriter, _ *http.Request, httpCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message, "requestId": w.Header().Get("X-Request-ID")})
}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// Middleware forwards no caller-supplied identity headers such as X-User-ID.
// A configured API token is a development access gate, not an identity system.
func Middleware(next http.Handler, apiToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(id) {
			var b [16]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		if apiToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+apiToken)) != 1 {
			writeError(w, r, 401, "UNAUTHENTICATED", "Invalid bearer token")
			return
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("HTTP request", "method", r.Method, "path", r.URL.Path, "request_id", id, "duration", time.Since(start))
	})
}
