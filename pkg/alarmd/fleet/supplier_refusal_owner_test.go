// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// refusedRounds tracks rounds of one Query Group refused at 400 with the
// provider's rejection class, and returns its attributed row.
func refusedRounds(t *testing.T, queryGroup, class string) Anomaly {
	t.Helper()
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(100 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			QueryFailure: &observability.QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_REJECTED",
				Detail: "http_status=400", HTTPStatus: 400,
				Rejection: &observability.QueryRejection{Class: class, TraceID: refusedTraceID}},
			Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Millisecond)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "QUERY_REJECTED",
			Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Minute)
	}
	row, tracked := tracker.TrackedRow(queryGroup)
	if !tracked {
		t.Fatalf("%s is not tracked", queryGroup)
	}
	list := []Anomaly{row}
	Attribute(list, at.at)
	return list[0]
}

// A refusal the provider classed as the storage's per-query sample limit is
// the strategy's query as written, not a refusal whose owner nobody can
// name: the strategy cannot be evaluated until its owner narrows it. A
// refusal classed by its status alone stays undetermined, and so does a
// PromQL parse error: a multi-query expression's PromQL is text alarmd
// composed from the owner's queries, the failure does not say which kind
// the query was, and a composed text's parse error may be alarmd's.
func TestARefusalTheProviderClassedAsTheQuerysOwnIsTheStrategys(t *testing.T) {
	for _, class := range []string{"VM_SAMPLE_LIMIT"} {
		row := refusedRounds(t, "qg-"+class, class)
		if row.Finding.Check != CheckPlanUnevaluable || row.Finding.Owner != OwnerStrategy {
			t.Errorf("%s: check %s owner %s, want PLAN_UNEVALUABLE, the strategy's", class, row.Finding.Check, row.Finding.Owner)
		}
		if words := standingOf(row); words.Action != ActionStrategyEdit {
			t.Errorf("%s: action %s, want the strategy owner's edit", class, words.Action)
		}
	}
	for _, class := range []string{"HTTP_400", "INVALID_QUERY", "PROMQL_PARSE"} {
		if row := refusedRounds(t, "qg-"+class, class); row.Finding.Check != CheckQueryRefused || row.Finding.Owner != OwnerUndetermined {
			t.Errorf("%s: check %s owner %s, want QUERY_REFUSED, undetermined", class, row.Finding.Check, row.Finding.Owner)
		}
	}
}

// A skip a cooldown held, read after the object left the pool, is the
// consequence of the line the cooldown's failure puts it on when that line
// is not this deployment's: a query target that does not exist cooled the
// object, and its skips read as DETECTION_ABANDONED, alarmd's. Through the
// tracker: the failure, the cooldown-held skip, the record it publishes.
// A cooldown whose failure lands on this deployment's own line, and a
// cooldown-held record that names no failure (an older build's), stay on
// DETECTION_ABANDONED.
func TestACooldownSkipAStrategySideFailureCausedIsNotDetectionAbandoned(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	held := &observability.HeldByFacts{Decision: "query_cooldown", QueryCooldownFailures: 7}
	tracker.Observe(context.Background(), observability.Observation{
		QueryFailure: &observability.QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE",
			Detail: "response=status_space_table_id_field_is_not_exists", HTTPStatus: 400},
		Trace: observability.TraceFields{QueryGroupKey: "qg-missing", StrategyID: "4101", BusinessID: "2", EvaluationTime: 100},
	})
	at.at = at.at.Add(time.Minute)
	skip := completion("qg-missing", "GAP_SKIPPED", "4101")
	skip.HeldBy = held
	tracker.Observe(context.Background(), skip)
	record := tracker.GapSkips()["qg-missing"]
	if record.HeldByFailure == nil || record.HeldByFailure.Detail != "response=status_space_table_id_field_is_not_exists" {
		t.Fatalf("record = %+v, want the cooldown's failure kept on it", record)
	}

	view := &View{
		PerReplica: []ReplicaView{{Replica: "pod-a", StartedAt: now.Add(-time.Hour)}},
		GapSkips: map[string]SkippedSpan{
			"qg-missing": record,
			// The cooldown's failure is this deployment's own store not answering.
			"qg-ours": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: record.At, Replica: "pod-a", HeldBy: "query_cooldown",
				HeldByFailure: &FailureRef{Code: "STATE_READ_TIMEOUT", Source: FailureFromPoolRecord}},
			// An older build's record: held by the cooldown, no failure.
			"qg-older": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: record.At, Replica: "pod-a", HeldBy: "query_cooldown"},
		}}
	reading := at.at.Add(time.Minute)
	rows, _ := skippedRows(view, map[string]struct{}{}, reading)
	checks := map[string]Anomaly{}
	for _, row := range rows {
		checks[row.QueryGroup] = row
	}
	if row := checks["qg-missing"]; row.Finding.Check != CheckQueryTargetMissing || row.Finding.Owner != OwnerStrategy ||
		row.Loss != LossAfterCooldown || row.Finding.Group != "response=status_space_table_id_field_is_not_exists" {
		t.Fatalf("strategy-side cooldown record: check %s owner %s loss %s group %q, want QUERY_TARGET_MISSING, the strategy's, folded on the detail",
			row.Finding.Check, row.Finding.Owner, row.Loss, row.Finding.Group)
	}
	for _, queryGroup := range []string{"qg-ours", "qg-older"} {
		if row := checks[queryGroup]; row.Finding.Check != CheckDetectionAbandoned || row.Loss != LossAfterCooldown {
			t.Errorf("%s: check %s loss %s, want DETECTION_ABANDONED / AFTER_COOLDOWN", queryGroup, row.Finding.Check, row.Loss)
		}
	}
}

// Two strategies refused for the storage's per-query sample limit fold into
// one group named for the class: a limit set too low across the cluster is
// one line, not one per strategy, and the row's sentence names both
// remedies. A strategy unevaluable for any other reason still folds on its
// own id.
func TestSampleLimitRefusalsFoldOnTheClassNotPerStrategy(t *testing.T) {
	first := refusedRounds(t, "qg-sample-a", "VM_SAMPLE_LIMIT")
	second := refusedRounds(t, "qg-sample-b", "VM_SAMPLE_LIMIT")
	first.Strategies = []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}
	second.Strategies = []StrategyRef{{StrategyID: "4102", BusinessID: "2"}}
	for _, row := range []Anomaly{first, second} {
		if key := groupKeyOf(row, CheckPlanUnevaluable); key != "VM_SAMPLE_LIMIT" {
			t.Fatalf("%s folds under %q, want the class", row.QueryGroup, key)
		}
	}
	sentence, _, ok := factSentence(CheckPlanUnevaluable, first, now)
	if !ok || !strings.Contains(sentence, "缩小查询") || !strings.Contains(sentence, "上限是否设得过低") {
		t.Fatalf("sentence %q, want both remedies named", sentence)
	}
	budget := Anomaly{QueryGroup: "qg-budget", Kind: KindDegradedRun, ReasonCode: "PLAN_BUDGET_EXCEEDED",
		Strategies: []StrategyRef{{StrategyID: "4103", BusinessID: "2"}}}
	if key := groupKeyOf(budget, CheckPlanUnevaluable); key != "4103" {
		t.Fatalf("a budget refusal folds under %q, want its strategy", key)
	}
}
