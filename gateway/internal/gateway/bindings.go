package gateway

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/MARETU01/ledger-core/gateway/internal/descriptor"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type fieldBinding struct {
	source string
	fields []protoreflect.FieldDescriptor
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
