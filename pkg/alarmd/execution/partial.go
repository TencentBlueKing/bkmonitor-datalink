// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "errors"

type PartialPolicy string

const (
	PartialRequiresFull           PartialPolicy = "REQUIRES_FULL"
	PartialProvableAbnormalOnly   PartialPolicy = "PROVABLE_ABNORMAL_ONLY"
	PartialEvidenceOmissionStable string        = "OMISSION_ONLY_RETURNED_RECORDS_STABLE"
)

type PartialProofRef struct {
	EvidenceKind    string
	EvidenceVersion uint32
	RuleID          string
	RuleVersion     uint32
	ProofDigest     string
}

type LevelPartialCapability struct {
	LevelID uint32
	Policy  PartialPolicy
	Proof   *PartialProofRef
}

func validatePartialCapabilities(plan DuePlan) error {
	levels := plan.CompiledPlan.Levels()
	if len(plan.PartialCapabilities) != len(levels) {
		return errors.New("alarmd execution: every compiled Level requires one PARTIAL capability closure")
	}
	seen := make(map[uint32]struct{}, len(levels))
	for _, capability := range plan.PartialCapabilities {
		if capability.LevelID == 0 || !compiledPlanHasLevel(plan.CompiledPlan, capability.LevelID) {
			return errors.New("alarmd execution: PARTIAL capability references an unknown Level")
		}
		if _, duplicate := seen[capability.LevelID]; duplicate {
			return errors.New("alarmd execution: duplicate Level PARTIAL capability")
		}
		seen[capability.LevelID] = struct{}{}
		switch capability.Policy {
		case PartialRequiresFull:
			if capability.Proof != nil {
				return errors.New("alarmd execution: REQUIRES_FULL must not carry a proof")
			}
		case PartialProvableAbnormalOnly:
			if capability.Proof == nil || capability.Proof.EvidenceKind != PartialEvidenceOmissionStable ||
				capability.Proof.EvidenceVersion == 0 || capability.Proof.RuleID == "" ||
				capability.Proof.RuleVersion == 0 || capability.Proof.ProofDigest == "" {
				return errors.New("alarmd execution: PROVABLE_ABNORMAL_ONLY requires a registered proof reference")
			}
		default:
			return errors.New("alarmd execution: invalid PARTIAL policy")
		}
	}
	return nil
}

func partialCapability(plan DuePlan, levelID uint32) (LevelPartialCapability, bool) {
	for _, capability := range plan.PartialCapabilities {
		if capability.LevelID == levelID {
			return capability, true
		}
	}
	return LevelPartialCapability{}, false
}

func partialEvidenceSupports(capability LevelPartialCapability, evidence *PartialEvidence) bool {
	return capability.Policy == PartialProvableAbnormalOnly && capability.Proof != nil && evidence != nil &&
		capability.Proof.EvidenceKind == evidence.Kind && capability.Proof.EvidenceVersion == evidence.Version &&
		evidence.EvidenceDigest != "" && evidence.OmissionOnly && evidence.ReturnedRecordsStable
}
