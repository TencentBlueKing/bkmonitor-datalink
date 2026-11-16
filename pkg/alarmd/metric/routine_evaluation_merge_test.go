// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// routineEvaluation is one routine evaluation line of a series of the Plan:
// a success with algorithm, input and Level facts, the shape the worker
// merges (observability.MergeRoutineEvaluation).
func routineEvaluation(series string, abnormal bool) observability.Observation {
	result := observability.AlgorithmEvaluationResultNormal
	outcome := "NORMAL"
	if abnormal {
		result, outcome = observability.AlgorithmEvaluationResultAbnormal, "ABNORMAL"
	}
	return observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		Result: observability.ResultSuccess, Operation: observability.OperationNormal,
		Duration: 3 * time.Millisecond, DurationKnown: true, Counts: observability.Counts{Records: 30},
		EvaluationRecordsKnown: true,
		EvaluationOwner:        observability.CostPlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "7"},
		Trace:                  observability.TraceFields{StrategyID: "7", BusinessID: "2", DimensionIdentityDigest: series},
		AlgorithmEvaluations: []observability.AlgorithmEvaluationFact{{
			SourceAlgorithmFamily: observability.AlgorithmFamilyPingUnreachable,
			DetectorKind:          observability.AlgorithmDetectorKindThreshold, Result: result,
		}},
		AlgorithmInputs: []observability.AlgorithmInputFact{{
			SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio,
			DetectorKind:          observability.AlgorithmDetectorKindSimpleRingRatio,
			InputName:             observability.AlgorithmInputNameHistory,
			DependencyPoint:       observability.AlgorithmDependencyPointPrevious,
			Result:                observability.AlgorithmInputResultAvailable,
		}},
		LevelOutcomes: []observability.LevelOutcomeFact{{Outcome: outcome, Count: 1}},
	}
}

// Every metric read from a Plan's routine evaluation lines is the same over
// the one line they are merged into as over the lines themselves: counts,
// records, algorithm and Level facts, busy time.
func TestRoutineEvaluationsMergedReadAsTheLinesTheyWereMergedFrom(t *testing.T) {
	lines := []observability.Observation{
		routineEvaluation("s1", false), routineEvaluation("s2", false), routineEvaluation("s3", true),
		routineEvaluation("s4", false), routineEvaluation("s5", true),
	}
	each, merged := NewRecorder(BuildInfo{}), NewRecorder(BuildInfo{})
	var line observability.Observation
	for _, observation := range lines {
		each.Observe(context.Background(), observation)
		observability.MergeRoutineEvaluation(&line, observation)
	}
	if line.Counts.Evaluations != int64(len(lines)) || line.Trace.DimensionIdentityDigest != "" {
		t.Fatalf("merged line counts %d evaluations of series %q, want %d of none", line.Counts.Evaluations,
			line.Trace.DimensionIdentityDigest, len(lines))
	}
	merged.Observe(context.Background(), line)
	// Every family but the gauges and the process's own runtime readings,
	// the lines' counts and their durations' histograms: a gauge here is when
	// a line was last observed, and the runtime's families (go_, process_,
	// alarmd_go_), counters among them, are the process reading itself, which
	// moves between the two scrapes whatever was observed.
	strip := func(text string) string {
		gauges := map[string]bool{}
		for _, row := range strings.Split(text, "\n") {
			if fields := strings.Fields(row); len(fields) == 4 && fields[0] == "#" && fields[1] == "TYPE" && fields[3] == "gauge" {
				gauges[fields[2]] = true
			}
		}
		var kept []string
		for _, row := range strings.Split(text, "\n") {
			if row == "" || strings.HasPrefix(row, "#") {
				continue
			}
			name, _, _ := strings.Cut(row, "{")
			name, _, _ = strings.Cut(name, " ")
			runtime := strings.HasPrefix(name, "go_") || strings.HasPrefix(name, "process_") || strings.HasPrefix(name, "alarmd_go_")
			if !gauges[name] && !runtime {
				kept = append(kept, row)
			}
		}
		return strings.Join(kept, "\n")
	}
	if got, want := strip(scrape(t, merged)), strip(scrape(t, each)); got != want {
		gotRows, wantRows := strings.Split(got, "\n"), strings.Split(want, "\n")
		for index := range min(len(gotRows), len(wantRows)) {
			if gotRows[index] != wantRows[index] {
				t.Fatalf("merged line reads %q where the lines read %q", gotRows[index], wantRows[index])
			}
		}
		t.Fatalf("merged line reads %d rows, the lines %d", len(gotRows), len(wantRows))
	}
}
