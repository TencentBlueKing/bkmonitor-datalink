// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// collectWithheldLines runs the emitter against a report and returns what it
// wrote.
func collectWithheldLines(report controlplane.WithheldReport) []observability.Observation {
	var written []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		written = append(written, observation)
	})
	observeWithheldObjects(context.Background(), observer, report)
	return written
}

// Every line carries the strategy, what happened to it, and why.
//
// The strategy is the point of these lines: the counts already say how many
// are withheld and under which disposition, and an operator cannot turn a
// count into something to look at.
func TestEachWithheldLineNamesTheStrategyAndWhy(t *testing.T) {
	written := collectWithheldLines(controlplane.WithheldReport{Lines: []controlplane.ObjectDisposition{
		{SourceID: "7", Scope: "PLAN", Disposition: controlplane.DispositionConfigRejected,
			Reason: "NO_DATA_CONFIG_INVALID", FieldPath: "item.no_data_config"},
		{SourceID: "9", Scope: "LEVEL", LevelID: 3, Disposition: controlplane.DispositionUnsupported,
			Reason: "ALGORITHM_NOT_MIGRATED", FieldPath: "level.detect_plan.algorithms"},
	}})

	if len(written) != 2 {
		t.Fatalf("lines = %d, want one per record", len(written))
	}
	for _, observation := range written {
		if observation.Component != observability.ComponentControlPlane {
			t.Fatalf("component = %q, want the control plane", observation.Component)
		}
		if observation.Stage != observability.StageSourceWithheld {
			t.Fatalf("stage = %q, want %q", observation.Stage, observability.StageSourceWithheld)
		}
		// A withheld strategy is a decision the round made and recorded, not a
		// failure of the round. Reporting it as one would put a leader that is
		// working correctly into a permanently failing state, and whoever
		// watches failures would learn to ignore these.
		if observation.Result != observability.ResultSuccess {
			t.Fatalf("result = %q, want success: a recorded refusal is not a failed round", observation.Result)
		}
		if observation.SourceWithheld == nil {
			t.Fatalf("observation carries no withheld facts: %+v", observation)
		}
	}
	plan, level := written[0], written[1]
	if plan.Trace.StrategyID != "7" || plan.Trace.TerminalScope != "PLAN" {
		t.Fatalf("plan line trace = %+v, want strategy 7 at PLAN scope", plan.Trace)
	}
	if plan.SourceWithheld.Disposition != string(controlplane.DispositionConfigRejected) ||
		plan.SourceWithheld.Reason != "NO_DATA_CONFIG_INVALID" {
		t.Fatalf("plan line facts = %+v, want the disposition and reason it was withheld under", plan.SourceWithheld)
	}
	// And the field. A reason names a class of refusal; a strategy document has
	// a few hundred keys, and which one it was is the difference between a line
	// an operator can act on and one they have to reproduce offline. The
	// compiler has always known -- it was dropped on the way out.
	if plan.SourceWithheld.Field != "item.no_data_config" ||
		level.SourceWithheld.Field != "level.detect_plan.algorithms" {
		t.Fatalf("withheld fields = %q and %q, want the two the compiler reported",
			plan.SourceWithheld.Field, level.SourceWithheld.Field)
	}
	if level.Trace.LevelID != "3" {
		t.Fatalf("level line level_id = %q, want 3", level.Trace.LevelID)
	}
	// A PLAN record has no level, and a zero would read as level zero rather
	// than as "the whole strategy".
	if plan.Trace.LevelID != "" {
		t.Fatalf("plan line carries level_id %q; a plan refusal is not about a level", plan.Trace.LevelID)
	}
}

// The cut is said once, on the last line.
//
// Said on every line it would be the same fact len(Lines) times, and a counter
// summing the field would report one round's drop once per line - a number
// that is wrong by a factor of the budget and still moves, which is the
// direction nobody checks.
func TestTheDroppedCountIsReportedOnceOnTheLastLine(t *testing.T) {
	written := collectWithheldLines(controlplane.WithheldReport{
		Lines: []controlplane.ObjectDisposition{
			{SourceID: "1", Scope: "PLAN", Disposition: controlplane.DispositionConfigRejected, Reason: "PLAN_INVALID"},
			{SourceID: "2", Scope: "PLAN", Disposition: controlplane.DispositionConfigRejected, Reason: "PLAN_INVALID"},
			{SourceID: "3", Scope: "PLAN", Disposition: controlplane.DispositionConfigRejected, Reason: "PLAN_INVALID"},
		},
		Dropped: 12,
	})

	if len(written) != 3 {
		t.Fatalf("lines = %d, want 3", len(written))
	}
	for index, observation := range written[:2] {
		if observation.SourceWithheld.Dropped != 0 {
			t.Fatalf("line %d reports Dropped = %d; only the last line carries the cut",
				index, observation.SourceWithheld.Dropped)
		}
	}
	if got := written[2].SourceWithheld.Dropped; got != 12 {
		t.Fatalf("last line Dropped = %d, want the 12 the budget cut", got)
	}
}

// A round where nothing changed writes nothing at all.
//
// Not an empty line, not a line saying zero: the steady state is silence, and
// that is what keeps a deployment with two hundred rejected strategies from
// writing two hundred lines every few seconds.
func TestARoundWithNoChangesWritesNoLines(t *testing.T) {
	if written := collectWithheldLines(controlplane.WithheldReport{}); len(written) != 0 {
		t.Fatalf("lines = %+v, want none", written)
	}
}

// The emitter does not depend on there being an observer.
//
// It runs on the leader's refresh path, and a refresh that panicked because
// nothing was listening would take the control plane down over a log line.
func TestWithheldLinesSurviveAnAbsentObserver(t *testing.T) {
	observeWithheldObjects(context.Background(), nil, controlplane.WithheldReport{
		Lines: []controlplane.ObjectDisposition{
			{SourceID: "1", Scope: "PLAN", Disposition: controlplane.DispositionConfigRejected, Reason: "PLAN_INVALID"},
		},
		Dropped: 1,
	})
}
