// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func envelopeOfShape(t *testing.T, points, levels int) int {
	t.Helper()
	fingerprint := strings.Repeat("ab", 32)
	history := make([]execution.StateHistoryPoint, points)
	for i := range history {
		facts := make([]execution.StateLevelFact, levels)
		for l := range facts {
			facts[l] = execution.StateLevelFact{LevelID: uint32(l + 1),
				DetectFingerprint: fingerprint, Result: execution.LevelFactNormal}
		}
		history[i] = execution.StateHistoryPoint{RecordID: fmt.Sprintf("%064x", i),
			SourceTime: 1758400000 + int64(i)*60, Levels: facts}
	}
	mutations := make([]execution.RuntimeLevelStateMutation, levels)
	for l := range mutations {
		mutations[l] = execution.RuntimeLevelStateMutation{LevelID: uint32(l + 1),
			LevelStateCompatibility: strings.Repeat("C", 64), HistoryCompleteness: execution.HistoryWarming,
			GapReasonCode:        execution.ReasonCode(strings.Repeat("G", 64)),
			WarmupRequirementRef: strings.Repeat("W", 64), LastProcessedEventTime: math.MaxInt64}
	}
	// Filled to the widest each field can be, not left at its zero value. The
	// bound has to hold for the record the store actually writes, and a fixture
	// that leaves the identity, the apply version, the digests and the Level
	// refs empty measures a record no Plan produces - so margin that exists
	// only because the fixture was thin reads as margin in the bound.
	guard := execution.StateGuardFact{
		Status: execution.HistoryWarming, ReasonCode: execution.ReasonCode(strings.Repeat("R", 64)),
		WarmupRequirementRef: strings.Repeat("w", 64),
	}
	raw, err := json.Marshal(runtimeEnvelope{
		Schema: executionStateSchemaV2,
		Identity: execution.StateKeyIdentity{
			Plan: execution.PlanIdentity{TenantID: strings.Repeat("t", 64),
				BusinessID: strings.Repeat("9", 20), StrategyID: strings.Repeat("9", 20)},
			StateGeneration:      execution.StateGeneration(strings.Repeat("g", 64)),
			SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("s", 64)),
		},
		BlobRevision: math.MaxUint64,
		ApplyVersion: execution.ApplyVersion{StateApplyEpoch: math.MaxUint64, EvaluationTime: math.MaxInt64,
			SlotDigest: execution.SlotIdentityDigest(strings.Repeat("d", 64))},
		MutationDigest: execution.MutationDigest(strings.Repeat("m", 64)),
		LastEventTime:  math.MaxInt64,
		SeriesGuard:    &guard,
		Levels:         mutations, History: history,
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(raw)
}

// The bound has to be above what the encoder produces, for every shape the
// compiler can admit, or it is not a bound.
//
// It is checked against the real encoder rather than against remembered
// figures: the record is written by encoding/json over these structs, so a
// field added to either - which is how the shape last changed - moves the
// encoder and leaves arithmetic written elsewhere behind. Checking against the
// thing itself is what makes that a failure here instead of a write refused in
// production.
func TestTheEnvelopeBoundIsAboveWhatTheEncoderWrites(t *testing.T) {
	for _, levels := range []int{1, 2, 3, 8} {
		for _, points := range []int{1, 100, 1469, 2238} {
			bound, err := RuntimeEnvelopeUpperBoundV2(levels, points)
			if err != nil {
				t.Fatal(err)
			}
			if actual := envelopeOfShape(t, points, levels); actual > bound {
				t.Fatalf("levels=%d points=%d: encoder wrote %d, bound says %d; a bound below the encoder "+
					"admits a Plan whose every write is refused", levels, points, actual, bound)
			}
		}
	}
}

// And the ceiling it derives must be a shape the store will actually accept,
// which is the property the compile-time refusal rests on.
func TestTheDerivedCeilingIsAShapeTheStoreAccepts(t *testing.T) {
	const valueBytes = 512 << 10
	for _, levels := range []int{1, 2, 3, 8} {
		ceiling, err := MaxRuntimeEnvelopePoints(levels, valueBytes)
		if err != nil {
			t.Fatal(err)
		}
		if ceiling <= 0 {
			t.Fatalf("levels=%d: no ceiling derived", levels)
		}
		if actual := envelopeOfShape(t, ceiling, levels); actual > valueBytes {
			t.Fatalf("levels=%d: a record at the derived ceiling of %d points is %d bytes, over the %d "+
				"the store accepts", levels, ceiling, actual, valueBytes)
		}
		// The ceiling is below the point count the configuration states, which
		// is the whole finding: the stated limit cannot be reached in this
		// representation, so deriving it from the representation is not a
		// tightening of an existing bound but the first bound there is.
		if ceiling >= 4096 {
			t.Fatalf("levels=%d: derived ceiling %d is at or above the configured 4096, so this "+
				"representation would not have been the binding constraint", levels, ceiling)
		}
	}
}
