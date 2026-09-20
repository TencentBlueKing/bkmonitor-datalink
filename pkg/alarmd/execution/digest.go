// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type duePlanDigestLevel struct {
	LevelID                        uint32                 `json:"level_id"`
	Priority                       uint32                 `json:"priority"`
	DetectFingerprint              string                 `json:"detect_fingerprint"`
	TriggerFingerprint             string                 `json:"trigger_fingerprint"`
	EffectiveTimeRequirementDigest string                 `json:"effective_time_requirement_digest"`
	PartialCapability              LevelPartialCapability `json:"partial_capability"`
}

type duePlanDigestEntry struct {
	Identity                    PlanIdentity              `json:"identity"`
	PlanRef                     contract.RuntimePlanRefV1 `json:"plan_ref"`
	DetectFingerprint           string                    `json:"detect_fingerprint"`
	TriggerFingerprint          string                    `json:"trigger_fingerprint"`
	StateGeneration             StateGeneration           `json:"state_generation"`
	StateApplyEpoch             StateApplyEpoch           `json:"state_apply_epoch"`
	ScheduleRevision            PlanScheduleRevision      `json:"schedule_revision"`
	CompletionDeadlineUnixMilli int64                     `json:"completion_deadline_unix_milli"`
	Levels                      []duePlanDigestLevel      `json:"levels"`
}

// DeriveSnapshotUnavailableDuePlanSetDigest identifies the due Plan set of a
// Slot that is finalized query-free because the Snapshot publication of its
// Schedule Segment can no longer be read. No compiled Plan exists to bind, so
// the digest binds the persisted Segment facts, the evaluation time and the
// due Plan schedule references. It is deterministic across attempts and lives
// in its own domain, so it never collides with a compiled due Plan set digest.
func DeriveSnapshotUnavailableDuePlanSetDigest(
	segment ScheduleSegmentFact,
	evaluationTime EvaluationTime,
	duePlans []FrozenPlanScheduleRef,
) (DuePlanSetDigest, error) {
	if err := segment.Validate(); err != nil {
		return "", err
	}
	if evaluationTime < segment.Start || len(duePlans) == 0 {
		return "", fmt.Errorf("alarmd execution: snapshot-unavailable due Plan set requires an in-segment Slot with due Plans")
	}
	sorted := append([]FrozenPlanScheduleRef(nil), duePlans...)
	sort.Slice(sorted, func(left, right int) bool { return lessPlanIdentity(sorted[left].Identity, sorted[right].Identity) })
	for index := range sorted {
		if err := sorted[index].Identity.Validate(); err != nil || sorted[index].ScheduleRevision == "" {
			return "", fmt.Errorf("alarmd execution: snapshot-unavailable due Plan %d is incomplete", index)
		}
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-snapshot-unavailable-due-plan-set-v1", struct {
		QueryGroup           QueryGroupIdentity      `json:"query_group"`
		SnapshotRevision     SnapshotRevision        `json:"snapshot_revision"`
		PublicationEpoch     PublicationEpoch        `json:"publication_epoch"`
		QueryRevision        QueryRevision           `json:"query_revision"`
		ScheduleRevision     ScheduleRevision        `json:"schedule_revision"`
		ScheduleSegmentStart EvaluationTime          `json:"schedule_segment_start"`
		EvaluationTime       EvaluationTime          `json:"evaluation_time"`
		DuePlans             []FrozenPlanScheduleRef `json:"due_plans"`
	}{
		QueryGroup: segment.QueryGroup, SnapshotRevision: segment.Publication.SnapshotRevision,
		PublicationEpoch: segment.Publication.PublicationEpoch, QueryRevision: segment.QueryRevision,
		ScheduleRevision: segment.ScheduleRevision, ScheduleSegmentStart: segment.Start,
		EvaluationTime: evaluationTime, DuePlans: sorted,
	})
	if err != nil {
		return "", err
	}
	return DuePlanSetDigest(digest), nil
}

// DeriveDuePlanSetDigest binds the frozen due Plan set, capability closure and
// logical data dependencies. It excludes ownership and attempt facts.
func DeriveDuePlanSetDigest(plans []DuePlan, requirements []DataRequirement) (DuePlanSetDigest, error) {
	entries := make([]duePlanDigestEntry, len(plans))
	for index, plan := range plans {
		if plan.CompiledPlan == nil {
			return "", fmt.Errorf("alarmd execution: due Plan %d is not compiled", index)
		}
		fingerprints := plan.CompiledPlan.Fingerprints()
		capabilities := make(map[uint32]LevelPartialCapability, len(plan.PartialCapabilities))
		for _, capability := range plan.PartialCapabilities {
			capabilities[capability.LevelID] = capability
		}
		levels := plan.CompiledPlan.Levels()
		levelEntries := make([]duePlanDigestLevel, len(levels))
		for levelIndex, level := range levels {
			definition := level.Definition()
			levelFingerprints := level.Fingerprints()
			levelEntries[levelIndex] = duePlanDigestLevel{
				LevelID: definition.LevelID, Priority: definition.Priority,
				DetectFingerprint: levelFingerprints.Detect, TriggerFingerprint: levelFingerprints.Trigger,
				EffectiveTimeRequirementDigest: level.EffectiveTimeRequirementDigest(),
				PartialCapability:              capabilities[definition.LevelID],
			}
		}
		sort.Slice(levelEntries, func(left, right int) bool { return levelEntries[left].LevelID < levelEntries[right].LevelID })
		entries[index] = duePlanDigestEntry{
			Identity: plan.Identity, PlanRef: plan.CompiledPlan.PlanRef(), DetectFingerprint: fingerprints.Detect,
			TriggerFingerprint: fingerprints.Trigger, StateGeneration: plan.StateGeneration,
			StateApplyEpoch: plan.StateApplyEpoch, ScheduleRevision: plan.ScheduleRevision,
			CompletionDeadlineUnixMilli: plan.CompletionDeadlineUnixMilli, Levels: levelEntries,
		}
	}
	sort.Slice(entries, func(left, right int) bool {
		return lessPlanIdentity(entries[left].Identity, entries[right].Identity)
	})
	normalizedRequirements := append([]DataRequirement(nil), requirements...)
	for index := range normalizedRequirements {
		normalizedRequirements[index].RequiredColumns = append([]string(nil), normalizedRequirements[index].RequiredColumns...)
		sort.Strings(normalizedRequirements[index].RequiredColumns)
		normalizedRequirements[index].Consumers = append([]DataRequirementConsumer(nil), normalizedRequirements[index].Consumers...)
		sort.Slice(normalizedRequirements[index].Consumers, func(left, right int) bool {
			leftConsumer := normalizedRequirements[index].Consumers[left]
			rightConsumer := normalizedRequirements[index].Consumers[right]
			if leftConsumer.Consumer.Plan != rightConsumer.Consumer.Plan {
				return lessPlanIdentity(leftConsumer.Consumer.Plan, rightConsumer.Consumer.Plan)
			}
			return leftConsumer.Consumer.LevelID < rightConsumer.Consumer.LevelID
		})
	}
	sort.Slice(normalizedRequirements, func(left, right int) bool {
		return normalizedRequirements[left].RequirementID < normalizedRequirements[right].RequirementID
	})
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-due-plan-set-v1", struct {
		Plans        []duePlanDigestEntry `json:"plans"`
		Requirements []DataRequirement    `json:"requirements"`
	}{Plans: entries, Requirements: normalizedRequirements})
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive due Plan set digest: %w", err)
	}
	return DuePlanSetDigest(digest), nil
}

func lessPlanIdentity(left, right PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}
