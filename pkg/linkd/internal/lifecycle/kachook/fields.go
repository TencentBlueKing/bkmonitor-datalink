// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kachook

import (
	"encoding/json"

	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/view"
	"linkd/internal/jsonpath"
)

func mappedPayload(message kingeye.AlarmMessage, alert domain.Alert, fields map[string]string) ([]byte, error) {
	if err := kingeye.ValidateFieldMappings(fields); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(message)
	if err != nil || len(fields) == 0 {
		return encoded, err
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	effective, err := view.EnrichedAlert(alert)
	if err != nil {
		return nil, err
	}
	document, err := domain.AlertDocument(effective)
	if err != nil {
		return nil, err
	}
	for key, path := range fields {
		target, _ := jsonpath.ParseTarget(path)
		value, found := target.Get(document)
		if !found {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		result[key] = encoded
	}
	return json.Marshal(result)
}
