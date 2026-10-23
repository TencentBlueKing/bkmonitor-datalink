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
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// A group's dimensions are kept as a reader searches the alert store by
// them: the tag left out, a string value unquoted, other values as written;
// and bounded, the first keys in order and each value cut at a character
// boundary, with the cut said.
func TestAnEmittedGroupIsKeptReadableAndBounded(t *testing.T) {
	group, truncated := NoDataEmittedGroup(map[string]json.RawMessage{
		"__NO_DATA_DIMENSION__": json.RawMessage("true"), "bk_target_ip": json.RawMessage(`"192.0.2.10"`), "bk_target_cloud_id": json.RawMessage("0"),
	}, "__NO_DATA_DIMENSION__")
	if truncated || len(group) != 2 || group["bk_target_ip"] != "192.0.2.10" || group["bk_target_cloud_id"] != "0" {
		t.Fatalf("group = %v truncated=%v, want the two dimensions, the tag left out, the string unquoted", group, truncated)
	}
	wide := map[string]json.RawMessage{}
	for index := 0; index < MaxNoDataEmittedGroupKeys+3; index++ {
		wide["k"+strconv.Itoa(10+index)] = json.RawMessage(`"v"`)
	}
	wide["k10"] = json.RawMessage(strconv.Quote(strings.Repeat("a", MaxNoDataEmittedGroupValueBytes-1) + "\u00e9xyz"))
	group, truncated = NoDataEmittedGroup(wide, "__NO_DATA_DIMENSION__")
	if !truncated || len(group) != MaxNoDataEmittedGroupKeys || group["k10"] == "" {
		t.Fatalf("wide group = %d keys truncated=%v, want the first %d keys and the cut said", len(group), truncated, MaxNoDataEmittedGroupKeys)
	}
	if value := group["k10"]; len(value) > MaxNoDataEmittedGroupValueBytes || !utf8.ValidString(value) {
		t.Fatalf("long value = %q (%d bytes), want it cut within %d bytes at a character boundary", value, len(value), MaxNoDataEmittedGroupValueBytes)
	}
	if group, truncated = NoDataEmittedGroup(map[string]json.RawMessage{"__NO_DATA_DIMENSION__": json.RawMessage("true")}, "__NO_DATA_DIMENSION__"); group != nil || truncated {
		t.Fatalf("whole-item group = %v, want none", group)
	}
}
