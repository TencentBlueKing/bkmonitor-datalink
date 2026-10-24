// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricmeta

import (
	"encoding/json"
	"testing"
)

func TestEntryJSONRoundTrip(t *testing.T) {
	for present := uint8(0); present < 16; present++ {
		want := Entry{Key: NewKey(1, Scope{Type: ScopeAPM, Name: "a-b"}, "metric"), Value: Value{Name: "原名", Type: "counter", OriginalType: "sum", Temporality: "delta", Present: present}}
		if present&PresentDescription != 0 {
			want.Value.Description = ""
		}
		if present&PresentUnit != 0 {
			want.Value.Unit = "1"
		}
		if present&PresentInterval != 0 {
			want.Value.Interval = 1000
		}
		if present&PresentUsage != 0 {
			want.Value.Usage = "sum"
		}
		body, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got Entry
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("present=%d got=%+v want=%+v", present, got, want)
		}
	}
	for _, body := range []string{`{}`, `{`, `{"dataid":"invalid"}`} {
		original := Entry{Key: Key{DataID: 42}}
		got := original
		if err := json.Unmarshal([]byte(body), &got); err == nil {
			t.Fatal("invalid entry accepted")
		}
		if got != original {
			t.Fatal("failed decode mutated destination")
		}
	}
}
