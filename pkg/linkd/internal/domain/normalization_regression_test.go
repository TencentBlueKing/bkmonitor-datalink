// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestNormalizationPreservesJSONValidationAndOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"nested", ` {"z": [1, {"b":2,"a":1}], "a":9007199254740993} `, true},
		{"duplicate", `{"a":1,"a":2}`, false},
		{"nested duplicate", `[{"a":1,"a":2}]`, false},
		{"trailing", `{} {}`, false},
		{"invalid", `[`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := domain.JSONObject{"value": json.RawMessage(tc.raw)}
			event := validEvent()
			event.SourceRawData = input
			event.ExtraData = input
			alert := validAlert()
			alert.ExtraData = input
			alert.Enrich = domain.JSONObject{
				"status":     json.RawMessage(`"succeeded"`),
				"processors": json.RawMessage(`[{"test":{"status":"succeeded","value":{"value":` + tc.raw + `}}}]`),
			}
			log := domain.AlertLog{LogID: "log", BKTenantID: "tenant", AlertID: "alert", OperatorKind: domain.OperatorKindSystem, OperationKind: domain.OperationKindTrigger, Params: input, CreatedTime: time.Now()}
			en, ee := event.Normalize()
			an, ae := alert.Normalize()
			ln, le := log.Normalize()
			if (ee == nil) != tc.valid || (ae == nil) != tc.valid || (le == nil) != tc.valid {
				t.Fatalf("normalize event=%v alert=%v log=%v", ee, ae, le)
			}
			if (event.Validate() == nil) != tc.valid || (alert.Validate() == nil) != tc.valid || (log.Validate() == nil) != tc.valid {
				t.Fatal("Validate disagrees with Normalize")
			}
			if !tc.valid {
				return
			}
			want, err := input.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(en.SourceRawData, want) || !reflect.DeepEqual(an.ExtraData, want) || !reflect.DeepEqual(ln.Params, want) {
				t.Fatal("canonical JSON changed")
			}
			input["value"][0] = '!'
			if en.Validate() != nil || an.Validate() != nil || ln.Validate() != nil {
				t.Fatal("normalized value shares input bytes")
			}
			en.SourceRawData["value"] = json.RawMessage(`{"a":1,"a":2}`)
			an.Enrich["processors"] = json.RawMessage(`[{"test":{"status":"succeeded","value":{"a":1,"a":2}}}]`)
			ln.Params["value"] = json.RawMessage(`{"a":1,"a":2}`)
			if en.Validate() == nil || an.Validate() == nil || ln.Validate() == nil {
				t.Fatal("Validate trusted previously normalized value after mutation")
			}
		})
	}
	bad := validEvent()
	bad.EventID = ""
	if _, err := bad.Normalize(); err == nil {
		t.Fatal("Normalize skipped non-JSON validation")
	}
}

func BenchmarkEventNormalizeJSON(b *testing.B) {
	event := validEvent()
	event.SourceRawData = domain.JSONObject{"payload": json.RawMessage(`{"nested":{"b":2,"a":1},"labels":["a","b","c"],"counter":9007199254740993}`)}
	for _, revalidate := range []bool{false, true} {
		name := "single-validation"
		if revalidate {
			name = "with-redundant-validation"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				normalized, err := event.Normalize()
				if err != nil {
					b.Fatal(err)
				}
				if revalidate {
					if err := normalized.Validate(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
