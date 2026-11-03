// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activecopy

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

// ErrReadScope is a read the copy refuses to send: no event source, no
// fingerprint, more than BatchSize of them, or one that is not a native
// fingerprint.
var ErrReadScope = errors.New("alarmd activecopy: a read names the event source, status active and 1..500 fingerprints")

// ReadQuery is the copy's one read: the active alerts of this source among
// the fingerprints. The tenant is the request's (the link scopes a search
// to it); no strategy is named - a fingerprint names its own.
func ReadQuery(eventSourceID string, fingerprints []string) (json.RawMessage, error) {
	if eventSourceID == "" || len(fingerprints) == 0 || len(fingerprints) > BatchSize {
		return nil, ErrReadScope
	}
	values := append([]string(nil), fingerprints...)
	sort.Strings(values)
	for _, value := range values {
		if !nativeFingerprint(value) {
			return nil, ErrReadScope
		}
	}
	type object = map[string]any
	body, err := json.Marshal(object{"bool": object{"filter": []any{
		object{"term": object{"event_source_id": eventSourceID}},
		object{"term": object{"status": "active"}},
		object{"terms": object{"fingerprint": values}},
	}}})
	if err != nil {
		return nil, err
	}
	if len(body) > linkdmanage.MaxFilterBodyBytes {
		return nil, ErrReadScope
	}
	return body, nil
}

// nativeFingerprint is the 32 lowercase hex digits of contract.MonitorDedupeMD5.
func nativeFingerprint(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
