// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"encoding/json"
	"sort"
	"unicode/utf8"
)

// NoDataEmissionFacts is what one write of a Plan's events did with the
// no-data events in it: how many were written and acknowledged, by kind; how
// many the write could not answer for, the acknowledgement unknown and the
// batch possibly landed; how many are known not written -- refused, withheld,
// deferred for the lease; and the latest acknowledged event of each kind.
// Nil on a write that carried no no-data event.
//
// The counts are of acknowledged writes, retries included: an event sent
// again after an unknown acknowledgement that had in fact landed counts
// twice. They are not alerts.
type NoDataEmissionFacts struct {
	AbnormalSent uint32
	RecoverySent uint32
	AckUnknown   uint32
	NotWritten   uint32
	LastAbnormal *NoDataEmittedEvent
	LastRecovery *NoDataEmittedEvent
}

// NoDataEmittedEvent is one acknowledged no-data event as a reader looks it
// up downstream: the Slot that decided it, the key its message was written
// under -- the alert id under the standard protocol, the dedupe md5 of the
// group's dimensions and the tag under the compatibility protocol, which is
// what the alert store files it by -- and the group's dimension values, at most
// MaxNoDataEmittedGroupKeys keys of at most MaxNoDataEmittedGroupValueBytes
// bytes each, GroupTruncated when either bound cut it.
type NoDataEmittedEvent struct {
	EvaluationTime int64             `json:"evaluation_time"`
	AlertKey       string            `json:"alert_key,omitempty"`
	Group          map[string]string `json:"group,omitempty"`
	GroupTruncated bool              `json:"group_truncated,omitempty"`
}

const (
	MaxNoDataEmittedGroupKeys       = 8
	MaxNoDataEmittedGroupValueBytes = 64
)

// NoDataEmittedGroup is a group's dimensions bounded for a fact: the no-data
// tag itself left out, the first keys in order, a string value unquoted and
// any value cut at a character boundary within the byte bound.
func NoDataEmittedGroup(dimensions map[string]json.RawMessage, tag string) (map[string]string, bool) {
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		if key != tag {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, false
	}
	sort.Strings(keys)
	truncated := false
	if len(keys) > MaxNoDataEmittedGroupKeys {
		keys, truncated = keys[:MaxNoDataEmittedGroupKeys], true
	}
	group := make(map[string]string, len(keys))
	for _, key := range keys {
		raw := dimensions[key]
		value := string(raw)
		var text string
		if json.Unmarshal(raw, &text) == nil {
			value = text
		}
		if len(value) > MaxNoDataEmittedGroupValueBytes {
			cut := MaxNoDataEmittedGroupValueBytes
			for cut > 0 && !utf8.RuneStart(value[cut]) {
				cut--
			}
			value, truncated = value[:cut], true
		}
		group[key] = value
	}
	return group, truncated
}
