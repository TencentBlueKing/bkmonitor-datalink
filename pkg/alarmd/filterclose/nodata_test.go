// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
)

// Every filter alarmd builds tells a no-data alert by the tag's presence
// (exists), never by a value: the link keeps the tag as the JSON boolean
// true, which a term of "1" or 1 would not match.
func TestEveryNoDataClauseIsExistsNeverAValue(t *testing.T) {
	digest := strings.Repeat("a", 32)
	builders := map[string]func() (Filter, error){
		"bucket no-data": func() (Filter, error) {
			return BucketFilter(testScope, Bucket{Version: "4", Digest: digest, NoData: true})
		},
		"bucket metric":    func() (Filter, error) { return BucketFilter(testScope, Bucket{Version: "4", Digest: digest}) },
		"bucket no digest": func() (Filter, error) { return BucketFilter(testScope, Bucket{Version: "4", NoData: true}) },
		"alerts": func() (Filter, error) {
			return alertsFilter(testScope, Bucket{Version: "4", NoData: true}, []string{"a"})
		},
		"level": func() (Filter, error) { return LevelFilter(testScope, []string{"3"}, []string{"info"}) },
		"enumeration": func() (Filter, error) {
			return enumerationFilter(testScope, "5", []Bucket{{Version: "4", NoData: true}, {Version: "4", Digest: digest}})
		},
		"strategy":    func() (Filter, error) { return StrategyFilter(testScope) },
		"version":     func() (Filter, error) { return VersionFilter(testScope, "4") },
		"fingerprint": func() (Filter, error) { return FingerprintFilter(testScope, []string{"f"}, fakeDecidedAt) },
		"scope": func() (Filter, error) {
			return ScopeFilter(testScope, TargetShape{Dimensions: []string{"ip"}}, []Target{{"ip": "192.0.2.1"}})
		},
	}
	tag := dimensionNoData
	for name, build := range builders {
		filter, err := build()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var query any
		if err := json.Unmarshal(filter.JSON(), &query); err != nil {
			t.Fatal(err)
		}
		exists := 0
		var walk func(any)
		walk = func(node any) {
			switch value := node.(type) {
			case map[string]any:
				for kind, body := range value {
					if fields, ok := body.(map[string]any); ok {
						if _, named := fields[tag]; named && kind != "exists" {
							t.Fatalf("%s: %s on the no-data tag: %s", name, kind, filter.JSON())
						}
						if kind == "exists" && fields["field"] == tag {
							exists++
						}
					}
					walk(body)
				}
			case []any:
				for _, item := range value {
					walk(item)
				}
			}
		}
		walk(query)
		if strings.HasPrefix(name, "bucket") || name == "alerts" || name == "level" || name == "enumeration" {
			if exists == 0 {
				t.Fatalf("%s: no exists on the no-data tag: %s", name, filter.JSON())
			}
		}
	}
}

// A bucket whose sample carries the no-data tag but says
// evaluation_family metric_algorithm (or the other way) is not decided:
// counted family_mismatch, nothing closed. An alert read one by one whose
// family and tag disagree is not closed either (edit_family_unknown). The
// agreeing ones close as before.
func TestAnAlertWhoseFamilyDisagreesWithItsTagIsNotClosed(t *testing.T) {
	h := newHarness(t)
	old := factsOf("m4")
	disagree := func(alert *fakeAlert, family string) *fakeAlert {
		var extra map[string]any
		_ = json.Unmarshal(alert.extra, &extra)
		extra["evaluation_family"] = family
		alert.extra, _ = json.Marshal(extra)
		return alert
	}
	h.link.add(disagree(opened("x-tagged", "4", old, true, "warning", nil), "metric_algorithm"))
	h.link.add(disagree(opened("x-untagged", "3", old, false, "warning", nil), "no_data"))
	h.link.add(opened("x-agrees", "2", old, false, "warning", nil))
	h.link.add(disagree(opened("n-tagged", "1", nil, true, "warning", map[string]string{"bk_target_ip": "192.0.2.1", "removed_dim": "x"}), "metric_algorithm"))
	h.link.add(opened("n-agrees", "1", nil, false, "warning", map[string]string{"bk_target_ip": "192.0.2.1", "removed_dim": "x"}))
	h.opened(t, "41", "4", nil, fingerprints(0, 5)...)
	h.opened(t, "41", "3", nil, fingerprints(5, 5)...)
	h.opened(t, "41", "2", nil, fingerprints(10, 5)...)
	closer := h.closer(t)
	if word, _ := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()}); word != "" {
		t.Fatalf("word %s", word)
	}
	for id, want := range map[string]int{"x-tagged": 0, "x-untagged": 0, "x-agrees": 1, "n-tagged": 0, "n-agrees": 1} {
		if got := h.link.alert(id).closes; got != want {
			t.Fatalf("%s closed %d, want %d (stats %v)", id, got, want, nonZero(closer.Stats()))
		}
	}
	stats := closer.Stats()
	if stats[OutcomeFamilyMismatch] != 2 || stats["edit_"+editclose.OutcomeFamilyUnknown] != 1 {
		t.Fatalf("stats %v", nonZero(stats))
	}
}
