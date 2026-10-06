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
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// A turnaway line carries the predicate that decided and both values it
// compared. The counter it accompanies says only that a cohort was turned
// away; without the verdict and the two deadlines a rising 10s count cannot
// be told apart between an unknown deadline, a tail that expires earlier and
// a lost tie, and the next step would be a mechanism guessed from a counter.
func TestLoggingObserverWritesDispatchTurnawayVerdictAndBothDeadlines(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageDispatchTurnaway, Result: ResultSuccess,
		Trace: TraceFields{QueryGroupKey: "qg-short"},
		DispatchTurnaway: &DispatchTurnawayFacts{
			Outcome: "normal_queue_full", Cohort: "10s", Verdict: "tail_earlier",
			DeadlineUnixMilli: 1_700_000_010_000, KeptQueryGroup: "qg-tail", KeptCohort: "10s",
			KeptDeadlineUnixMilli: 1_700_000_009_000, QueueLength: 1536, QueueCapacity: 1536,
		},
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode dispatch turnaway log: %v; log=%s", err, output.String())
	}
	if event["stage"] != StageDispatchTurnaway || event["component"] != string(ComponentScheduler) {
		t.Fatalf("stage/component = %v/%v, want %s/%s: an unlisted stage is normalised to other and the line loses its name",
			event["stage"], event["component"], StageDispatchTurnaway, ComponentScheduler)
	}
	facts, ok := event["dispatch_turnaway"].(map[string]any)
	if !ok {
		t.Fatalf("event lacks dispatch_turnaway facts: %#v", event)
	}
	want := map[string]any{
		"outcome": "normal_queue_full", "cohort": "10s", "verdict": "tail_earlier",
		"deadline_ms": float64(1_700_000_010_000), "kept_query_group": "qg-tail", "kept_cohort": "10s",
		"kept_deadline_ms": float64(1_700_000_009_000), "queue_length": float64(1536), "queue_capacity": float64(1536),
	}
	for field, value := range want {
		if facts[field] != value {
			t.Fatalf("dispatch_turnaway[%q] = %#v, want %#v; facts=%#v", field, facts[field], value, facts)
		}
	}
	if event["query_group_key"] != "qg-short" {
		t.Fatalf("query_group_key = %#v, want the turned-away Query Group so the scoped limiter keys on it; event=%#v", event["query_group_key"], event)
	}
}
