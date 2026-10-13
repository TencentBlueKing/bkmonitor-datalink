// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// MarshalJSON emits the explicit object boundary while preserving the wire
// schema and execution digests of existing scalar fields.
func (f Field) MarshalJSON() ([]byte, error) {
	type plain Field
	data, err := json.Marshal(plain(f))
	if err != nil || f.Type != "object" {
		return data, err
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	object["additionalProperties"] = f.AdditionalProperties
	return json.Marshal(object)
}

func validateFieldDefinition(f Field) error {
	switch f.Type {
	case "string", "integer", "number", "boolean", "array", "object":
	default:
		return fmt.Errorf("unsupported parameter type %q", f.Type)
	}
	if f.MinLength < 0 || f.MaxLength < 0 || (f.MaxLength > 0 && f.MinLength > f.MaxLength) ||
		f.MinItems < 0 || f.MaxItems < 0 || (f.MaxItems > 0 && f.MinItems > f.MaxItems) ||
		(f.Minimum != nil && f.Maximum != nil && *f.Minimum > *f.Maximum) {
		return fmt.Errorf("invalid field bounds")
	}
	if f.Pattern != "" {
		if _, err := regexp.Compile(f.Pattern); err != nil {
			return fmt.Errorf("invalid pattern: %w", err)
		}
	}
	for _, name := range f.Required {
		if _, exists := f.Properties[name]; !exists {
			return fmt.Errorf("unknown required property %q", name)
		}
	}
	for name, child := range f.Properties {
		if err := validateFieldDefinition(child); err != nil {
			return fmt.Errorf("property %q: %w", name, err)
		}
	}
	if f.Items != nil {
		return validateFieldDefinition(*f.Items)
	}
	return nil
}

// SchemaOf describes the JSON fields of an existing evidence type. It has no
// role in domain validation; the producer still owns the meaning of its data.
// Recursive or open payloads stay explicitly open instead of inventing fields.
func SchemaOf(value any) any { return typeSchema(reflect.TypeOf(value), map[reflect.Type]bool{}) }
func typeSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return map[string]any{}
	}
	if t.Kind() == reflect.Pointer {
		return map[string]any{"anyOf": []any{typeSchema(t.Elem(), seen), map[string]any{"type": "null"}}}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": []string{"array", "null"}, "items": typeSchema(t.Elem(), seen)}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": typeSchema(t.Elem(), seen)}
	case reflect.Struct:
		if seen[t] {
			return map[string]any{"type": "object", "additionalProperties": true}
		}
		seen[t] = true
		defer delete(seen, t)
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			if f.Anonymous && tag[0] == "" {
				embedded := typeSchema(f.Type, seen)
				if p, ok := embedded["properties"].(map[string]any); ok {
					for k, v := range p {
						props[k] = v
					}
				}
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			props[name] = typeSchema(f.Type, seen)
			optional := false
			for _, word := range tag[1:] {
				if word == "omitempty" {
					optional = true
				}
			}
			if !optional {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": true}
	default:
		return map[string]any{}
	}
}
