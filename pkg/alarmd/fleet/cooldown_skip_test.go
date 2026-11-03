// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An object in the cooldown pool alternates between two kinds of round: a
// probe, whose query the backend refuses, and the Slots the cooldown holds
// until they fall past the replay range. Read by each round's own outcome
// the probe put it on the strategy's line and the skip on this deployment's,
// so the same object crossed the page twice a cycle -- 205 of a live
// deployment's 358 cooling objects were on "detection abandoned" at one
// read, the rest on the refusal, by nothing but which round came last. The
// completion says what held the Slot; a skip the cooldown held is read as
// the cooldown is, on every axis the page has.
func TestASkipTheCooldownHeldStaysOnTheFailuresLine(t *testing.T) {
	const group = "qg-cooling"
	d := newDefectTracker(t, group)
	refused := &observability.QueryFailureFacts{Stage: observability.QueryFailureStageProvider,
		Category: observability.QueryFailureCategorySourceBackend, Code: "QUERY_UNAVAILABLE",
		Detail: "response=status_space_table_id_field_is_not_exists"}
	trace := func(slot int64) observability.TraceFields {
		return observability.TraceFields{QueryGroupKey: group, StrategyID: "4101", BusinessID: "2", EvaluationTime: slot}
	}
	probe := func(slot int64) {
		d.tick()
		d.tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
			Result: observability.ResultDegraded, ReasonCode: "QUERY_UNAVAILABLE", QueryFailure: refused, Trace: trace(slot),
		})
		d.tracker.Observe(context.Background(), observability.Observation{
			QueryCooldown: &observability.QueryCooldownFacts{Event: "extended", Failures: 16}, Trace: trace(slot),
		})
		d.tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
			ProgressCompletionReason: "QUERY_UNAVAILABLE", Trace: trace(slot),
		})
	}
	skip := func(slot int64, heldBy string) {
		d.tick()
		observation := observability.Observation{
			ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
			ProgressCompletionReason: "GAP_SKIPPED", Trace: trace(slot),
		}
		if heldBy != "" {
			observation.HeldBy = &observability.HeldByFacts{Decision: heldBy, QueryCooldownFailures: 16}
		}
		d.tracker.Observe(context.Background(), observation)
	}
	row := func() Anomaly {
		t.Helper()
		rows := append(d.tracker.Anomalies(), d.tracker.Demoted()...)
		Attribute(rows, d.at.Now())
		for _, candidate := range rows {
			if candidate.QueryGroup == group {
				return candidate
			}
		}
		t.Fatal("object not listed")
		return Anomaly{}
	}

	probe(100)
	onProbe := row()
	if onProbe.Finding.Check != CheckQueryTargetMissing || onProbe.HeldBy != "" {
		t.Fatalf("after the probe: %s held_by=%q, want QUERY_TARGET_MISSING and no holder", onProbe.Finding.Check, onProbe.HeldBy)
	}
	skip(160, heldByCooldown)
	skip(220, heldByCooldown)
	onSkip := row()
	if onSkip.HeldBy != heldByCooldown {
		t.Fatalf("the skipped round's row held_by=%q, want %q from the completion", onSkip.HeldBy, heldByCooldown)
	}
	if onSkip.Finding.Check != CheckQueryTargetMissing || onSkip.Finding.Owner != OwnerStrategy {
		t.Fatalf("the skipped round's row is under %s/%s, want the failure's line QUERY_TARGET_MISSING/STRATEGY", onSkip.Finding.Check, onSkip.Finding.Owner)
	}
	if onSkip.Finding.Group != onProbe.Finding.Group {
		t.Fatalf("the skipped round folds on %q, the probe on %q: one object, two folds", onSkip.Finding.Group, onProbe.Finding.Group)
	}
	if b := onSkip.Blocked; b == nil || b.Stage != StageQuery || b.Class != ClassRefused || b.Dependency != DependencyQueryBackend ||
		b.Code != "QUERY_UNAVAILABLE" || b.Text != refused.Detail {
		t.Fatalf("the skipped round's reading = %+v, want QUERY / REFUSED / QUERY_BACKEND with the refusal's words", onSkip.Blocked)
	}
	if onSkip.Attribution == AttributionOurs {
		t.Fatalf("a skip the cooldown held counts against this deployment")
	}

	// The next probe: the same line, and the holder is gone from the row
	// because the latest round is not the skip.
	probe(280)
	again := row()
	if again.Finding.Check != CheckQueryTargetMissing || again.HeldBy != "" {
		t.Fatalf("after the next probe: %s held_by=%q", again.Finding.Check, again.HeldBy)
	}

	// A skip nothing held, or something other than the cooldown held, is
	// read as before: this deployment gave the Slot up.
	for _, heldBy := range []string{"", observability.HeldByReadinessDeferred} {
		other := newDefectTracker(t, group)
		for slot := int64(400); slot <= 520; slot += 60 {
			other.tick()
			observation := observability.Observation{
				ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
				ProgressCompletionReason: "GAP_SKIPPED", Trace: trace(slot),
			}
			if heldBy != "" {
				observation.HeldBy = &observability.HeldByFacts{Decision: heldBy}
			}
			other.tracker.Observe(context.Background(), observation)
		}
		rows := append(other.tracker.Anomalies(), other.tracker.Undecidable()...)
		Attribute(rows, other.at.Now())
		if len(rows) != 1 || rows[0].Finding.Check != CheckDetectionAbandoned || rows[0].HeldBy != heldBy {
			t.Fatalf("held by %q: rows=%+v, want DETECTION_ABANDONED with held_by %q", heldBy, rows, heldBy)
		}
	}
}
