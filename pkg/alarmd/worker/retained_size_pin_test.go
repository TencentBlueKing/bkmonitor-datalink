// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// pinnedSeriesResult is one series of a Slot as the output charge sees it:
// one Level outcome and one State mutation with one Level and one point.
func pinnedSeriesResult() execution.PlanEvaluationResult {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "101"}
	series := execution.SeriesIdentityDigest(strings.Repeat("4f", 32))
	anchor := execution.RecordAnchor{RecordID: strings.Repeat("b", 32), SourceTime: 1_787_999_940}
	return execution.PlanEvaluationResult{Plan: plan,
		LevelOutcomes: []execution.LevelOutcome{{Plan: plan, LevelID: 1, SeriesIdentityDigest: series, Record: anchor,
			Outcome: execution.LevelOutcomeUnknown, ReasonCode: "QUERY_TIMEOUT"}},
		StateResults: []execution.StateEvaluation{{Mutation: execution.StateMutation{
			Identity:        execution.StateKeyIdentity{Plan: plan, StateGeneration: "g1", SeriesIdentityDigest: series},
			ApplyVersion:    execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 1_788_000_000, SlotDigest: "slot-digest"},
			AffectedRecords: []execution.RecordAnchor{anchor},
			Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat-1",
				HistoryCompleteness: execution.HistoryGapped, GapReasonCode: "QUERY_TIMEOUT",
				WarmupRequirementRef: "warmup-1", LastProcessedEventTime: 1_787_999_940}},
			Points: []execution.StateHistoryPoint{{RecordID: anchor.RecordID, SourceTime: anchor.SourceTime,
				Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "fp-1", Result: "NORMAL"}}}},
		}}},
	}
}

// Every series of a Slot keeps these structs in memory until the Slot
// completes, and retained accounting charges their sizes against the
// group's share: a field added to any of them is paid once per series, twice
// on the output charge. The sizes here are the ones a group at the edge of
// its share was measured at; a change that grows one is a capacity change
// and has to come with its own estimate, not arrive with a field.
func TestThePerSeriesRetainedStructsKeepTheirSize(t *testing.T) {
	for _, pinned := range []struct {
		name      string
		got, want uintptr
	}{
		{"LevelOutcome", unsafe.Sizeof(execution.LevelOutcome{}), 160},
		// 80 until LastRealPointTime, which has no alignment gap to sit in:
		// eight bytes a Level, a no-data end's clock (the recovery spec,
		// section 2.2). The output charge below moves by twice that.
		{"RuntimeLevelStateMutation", unsafe.Sizeof(execution.RuntimeLevelStateMutation{}), 88},
		{"RuntimeLevelStateView", unsafe.Sizeof(execution.RuntimeLevelStateView{}), 88},
		{"StateEvaluation", unsafe.Sizeof(execution.StateEvaluation{}), 304},
		{"StateMutation", unsafe.Sizeof(execution.StateMutation{}), 256},
		{"StateHistoryPoint", unsafe.Sizeof(execution.StateHistoryPoint{}), 48},
		{"StateLevelFact", unsafe.Sizeof(execution.StateLevelFact{}), 40},
		// Once per gap scope or per Plan, not per series; pinned all the
		// same, so they cannot grow without saying so either.
		{"GapScopeState", unsafe.Sizeof(execution.GapScopeState{}), 72},
		{"EvaluationResult", unsafe.Sizeof(execution.EvaluationResult{}), 192},
		// 424 until NoDataEnds, a slice header a Plan: the index updates of
		// a Plan written to end on no data, empty for every other.
		{"PlanEvaluationResult", unsafe.Sizeof(execution.PlanEvaluationResult{}), 448},
	} {
		if pinned.got != pinned.want {
			t.Errorf("%s is %d bytes, pinned at %d", pinned.name, pinned.got, pinned.want)
		}
	}
	// The same, as the output charge adds it up for a series after the
	// Slot's first: 2528 bytes, what the release before the origin fields
	// charged for this fixture, and 2544 since LastRealPointTime - 16 bytes,
	// 0.6 percent, for its one Level.
	current := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{pinnedSeriesResult()}}
	next := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{pinnedSeriesResult()}}
	if got := newEffectBytes(current, next); got != 2544 {
		t.Errorf("one series charges %d output bytes, pinned at 2544", got)
	}
}

// An origin rides in the padding: a carried outcome and a Level holding a
// guard with its origin charge what they charge without one.
func TestACarriedOriginCostsASeriesNoBytes(t *testing.T) {
	origin := execution.StampReasonOrigin(1_787_999_880, "QUERY_TIMEOUT")
	carried := pinnedSeriesResult()
	carried.LevelOutcomes[0].ReasonCarried = true
	carried.LevelOutcomes[0] = carried.LevelOutcomes[0].WithReasonOrigin(origin)
	carried.StateResults[0].Mutation.Levels[0] = carried.StateResults[0].Mutation.Levels[0].WithReasonOrigin(origin)
	if carried.LevelOutcomes[0].ReasonOrigin() != origin || carried.StateResults[0].Mutation.Levels[0].ReasonOrigin() != origin {
		t.Fatalf("origin not kept: outcome %+v, Level %+v", carried.LevelOutcomes[0].ReasonOrigin(),
			carried.StateResults[0].Mutation.Levels[0].ReasonOrigin())
	}
	current := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{pinnedSeriesResult()}}
	plain := newEffectBytes(current, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{pinnedSeriesResult()}})
	with := newEffectBytes(current, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{carried}})
	if with != plain {
		t.Fatalf("a series with a carried origin charges %d bytes, without %d", with, plain)
	}
}
