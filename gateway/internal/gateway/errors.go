package gateway

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"
)

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
