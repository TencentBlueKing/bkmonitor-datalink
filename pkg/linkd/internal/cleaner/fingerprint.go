// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"linkd/internal/config"
	"linkd/internal/domain"
)

type fingerprintPart struct {
	Path  string `json:"path"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

func computeFingerprint(event domain.Event, source config.EventSource) (string, error) {
	if source.FingerprintMode == config.FingerprintModeField {
		part, err := fingerprintValue(event, source.FingerprintField)
		if err != nil {
			return "", err
		}
		if part.Type != "string" || strings.TrimSpace(part.Value) == "" {
			return "", fmt.Errorf("fingerprint field %q must be a non-empty string", source.FingerprintField)
		}
		if len(part.Value) > 128 {
			return "", fmt.Errorf("fingerprint exceeds 128 bytes")
		}
		return part.Value, nil
	}
	paths := append([]string(nil), source.FingerprintFields...)
	sort.Strings(paths)
	parts := make([]fingerprintPart, 0, len(paths))
	for _, path := range paths {
		part, err := fingerprintValue(event, path)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	body, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("encode fingerprint fields: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func fingerprintValue(event domain.Event, path string) (fingerprintPart, error) {
	part := fingerprintPart{Path: path, Type: "string"}
	switch path {
	case "source_alert_id":
		part.Value = event.SourceAlertID
	case "condition_key":
		part.Value = event.ConditionKey
	case "subject_system":
		part.Value = event.SubjectSystem
	case "subject_type":
		part.Value = event.SubjectType
	case "subject_id":
		part.Value = event.SubjectID
	default:
		if !strings.HasPrefix(path, "dimensions.") {
			return fingerprintPart{}, fmt.Errorf("unsupported fingerprint field %q", path)
		}
		key := strings.TrimPrefix(path, "dimensions.")
		value, exists := event.Dimensions[key]
		if !exists {
			return fingerprintPart{}, fmt.Errorf("fingerprint dimension %q is missing", key)
		}
		return scalarFingerprintPart(path, value)
	}
	if part.Value == "" {
		return fingerprintPart{}, fmt.Errorf("fingerprint field %q is empty", path)
	}
	return part, nil
}

func scalarFingerprintPart(path string, value domain.Scalar) (fingerprintPart, error) {
	part := fingerprintPart{Path: path}
	switch value.Kind() {
	case domain.ScalarKindString:
		part.Type = "string"
		part.Value, _ = value.StringValue()
	case domain.ScalarKindNumber:
		part.Type = "number"
		number, _ := value.NumberValue()
		part.Value = strconv.FormatFloat(number, 'g', -1, 64)
	case domain.ScalarKindBool:
		part.Type = "boolean"
		boolean, _ := value.BoolValue()
		part.Value = strconv.FormatBool(boolean)
	default:
		return fingerprintPart{}, fmt.Errorf("fingerprint field %q contains invalid scalar", path)
	}
	return part, nil
}
