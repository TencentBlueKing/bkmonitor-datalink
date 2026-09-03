// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"linkd/internal/store"
)

const (
	cursorKindEvent        = "elasticsearch_unprocessed_event"
	cursorKindAllEvent     = "elasticsearch_all_unprocessed_event"
	cursorKindEventByAlert = "elasticsearch_event_by_alert"
	cursorKindAlertLog     = "elasticsearch_alert_log"
)

type cursorPayload struct {
	Kind          string            `json:"kind"`
	TenantID      string            `json:"tenant_id"`
	ParentID      string            `json:"parent_id,omitempty"`
	BoundaryNS    int64             `json:"boundary_ns,omitempty"`
	RangeStartNS  int64             `json:"range_start_ns,omitempty"`
	TargetsDigest string            `json:"targets_digest"`
	TimeNS        int64             `json:"time_ns"`
	ObjectID      string            `json:"object_id"`
	ItemTenantID  string            `json:"item_tenant_id,omitempty"`
	PITID         string            `json:"pit_id"`
	SearchAfter   []json.RawMessage `json:"search_after"`
}

func encodeCursor(payload cursorPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode elasticsearch cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(encoded string, expected cursorPayload) (cursorPayload, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cursorPayload{}, fmt.Errorf("%w: decode elasticsearch cursor: %w", store.ErrInvalidCursor, err)
	}
	var decoded cursorPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return cursorPayload{}, fmt.Errorf("%w: decode elasticsearch cursor payload: %w", store.ErrInvalidCursor, err)
	}
	if decoded.Kind != expected.Kind || decoded.TenantID != expected.TenantID ||
		decoded.ParentID != expected.ParentID || decoded.BoundaryNS != expected.BoundaryNS ||
		decoded.RangeStartNS != expected.RangeStartNS ||
		decoded.TargetsDigest != expected.TargetsDigest || decoded.ObjectID == "" || decoded.PITID == "" ||
		len(decoded.SearchAfter) != cursorSearchAfterSize(expected.Kind) {
		return cursorPayload{}, fmt.Errorf("%w: cursor does not belong to this query", store.ErrInvalidCursor)
	}
	return decoded, nil
}

func cursorSearchAfterSize(kind string) int {
	if kind == cursorKindAllEvent {
		return 4
	}
	return 3
}

func targetsDigest(targets []string) string {
	cloned := append([]string(nil), targets...)
	sort.Strings(cloned)
	digest := sha256.Sum256([]byte(strings.Join(cloned, "\x00")))
	return hex.EncodeToString(digest[:])
}
