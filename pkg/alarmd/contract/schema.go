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
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion           = "detection-outcome/1.0"
	schemaMajor             = 1
	detectionOutcomeSchema  = "detection-outcome"
	triggerStrategyIRSchema = "trigger-strategy-ir"

	featureFullLevelEvaluations = "full-level-evaluations-v1"
	featureRawJSON              = "raw-json-v1"
	featureRawStrategyBytes     = "raw-strategy-bytes-v1"

	PurposeDetect  = "DETECT"
	PurposeNodata  = "NODATA"
	maxContractInt = 1<<31 - 1
)

var (
	canonicalDecimalPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)
	sha256Pattern           = regexp.MustCompile(`^[0-9a-f]{64}$`)
	dimensionsMD5Pattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type Schema struct {
	Name  string `json:"name"`
	Major int    `json:"major"`
	Minor int    `json:"minor"`
}

type StrategyRef struct {
	StrategyID    string `json:"strategy_id"`
	ItemID        string `json:"item_id"`
	Generation    string `json:"generation"`
	ContentSHA256 string `json:"content_sha256"`
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return "contract validation: " + e.Message
	}
	return fmt.Sprintf("contract validation: %s: %s", e.Field, e.Message)
}

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

func validateHeader(schema Schema, features []string, name string, required map[string]struct{}) error {
	if schema.Name != name {
		return invalid("schema.name", "unexpected schema name")
	}
	if schema.Major != schemaMajor {
		return invalid("schema.major", "unsupported schema major")
	}
	if schema.Minor < 0 || schema.Minor > maxContractInt {
		return invalid("schema.minor", "must be a non-negative 32-bit signed integer")
	}
	seen := make(map[string]struct{}, len(features))
	for _, feature := range features {
		if _, ok := required[feature]; !ok {
			return invalid("required_features", "unsupported required feature "+feature)
		}
		if _, ok := seen[feature]; ok {
			return invalid("required_features", "contains duplicate feature "+feature)
		}
		seen[feature] = struct{}{}
	}
	for feature := range required {
		if _, ok := seen[feature]; !ok {
			return invalid("required_features", "missing required feature "+feature)
		}
	}
	return nil
}

func validateStrategyRef(ref StrategyRef) error {
	if !canonicalDecimalPattern.MatchString(ref.StrategyID) {
		return invalid("strategy_ref.strategy_id", "must use canonical decimal form")
	}
	if !canonicalDecimalPattern.MatchString(ref.ItemID) {
		return invalid("strategy_ref.item_id", "must use canonical decimal form")
	}
	if ref.Generation == "" {
		return invalid("strategy_ref.generation", "must be non-empty")
	}
	if !sha256Pattern.MatchString(ref.ContentSHA256) {
		return invalid("strategy_ref.content_sha256", "must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func validatePurpose(purpose string) error {
	if purpose != PurposeDetect && purpose != PurposeNodata {
		return invalid("purpose", "unsupported purpose")
	}
	return nil
}

func decodeJSONObject(payload []byte, target any) error {
	if !utf8.Valid(payload) {
		return invalid("json", "must contain valid UTF-8")
	}
	if err := validateJSONSurrogateEscapes(payload); err != nil {
		return err
	}
	if err := rejectDuplicateJSONFields(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return invalid("json", err.Error())
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func validateJSONSurrogateEscapes(payload []byte) error {
	for index := 0; index < len(payload); index++ {
		if payload[index] != '"' {
			continue
		}
		for index++; index < len(payload); index++ {
			switch payload[index] {
			case '"':
				goto stringDone
			case '\\':
				index++
				if index >= len(payload) || payload[index] != 'u' {
					continue
				}
				codeUnit, ok := decodeJSONHexQuad(payload, index+1)
				if !ok {
					continue
				}
				index += 4
				if codeUnit >= 0xdc00 && codeUnit <= 0xdfff {
					return invalid("json", "contains unpaired low surrogate escape")
				}
				if codeUnit < 0xd800 || codeUnit > 0xdbff {
					continue
				}
				if index+6 >= len(payload) || payload[index+1] != '\\' || payload[index+2] != 'u' {
					return invalid("json", "contains unpaired high surrogate escape")
				}
				low, ok := decodeJSONHexQuad(payload, index+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return invalid("json", "contains unpaired high surrogate escape")
				}
				index += 6
			}
		}
	stringDone:
	}
	return nil
}

func decodeJSONHexQuad(payload []byte, start int) (uint16, bool) {
	if start+4 > len(payload) {
		return 0, false
	}
	var value uint16
	for _, char := range payload[start : start+4] {
		value <<= 4
		switch {
		case char >= '0' && char <= '9':
			value += uint16(char - '0')
		case char >= 'a' && char <= 'f':
			value += uint16(char-'a') + 10
		case char >= 'A' && char <= 'F':
			value += uint16(char-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func validateJSONObjectFields(
	payload []byte,
	field string,
	required []string,
	optional []string,
	allowUnknown bool,
) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := decodeJSONObject(payload, &object); err != nil {
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

func validateContractEnvelope(
	payload []byte,
	field string,
	schemaName string,
	required []string,
	optional []string,
) (Schema, map[string]json.RawMessage, error) {
	object, err := validateJSONObjectFields(payload, field, required, optional, true)
	if err != nil {
		return Schema{}, nil, err
	}
	_, err = validateJSONObjectFields(
		object["schema"],
		field+".schema",
		[]string{"name", "major", "minor"},
		nil,
		true,
	)
	if err != nil {
		return Schema{}, nil, err
	}
	var schema Schema
	if err := decodeJSONObject(object["schema"], &schema); err != nil {
		return Schema{}, nil, err
	}
	if schema.Name != schemaName || schema.Major != schemaMajor || schema.Minor < 0 || schema.Minor > maxContractInt {
		return Schema{}, nil, invalid(field+".schema", "unsupported schema version")
	}
	allowUnknown := schema.Minor > 0
	if _, err := validateJSONObjectFields(payload, field, required, optional, allowUnknown); err != nil {
		return Schema{}, nil, err
	}
	if _, err := validateJSONObjectFields(
		object["schema"],
		field+".schema",
		[]string{"name", "major", "minor"},
		nil,
		allowUnknown,
	); err != nil {
		return Schema{}, nil, err
	}
	return schema, object, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return invalid("json", "contains trailing value")
		}
		return invalid("json", err.Error())
	}
	return nil
}

func rejectDuplicateJSONFields(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return invalid("json", err.Error())
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		if number, ok := token.(json.Number); ok && strings.ContainsAny(number.String(), ".eE") {
			parsed, err := strconv.ParseFloat(number.String(), 64)
			if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
				return invalid("json", "contains non-finite floating-point number")
			}
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return invalid("json", err.Error())
			}
			key, ok := keyToken.(string)
			if !ok {
				return invalid("json", "object key must be a string")
			}
			if _, exists := seen[key]; exists {
				return invalid("json", "duplicate field "+key)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return invalid("json", "unterminated object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return invalid("json", "unterminated array")
		}
	default:
		return invalid("json", "unexpected delimiter")
	}
	return nil
}
