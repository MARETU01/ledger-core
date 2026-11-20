package gateway

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// bindQuery accepts only original .proto field names, including nested paths.
// URL values never pass
// through an untyped JSON number, preserving full int64/uint64 precision.
func bindQuery(message protoreflect.Message, r *http.Request) error {
	if len(r.URL.RawQuery) > 16<<10 {
		return fmt.Errorf("query exceeds 16 KiB")
	}
	if strings.Count(r.URL.RawQuery, "&") >= 128 {
		return fmt.Errorf("too many query parameters")
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return fmt.Errorf("invalid query encoding")
	}
	for name, values := range query {
		parts := strings.Split(name, ".")
		if len(parts) > 8 {
			return fmt.Errorf("query nesting exceeds 8 levels")
		}
		fields := []protoreflect.FieldDescriptor{}
		md := message.Descriptor()
		for i, part := range parts {
			field := md.Fields().ByName(protoreflect.Name(part))
			if field == nil {
				return fmt.Errorf("unknown input field %s", name)
			}
			fields = append(fields, field)
			if i < len(parts)-1 {
				if field.Message() == nil || field.IsMap() || field.IsList() {
					return fmt.Errorf("invalid nested field %s", name)
				}
				md = field.Message()
			} else if field.Message() != nil || field.IsMap() {
				return fmt.Errorf("field %s requires a JSON body", name)
			}
		}
		if err := setField(message, fields, values); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func setField(message protoreflect.Message, fields []protoreflect.FieldDescriptor, values []string) error {
	field := fields[0]
	if oneof := field.ContainingOneof(); oneof != nil {
		if selected := message.WhichOneof(oneof); selected != nil && selected != field {
			return fmt.Errorf("conflicting oneof fields %s and %s", selected.Name(), field.Name())
		}
	}
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
