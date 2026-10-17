// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/store/storetest"
)

func TestShieldTargetOnlyKeepsAllAdmissionGates(t *testing.T) {
	spec := policySpecMap(t, Shield)
	spec["policy"] = map[string]any{}
	c, err := Compile(Shield, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	r := Release{Scope: Scope{TenantID: "tenant", Kind: Shield}, ID: "p", Version: 1, Spec: c.Canonical, Compiled: c.Summary}
	e := policyEvent(`[]`, domain.EnrichStatusSucceeded)
	e.Labels["model_id"] = domain.NewStringScalar("cmdb.host")
	e.Labels["model_inst_id"] = domain.NewStringScalar("123")
	reader := policyTargets{refs: []onemodel.InstanceRef{{ModelID: "cmdb.host", InstanceID: "123", EntityUID: "cmdb.host|123"}}}
	at := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		change  func(*domain.Event)
		targets policyTargets
		matched bool
	}{
		{"matches", func(*domain.Event) {}, reader, true},
		{"outside target", func(*domain.Event) {}, policyTargets{}, false},
		{"outside business", func(e *domain.Event) { e.Dimensions["bk_biz_id"] = domain.NewStringScalar("3") }, reader, false},
		{"terminal", func(e *domain.Event) { e.Evaluations[0].Action = domain.EventActionResolved }, reader, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := e.Clone()
			tc.change(&local)
			v, err := Evaluate(t.Context(), r, c, mustEventView(t, local, nil), tc.targets, at, false)
			if err != nil || v.Matched != tc.matched {
				t.Fatalf("%+v %v", v, err)
			}
		})
	}
	r.TenantID = "other"
	if _, err := Evaluate(t.Context(), r, c, mustEventView(t, e, nil), reader, at, false); err == nil {
		t.Fatal("cross tenant")
	}
	spec["shield_type"], spec["shield_mode"], spec["rely_policy"] = "rely_shield", "custom_shield", map[string]any{}
	if _, err := Compile(Shield, encodeSpec(t, spec)); err == nil {
		t.Fatal("empty dependency conditions")
	}
}

func TestShieldExpandedWindowsAndRegexReferences(t *testing.T) {
	spec := policySpecMap(t, Shield)
	spec["shield_type"], spec["shield_mode"] = "rely_shield", "custom_shield"
	spec["rely_policy"] = spec["policy"]
	for _, minutes := range []int{10080, 10081, 65535, 65536} {
		spec["time_range_before"], spec["time_range_after"] = minutes, minutes
		_, err := Compile(Shield, encodeSpec(t, spec))
		if (err == nil) != (minutes <= 65535) {
			t.Fatalf("minutes=%d: %v", minutes, err)
		}
	}
	spec["time_range_before"], spec["time_range_after"] = 1, 10
	pattern := `(?<=prefix)(?<word>主机)\k<word>`
	spec["policy"] = map[string]any{"expression": "A", "A": map[string]any{"target_key": "name", "condition": "regexp", "target_value": pattern}}
	spec["rely_policy"] = map[string]any{"expression": "A", "A": map[string]any{"target_key": "name", "condition": "regexp", "target_value": "${content}", "is_alarm_field_referenced": true}}
	c, err := Compile(Shield, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	child := Fields{Values: map[string]any{"name": "prefix主机主机 suffix"}}
	for _, expr := range []*Expression{c.Conditions[0], c.Rely} {
		v, err := expr.match(t.Context(), child, Fields{Values: map[string]any{"content": pattern}})
		if err != nil || !v.Matched {
			t.Fatal("engine lost after reference substitution", v, err)
		}
	}
	second, err := Compile(Shield, c.Canonical)
	if err != nil || second.Summary.Digest != c.Summary.Digest {
		t.Fatal("unstable publication", err)
	}
}

type batchShieldFacts struct{ alerts map[string]domain.Alert }

func (f batchShieldFacts) Event(context.Context, string, string) (domain.Event, error) {
	panic("batch preview must not re-enrich or read Events")
}

func (f batchShieldFacts) Alert(_ context.Context, _, id string) (domain.Alert, error) {
	a, ok := f.alerts[id]
	if !ok {
		return domain.Alert{}, ErrNotFound
	}
	return a.Clone(), nil
}

func TestShieldBatchPreviewUsesRealMatchingAndNoWrites(t *testing.T) {
	spec := policySpecMap(t, Shield)
	spec["policy"] = map[string]any{"expression": "A", "A": map[string]any{"target_key": "strategy_name", "condition": "term", "target_value": "cpu"}}
	spec["field_mappings"] = map[string]FieldMapping{"strategy_name": {Path: "$.extra_data.strategy_name", ReadPath: `$["extra_data","labels"]["strategy_name"]`, Kind: FieldText}}
	spec["timezone"] = "Asia/Shanghai"
	spec["activate_times"] = []ActiveTime{{Period: "everyday", OpenClock: "09:00:00", CloseClock: "10:00:00"}}
	a := storetest.Alert("tenant", "alert-1", "e", "f", "warning")
	a.Labels["model_id"] = domain.NewStringScalar("cmdb.host")
	a.Labels["model_inst_id"] = domain.NewStringScalar("123")
	a.Labels["bk_biz_id"] = domain.NewStringScalar("2")
	a.Labels["strategy_name"] = domain.NewStringScalar("cpu")
	facts := batchShieldFacts{alerts: map[string]domain.Alert{a.AlertID: a}}
	reader := policyTargets{refs: []onemodel.InstanceRef{{ModelID: "cmdb.host", InstanceID: "123", EntityUID: "cmdb.host|123"}}}
	p := NewPreviewer(nil, facts, reader, nil, nil)
	at := time.Date(2026, 10, 17, 1, 30, 0, 0, time.UTC)
	req := BatchPreviewRequest{Scope: Scope{TenantID: "tenant", Kind: Shield}, Spec: encodeSpec(t, spec), At: &at, AlertIDs: []string{a.AlertID, "missing"}, Fields: []string{"strategy_name", "level"}}
	original := a.Clone()
	res, err := p.PreviewBatch(t.Context(), req)
	if err != nil || len(res.Items) != 2 || !res.Items[0].Matched || res.Items[0].Fields["strategy_name"] != "cpu" || res.Items[1].Evaluated || res.Items[1].Reason != "alert_not_found" {
		t.Fatal(res, err)
	}
	if !reflect.DeepEqual(facts.alerts[a.AlertID], original) {
		t.Fatal("preview mutated alert")
	}
	// 北京时间的时段不能在 UTC 同一钟面时间误命中。
	at = at.Add(8 * time.Hour)
	res, err = p.PreviewBatch(t.Context(), req)
	if err != nil || res.Items[0].Matched || res.Items[0].Reason != "inactive" {
		t.Fatal(res, err)
	}
	at = at.Add(-8 * time.Hour)
	a.ExtraData["strategy_name"] = json.RawMessage(`""`)
	facts.alerts[a.AlertID] = a
	res, err = p.PreviewBatch(t.Context(), req)
	if err != nil || res.Items[0].Matched {
		t.Fatal("explicit empty fell back to labels", res, err)
	}
	a.BKTenantID = "other"
	facts.alerts[a.AlertID] = a
	if _, err := p.PreviewBatch(t.Context(), req); !errors.Is(err, ErrAccess) {
		t.Fatal("cross tenant", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.PreviewBatch(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	for range cap(p.slots) {
		p.slots <- struct{}{}
	}
	if _, err := p.PreviewBatch(t.Context(), req); !errors.Is(err, ErrPreviewCapacity) {
		t.Fatal("capacity", err)
	}
}
