package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/descriptor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/dynamicpb"
)

type routeHandler struct {
	grpcMethod string
	method     *descriptor.Method
	conn       grpc.ClientConnInterface
	timeout    time.Duration
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
	if r.Method == http.MethodGet {
		if err := bindQuery(request, r); err != nil {
			writeError(w, r, 400, "INVALID_ARGUMENT", err.Error())
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
	if err := h.conn.Invoke(ctx, h.grpcMethod, request, response); err != nil {
		code := status.Code(err)
		if errors.Is(err, context.DeadlineExceeded) {
			code = codes.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			code = codes.Canceled
		}
		slog.Warn("gRPC invocation failed", "method", h.grpcMethod, "request_id", w.Header().Get("X-Request-ID"), "code", code.String(), "error", err)
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
