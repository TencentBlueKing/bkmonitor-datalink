// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution_test

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The Level contract a record carries says which Plan may read it back, and a
// record is dropped when the two disagree. How many positions the Level
// retains is not part of that: a Level that keeps more positions than it did
// yesterday reads the same record the same way.
//
// It was part of it, and the whole requirement was hashed. Giving 674 Levels a
// few positions of recovery slack changed their warmup reference, so the
// records already written no longer matched the Plans that wrote them; every
// one of those Query Groups reported the mismatch until the state aged out and
// each series began again from an empty window.
//
// Two assertions, because one of them alone proves nothing. The reference must
// equal the one derived over a requirement whose retention is its required
// count -- that is the shape every hash a deployment has stored was derived
// over, so the Levels that never took slack keep the reference they have and
// the ones that did go back to theirs. And it must not equal the one derived
// over the requirement as it stands, or the derivation is reading the field
// again and the first assertion passed only because this fixture has no slack.
func TestTheWarmupReferenceDoesNotReadHowMuchTheLevelRetains(t *testing.T) {
	// A window long enough that the recovery slack is not zero: the span gate
	// only waives it for windows that fit inside the hole tolerance.
	plan := compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":30,"required_anomalies":5,"step_seconds":60}`))
	requirement := plan.Levels()[0].StateRequirement()
	if requirement.RetentionPoints <= requirement.RequiredDetectHistoryPoints {
		t.Fatalf("the fixture Level retains %d of %d required positions, so it takes no slack and cannot "+
			"tell the two derivations apart", requirement.RetentionPoints, requirement.RequiredDetectHistoryPoints)
	}
	refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
	if err != nil {
		t.Fatalf("derive Level contract refs: %v", err)
	}
	levelID := plan.Levels()[0].Definition().LevelID
	derive := func(value strategy.StateRequirement) string {
		digest, err := contract.DeriveCanonicalDigestV2("alarmd-level-warmup-requirement-v1", struct {
			LevelID          uint32                    `json:"level_id"`
			StateRequirement strategy.StateRequirement `json:"state_requirement"`
		}{levelID, value})
		if err != nil {
			t.Fatalf("derive warmup requirement: %v", err)
		}
		return digest
	}
	asStored := derive(strategy.StateRequirement{
		RequiredDetectHistoryPoints: requirement.RequiredDetectHistoryPoints,
		RetentionPoints:             requirement.RequiredDetectHistoryPoints,
	})
	if refs[0].WarmupRequirementRef != asStored {
		t.Fatalf("the warmup reference is %s, want %s: it has to be the value a binary from before the "+
			"retention could differ would have derived, or every Level's records are abandoned once more",
			refs[0].WarmupRequirementRef, asStored)
	}
	if refs[0].WarmupRequirementRef == derive(requirement) {
		t.Fatal("the warmup reference is the one derived over the retention as it stands; raising how much " +
			"a Level keeps would refuse every record it has already written")
	}
}

// And the half that must keep moving: how many positions detection reads is
// part of the contract, because a record written for a shorter window cannot
// answer a longer one.
func TestTheWarmupReferenceStillReadsHowMuchTheLevelDetectsOver(t *testing.T) {
	shorter := compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":30,"required_anomalies":5,"step_seconds":60}`))
	longer := compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":31,"required_anomalies":5,"step_seconds":60}`))
	shorterRefs, err := execution.DeriveRuntimeLevelContractRefs(shorter)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	longerRefs, err := execution.DeriveRuntimeLevelContractRefs(longer)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if shorterRefs[0].WarmupRequirementRef == longerRefs[0].WarmupRequirementRef {
		t.Fatal("a Level reading one more position has the same warmup reference as one that does not; " +
			"the record written for the shorter window would be read as if it answered the longer one")
	}
}
