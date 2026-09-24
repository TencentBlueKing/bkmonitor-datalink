// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The object-identity report reaches the log as one line per plan, reason
// and window, carrying the two sides that disagreed as coordinates: the
// dimension pairs expected against the dimension names seen, or the keys
// the record built against a sample of the keys the target names. Without
// that line the counter says a strategy rejects everything and nothing says
// which of the three fixes applies.
func TestObjectIdentityReportsAreWrittenAsLogLines(t *testing.T) {
	var buffer bytes.Buffer
	logger := observability.New("trigger", &buffer)
	clock := time.Unix(1700000000, 0)
	reporter := newIdentityReporter(logger, func() time.Time { return clock })
	filters := seriesAdmissionFilters(nil, reporter)
	if len(filters) != 2 {
		t.Fatalf("filters = %d, want the two target forms' filters", len(filters))
	}
	target, ok := filters[0].(admission.TargetScopeFilter)
	if !ok || target.Reporter != reporter {
		t.Fatalf("the target scope filter does not carry the reporter: %#v", filters[0])
	}
	plan := admission.PlanContext{TenantID: "system", BusinessID: "2", StrategyID: "77", TargetScope: &admission.TargetScope{
		Groups: []admission.TargetScopeGroup{{Conditions: []admission.TargetScopeCondition{{
			Field: admission.TargetScopeObjectModelInst, Method: admission.TargetScopeInclude,
			Keys:           map[string]struct{}{"switch|1": {}, "switch|2": {}, "switch|3": {}, "switch|4": {}},
			IdentityFields: [][2]string{{"cw_object_model_id", "cw_object_model_inst_id"}},
		}}}},
	}}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}}, filters)

	missing := chain.Enrich(map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"10.0.0.1"`), "device": json.RawMessage(`"eth0"`)})
	if admitted, _, reason := chain.Admit(plan, &missing); admitted || reason != "object_identity_missing" {
		t.Fatalf("decision = %v/%s", admitted, reason)
	}
	unmatched := chain.Enrich(map[string]json.RawMessage{"cw_object_model_id": json.RawMessage(`"sw"`), "cw_object_model_inst_id": json.RawMessage(`9`)})
	if admitted, _, reason := chain.Admit(plan, &unmatched); admitted || reason != "object_identity_unmatched" {
		t.Fatalf("decision = %v/%s", admitted, reason)
	}
	// Repeats inside the window are counted, not written.
	chain.Admit(plan, &missing)
	chain.Admit(plan, &unmatched)

	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want one per reason:\n%s", len(lines), buffer.String())
	}
	var missingLine, unmatchedLine map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &missingLine); err != nil {
		t.Fatalf("decode %s: %v", lines[0], err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &unmatchedLine); err != nil {
		t.Fatalf("decode %s: %v", lines[1], err)
	}
	for field, want := range map[string]any{
		"stage": "series_admission", "result": "rejected", "reason": "object_identity_missing",
		"bk_tenant_id": "system", "bk_biz_id": "2", "strategy_id": "77", "rejections": float64(1),
		"expected_dimension_pairs": "cw_object_model_id|cw_object_model_inst_id", "record_dimensions": "bk_target_ip,device",
	} {
		if got := missingLine[field]; got != want {
			t.Errorf("missing line %s = %v, want %v", field, got, want)
		}
	}
	for field, want := range map[string]any{
		"reason": "object_identity_unmatched", "record_keys": "sw|9", "target_keys_sample": "switch|1,switch|2,switch|3",
	} {
		if got := unmatchedLine[field]; got != want {
			t.Errorf("unmatched line %s = %v, want %v", field, got, want)
		}
	}
	if _, present := unmatchedLine["expected_dimension_pairs"]; present {
		t.Error("the unmatched line carries the missing line's fields")
	}
	if newIdentityReporter(nil, nil) != nil {
		t.Error("a reporter was built without a logger to write to")
	}
}
