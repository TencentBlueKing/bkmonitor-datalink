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
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func payloadAlert(tb testing.TB) domain.Alert {
	tb.Helper()
	a := storetest.Alert("tenant", "payload-alert", "event", "fingerprint", "warning")
	a.ExtraData = domain.JSONObject{"data": json.RawMessage(`{"large":"` + strings.Repeat("x", 64<<10) + `","n":9007199254740993}`)}
	a.EnrichStatus = domain.EnrichStatusSucceeded
	a.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"metric":{"status":"succeeded","value":{"large":"` + strings.Repeat("y", 64<<10) + `"}}}]`)}
	var err error
	a, err = a.Normalize()
	if err != nil {
		tb.Fatal(err)
	}
	return a
}

func TestPreparedAlertReplacementMatchesFullValidation(t *testing.T) {
	for _, name := range []string{"business", "json_format", "bad_json", "bad_enrich", "changed_status", "anchor", "revision", "terminal"} {
		t.Run(name, func(t *testing.T) {
			current := payloadAlert(t)
			next := current.Clone()
			next.UpdateAt = current.UpdateAt.Add(time.Second)
			next.LatestEventID = "next"
			switch name {
			case "json_format":
				next.ExtraData["data"] = append([]byte(" \n"), next.ExtraData["data"]...)
			case "bad_json":
				next.ExtraData["data"] = json.RawMessage(`{"bad":`)
			case "bad_enrich":
				next.Enrich["processors"] = json.RawMessage(`[{"bad":{"status":"succeeded"}}]`)
			case "changed_status":
				next.EnrichStatus = domain.EnrichStatusFailed
			case "anchor":
				next.TriggerEventID = "other"
			case "revision":
				next.Revision = 7
			case "terminal":
				current.Status = domain.AlertStatusRecovered
				at := current.UpdateAt
				current.EndAt = &at
				current.EndType = domain.AlertEndTypeSource
				next = current.Clone()
				next.Status = domain.AlertStatusActive
				next.EndAt = nil
				next.EndType = ""
				next.UpdateAt = current.UpdateAt.Add(time.Second)
			}
			before, replacement := current.Clone(), next.Clone()
			full, fullErr := domain.PrepareAlertReplacement(current, next)
			fast, fastErr := domain.PrepareAlertReplacementFromSnapshot(current, next)
			wantSuccess := name == "business" || name == "json_format"
			if (fullErr == nil) != wantSuccess {
				t.Fatalf("full validation success=%v, want %v: %v", fullErr == nil, wantSuccess, fullErr)
			}
			if (fullErr == nil) != (fastErr == nil) {
				t.Fatalf("full=%v fast=%v", fullErr, fastErr)
			}
			if fullErr == nil && !reflect.DeepEqual(full, fast) {
				t.Fatal("prepared path changed result")
			}
			if !reflect.DeepEqual(current, before) || !reflect.DeepEqual(next, replacement) {
				t.Fatal("preparation mutated inputs")
			}
			if name == "business" {
				if fast.Revision != 2 {
					t.Fatal("revision/projection ownership lost")
				}
				next.ExtraData["data"][0] = '!'
				if fast.ExtraData["data"][0] == '!' {
					t.Fatal("trusted output retained replacement-owned JSON")
				}
			}
		})
	}
}

func BenchmarkAlertReplacementPayload(b *testing.B) {
	current := payloadAlert(b)
	next := current.Clone()
	next.UpdateAt = current.UpdateAt.Add(time.Second)
	next.LatestEventID = "next"
	for _, fast := range []bool{false, true} {
		name := "full"
		if fast {
			name = "snapshot"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if fast {
					_, err = domain.PrepareAlertReplacementFromSnapshot(current, next)
				} else {
					_, err = domain.PrepareAlertReplacement(current, next)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkAlertBusinessSnapshotComparison(b *testing.B) {
	a := payloadAlert(b)
	c := a.Clone()
	b.ReportAllocs()
	for b.Loop() {
		if !domain.SameAlertBusinessSnapshot(a, c) {
			b.Fatal("snapshot differs")
		}
	}
}
