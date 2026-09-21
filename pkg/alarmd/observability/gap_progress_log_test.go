// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The sentence the page owes a reader is "releasing needs N consecutive
// complete rounds, currently k". This pins that both numbers reach the line,
// beside the scope they belong to and the reason the guard is held: k without
// N answers nothing, because N is the largest history requirement across that
// strategy's Levels and so differs strategy by strategy.
func TestGapScopeProgressLineCarriesBothNumbers(t *testing.T) {
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 100})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	observer.Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageGapGuardProgress, Result: ResultSuccess,
		Trace: TraceFields{StrategyID: "1074"},
		GapProgress: &GapProgressFacts{
			Scope: "plan", Status: "GAPPED", Reason: "CONFIG_DRIFT", Required: 5, Observed: 0,
			Progress: "none",
		},
	})

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want one; log=%s", len(lines), output.String())
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{
		"stage":                   string(StageGapGuardProgress),
		"gap_scope":               "plan",
		"gap_scope_status":        "GAPPED",
		"gap_scope_reason":        "CONFIG_DRIFT",
		"gap_full_slots_required": float64(5),
		// Zero, written rather than omitted. A guard that has not had one
		// complete round since it was raised is the state somebody is looking
		// for, and an omitted field reads as a line that does not report k.
		"gap_full_slots_observed": float64(0),
		// The same word the metric label carries, so a reader moving between
		// the chart and the line is reading one vocabulary rather than two.
		"gap_progress": "none",
		"strategy_id":  "1074",
	} {
		if line[field] != want {
			t.Fatalf("line[%q] = %#v, want %#v; line=%#v", field, line[field], want, line)
		}
	}
}
