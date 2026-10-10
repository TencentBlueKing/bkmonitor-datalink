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

// The committed line of a Slot that did not answer whole says why and
// where: the cause, its reason, the strategy, and the Level or the query.
// A completion without a cause carries none of those keys.
func TestTheCommittedLineNamesTheCauseAndWhereItWasFound(t *testing.T) {
	render := func(observation Observation) map[string]any {
		var output bytes.Buffer
		limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
		if err != nil {
			t.Fatal(err)
		}
		policy, err := NewBoundedLogPolicy(limiter)
		if err != nil {
			t.Fatal(err)
		}
		NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), observation)
		if output.Len() == 0 {
			return nil
		}
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatalf("decode: %v; log=%s", err, output.String())
		}
		return event
	}
	committed := Observation{Component: ComponentProgress, Stage: StageProgressCommitted, Result: ResultSuccess,
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "PRIMARY_INPUT_UNAVAILABLE",
		ProgressCompletionReason: "QUERY_UNAVAILABLE",
		ProgressCompletionScope: &CompletionScopeFacts{TenantID: "system", BusinessID: "2", StrategyID: "4101", LevelID: 1, HasLevel: true,
			PhysicalQuery: "physical-query-1"}}
	event := render(committed)
	if event == nil {
		t.Fatal("the committed line was not written")
	}
	for key, want := range map[string]any{"completion_cause": "PRIMARY_INPUT_UNAVAILABLE", "completion_reason": "QUERY_UNAVAILABLE",
		"completion_strategy": "4101", "completion_business": "2", "completion_level": float64(1), "completion_query": "physical-query-1"} {
		if event[key] != want {
			t.Errorf("%s = %v, want %v; line %v", key, event[key], want, event)
		}
	}
	whole := committed
	whole.ProgressCompletionKind, whole.ProgressCompletionCause, whole.ProgressCompletionReason, whole.ProgressCompletionScope = "FULL_COMPLETED", "", "", nil
	if event := render(whole); event != nil {
		for _, key := range []string{"completion_cause", "completion_reason", "completion_strategy", "completion_level", "completion_query"} {
			if _, present := event[key]; present {
				t.Errorf("a whole completion carries %s: %v", key, event)
			}
		}
	}
}

// Every word NormalizeReason keeps is counted by NormalizedReasonCount, the
// bound a counter labelled by a normalized reason declares: the words of
// every catalogue it keeps, and the three it answers with outside them. A
// catalogue added to the one and not the other is a counter whose series
// can outgrow what it declared.
func TestTheReasonBoundCountsEveryWordNormalizeReasonKeeps(t *testing.T) {
	kept := map[ReasonCode]bool{ReasonNone: true, ReasonNotReported: true, ReasonOther: true}
	catalogues := []map[ReasonCode]struct{}{commonReasonSet, resourceReasonSet, activationFailureReasonSet, viewStreamReasonSet,
		schedulerDecisionReasonSet, effectiveMaintenanceReasonSet, absentCloseReasonSet, completionAttributionReasonSet}
	words := []ReasonCode{}
	for _, catalogue := range catalogues {
		for word := range catalogue {
			words = append(words, word)
		}
	}
	for word := range contractObservationReasonSet {
		words = append(words, ReasonCode(word))
	}
	for _, word := range words {
		// none is one of the three: kept for a result that allows it.
		if word != ReasonNone && NormalizeReason(word, ResultDegraded) != word {
			t.Fatalf("%s is in a catalogue and NormalizeReason does not keep it", word)
		}
		kept[word] = true
	}
	if len(kept) > NormalizedReasonCount() {
		t.Fatalf("NormalizeReason keeps %d words, the bound counts %d", len(kept), NormalizedReasonCount())
	}
}
