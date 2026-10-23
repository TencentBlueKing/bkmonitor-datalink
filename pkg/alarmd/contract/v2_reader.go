// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type MessageFramingError struct {
	ReasonCode string
	FieldPath  string
	Message    string
}

func (e *MessageFramingError) Error() string {
	if e.FieldPath == "" {
		return fmt.Sprintf("alarmd contract framing: %s: %s", e.ReasonCode, e.Message)
	}
	return fmt.Sprintf("alarmd contract framing: %s: %s: %s", e.ReasonCode, e.FieldPath, e.Message)
}

func framing(reason, field, message string) error {
	return &MessageFramingError{ReasonCode: reason, FieldPath: field, Message: message}
}

// decodePrevalidatedJSONV2 decodes a JSON slice taken from an envelope after
// the complete payload has passed UTF-8, surrogate and duplicate-field
// validation. Repeating those whole-tree scans for every nested Plan and
// Record is unnecessary and dominates the phase-one Adapter hot path.
func decodePrevalidatedJSONV2(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return invalid("json", err.Error())
	}
	return ensureJSONEOF(decoder)
}

func validatePrevalidatedJSONObjectFieldsV2(
	payload []byte,
	field string,
	required []string,
	optional []string,
	allowUnknown bool,
) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := decodePrevalidatedJSONV2(payload, &object); err != nil {
		return nil, invalid(field, err.Error())
	}
	if object == nil {
		return nil, invalid(field, "must be a JSON object")
	}
	known := make(map[string]struct{}, len(required)+len(optional))
	for _, name := range append(append([]string(nil), required...), optional...) {
		known[name] = struct{}{}
	}
	for _, name := range required {
		raw, ok := object[name]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, invalid(field+"."+name, "is required and must be non-null")
		}
	}
	for name := range object {
		if _, ok := known[name]; ok {
			continue
		}
		for canonical := range known {
			if strings.EqualFold(name, canonical) {
				return nil, invalid(field+"."+name, "case-collides with "+canonical)
			}
		}
		if !allowUnknown {
			return nil, invalid(field+"."+name, "unknown field for schema minor")
		}
	}
	return object, nil
}

func validateSchemaRawV2(raw json.RawMessage, field, name string, major int) (int, error) {
	object, err := validatePrevalidatedJSONObjectFieldsV2(raw, field, []string{"name", "major", "minor"}, nil, false)
	if err != nil {
		return 0, framing(ReasonMalformedJSON, field, err.Error())
	}
	var schema Schema
	if err := decodePrevalidatedJSONV2(raw, &schema); err != nil {
		return 0, framing(ReasonMalformedJSON, field, err.Error())
	}
	if schema.Name != name || schema.Major != major || schema.Minor < 0 || schema.Minor > maxContractInt {
		return 0, framing(ReasonSchemaMajorUnsupported, field, "unsupported schema name or version")
	}
	_ = object
	return schema.Minor, nil
}

func validateRequiredFeaturesRawV2(raw json.RawMessage, field string) error {
	var features []string
	if err := decodePrevalidatedJSONV2(raw, &features); err != nil || features == nil {
		return framing(ReasonMalformedJSON, field, "must be an array")
	}
	previous := ""
	for index, feature := range features {
		if feature == "" || (index > 0 && feature <= previous) {
			return framing(ReasonRequiredFeatureUnsupported, field, "features must be non-empty, sorted and unique")
		}
		return framing(ReasonRequiredFeatureUnsupported, field, "required feature is not supported")
	}
	return nil
}

func isOpaqueASCII(value string) bool {
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return value != ""
}
