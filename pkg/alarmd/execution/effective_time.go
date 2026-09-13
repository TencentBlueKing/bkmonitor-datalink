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
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// BoundEffectiveTimeFact binds a resolved fact to one immutable Plan Level and
// series. Evaluation consumes facts only; it never resolves calendars itself.
type BoundEffectiveTimeFact struct {
	Consumer       ConsumerRef
	SeriesIdentity SeriesIdentityDigest
	Fact           strategy.EffectiveTimeFact
}

func validateEffectiveTimeFacts(input InternalExecution, plans map[PlanIdentity]DuePlan) error {
	type factIdentity struct {
		Consumer ConsumerRef
		Series   SeriesIdentityDigest
	}
	wanted := make(map[factIdentity]string)
	for _, item := range input.StatePreflight {
		due, ok := plans[item.Identity.Plan]
		if !ok {
			continue
		}
		for _, level := range due.CompiledPlan.Levels() {
			identity := factIdentity{
				Consumer: ConsumerRef{Plan: due.Identity, LevelID: level.Definition().LevelID, HasLevel: true},
				Series:   item.Identity.SeriesIdentityDigest,
			}
			wanted[identity] = level.EffectiveTimeRequirementDigest()
		}
	}
	seen := make(map[factIdentity]struct{}, len(input.EffectiveTimeFacts))
	for _, binding := range input.EffectiveTimeFacts {
		if err := validateOptionalLevel(binding.Consumer.LevelID, binding.Consumer.HasLevel); err != nil {
			return err
		}
		if !binding.Consumer.HasLevel || binding.SeriesIdentity == "" {
			return errors.New("alarmd execution: EffectiveTime fact requires Level and series identity")
		}
		due, ok := plans[binding.Consumer.Plan]
		if !ok || !compiledPlanHasLevel(due.CompiledPlan, binding.Consumer.LevelID) {
			return errors.New("alarmd execution: EffectiveTime fact references an unknown Plan Level")
		}
		identity := factIdentity{Consumer: binding.Consumer, Series: binding.SeriesIdentity}
		requirementDigest, required := wanted[identity]
		if !required || binding.Fact.RequirementDigest() != requirementDigest {
			return errors.New("alarmd execution: EffectiveTime fact does not match its compiled requirement")
		}
		if binding.Fact.FactRevision() == "" || binding.Fact.FactDigest() == "" ||
			binding.Fact.ValidFrom() > int64(input.Contract.Slot.EvaluationTime) ||
			binding.Fact.ValidUntil() <= int64(input.Contract.Slot.EvaluationTime) {
			return errors.New("alarmd execution: EffectiveTime fact is incomplete or stale for the Slot")
		}
		switch binding.Fact.Status() {
		case strategy.EffectiveTimeActive, strategy.EffectiveTimeInactive, strategy.EffectiveTimeUnknown:
		default:
			return errors.New("alarmd execution: invalid EffectiveTime status")
		}
		if _, duplicate := seen[identity]; duplicate {
			return errors.New("alarmd execution: duplicate EffectiveTime fact binding")
		}
		seen[identity] = struct{}{}
	}
	if len(seen) != len(wanted) {
		return errors.New("alarmd execution: every prepared Plan Level and series requires an EffectiveTime fact")
	}
	return nil
}

func findEffectiveTimeFact(
	input InternalExecution,
	plan PlanIdentity,
	levelID uint32,
	series SeriesIdentityDigest,
) (BoundEffectiveTimeFact, bool) {
	for _, binding := range input.EffectiveTimeFacts {
		if binding.Consumer.Plan == plan && binding.Consumer.HasLevel && binding.Consumer.LevelID == levelID &&
			binding.SeriesIdentity == series {
			return binding, true
		}
	}
	return BoundEffectiveTimeFact{}, false
}
