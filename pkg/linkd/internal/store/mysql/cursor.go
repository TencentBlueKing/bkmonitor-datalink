// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"linkd/internal/store"
)

const (
	cursorKindEvent        = "mysql_unprocessed_event"
	cursorKindAllEvent     = "mysql_all_unprocessed_event"
	cursorKindEventByAlert = "mysql_event_by_alert"
	cursorKindAlertLog     = "mysql_alert_log"
)

type cursorPayload struct {
	Kind         string `json:"kind"`
	TenantID     string `json:"tenant_id"`
	ParentID     string `json:"parent_id,omitempty"`
	BoundaryNS   int64  `json:"boundary_ns,omitempty"`
	RangeStartNS int64  `json:"range_start_ns,omitempty"`
	TimeNS       int64  `json:"time_ns"`
	ObjectID     string `json:"object_id"`
	ItemTenantID string `json:"item_tenant_id,omitempty"`
}

func encodeCursor(payload cursorPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode mysql cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(encoded string, expected cursorPayload) (cursorPayload, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cursorPayload{}, fmt.Errorf("%w: decode mysql cursor: %w", store.ErrInvalidCursor, err)
	}
	var decoded cursorPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return cursorPayload{}, fmt.Errorf("%w: decode mysql cursor payload: %w", store.ErrInvalidCursor, err)
	}
	if decoded.Kind != expected.Kind || decoded.TenantID != expected.TenantID ||
		decoded.ParentID != expected.ParentID || decoded.BoundaryNS != expected.BoundaryNS ||
		decoded.RangeStartNS != expected.RangeStartNS ||
		decoded.ObjectID == "" {
		return cursorPayload{}, fmt.Errorf("%w: cursor does not belong to this query", store.ErrInvalidCursor)
	}
	return decoded, nil
}
