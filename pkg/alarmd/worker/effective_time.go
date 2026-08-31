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
	"context"
	"errors"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type alwaysEffectiveTimeTarget struct {
	consumer execution.ConsumerRef
	series   execution.SeriesIdentityDigest
}

func prepareAlwaysEffectiveTimeFacts(
	ctx context.Context,
	header execution.InternalExecutionHeader,
) (map[execution.ConsumerRef]strategy.EffectiveTimeFact, error) {
	requestsByDigest := make(map[string]strategy.EffectiveTimeRequest)
	requirementByConsumer := make(map[execution.ConsumerRef]string)
	for _, due := range header.DuePlans {
		if due.CompiledPlan == nil {
			return nil, errors.New("alarmd worker: EffectiveTime target references an unknown Plan")
		}
		for _, level := range due.CompiledPlan.Levels() {
			requirement := level.EffectiveTimeRequirement()
			if requirement.Kind() != strategy.EffectiveTimeAlways {
				return nil, errors.New("alarmd worker: non-ALWAYS EffectiveTime requires a resolved series fact")
			}
			consumer := execution.ConsumerRef{Plan: due.Identity, LevelID: level.Definition().LevelID, HasLevel: true}
			requirementByConsumer[consumer] = requirement.Digest()
			requestsByDigest[requirement.Digest()] = strategy.EffectiveTimeRequest{
				TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID,
				EvaluationTime: int64(header.Contract.Slot.EvaluationTime), Requirement: requirement,
			}
		}
	}
	digests := make([]string, 0, len(requestsByDigest))
	for digest := range requestsByDigest {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	requests := make([]strategy.EffectiveTimeRequest, len(digests))
	for index, digest := range digests {
		requests[index] = requestsByDigest[digest]
	}
	resolved, err := strategy.NewStaticScheduleProvider(nil).Resolve(ctx, requests)
	if err != nil {
		return nil, err
	}
	factsByDigest := make(map[string]strategy.EffectiveTimeFact, len(resolved))
	for index, fact := range resolved {
		factsByDigest[digests[index]] = fact
	}
	facts := make(map[execution.ConsumerRef]strategy.EffectiveTimeFact, len(requirementByConsumer))
	for consumer, digest := range requirementByConsumer {
		facts[consumer] = factsByDigest[digest]
	}
	return facts, nil
}

// bindAlwaysEffectiveTimeFacts binds the facts prepared once per Slot only
// after Access has supplied real series identities. The returned header is
// scoped to one EvaluationRequest; the static header remains series-agnostic.
func bindAlwaysEffectiveTimeFacts(
	header execution.InternalExecutionHeader,
	stateItems []execution.StatePreflightItem,
	prepared map[execution.ConsumerRef]strategy.EffectiveTimeFact,
) (execution.InternalExecutionHeader, error) {
	plans := make(map[execution.PlanIdentity]execution.DuePlan, len(header.DuePlans))
	for _, due := range header.DuePlans {
		plans[due.Identity] = due
	}
	targets := make([]alwaysEffectiveTimeTarget, 0)
	seen := make(map[alwaysEffectiveTimeTarget]struct{})
	for _, item := range stateItems {
		due, ok := plans[item.Identity.Plan]
		if !ok || due.CompiledPlan == nil {
			return execution.InternalExecutionHeader{}, errors.New("alarmd worker: EffectiveTime target references an unknown Plan")
		}
		for _, level := range due.CompiledPlan.Levels() {
			target := alwaysEffectiveTimeTarget{
				consumer: execution.ConsumerRef{Plan: due.Identity, LevelID: level.Definition().LevelID, HasLevel: true},
				series:   item.Identity.SeriesIdentityDigest,
			}
			if _, duplicate := seen[target]; duplicate {
				continue
			}
			seen[target] = struct{}{}
			targets = append(targets, target)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		left, right := targets[i], targets[j]
		if left.consumer.Plan.TenantID != right.consumer.Plan.TenantID {
			return left.consumer.Plan.TenantID < right.consumer.Plan.TenantID
		}
		if left.consumer.Plan.BusinessID != right.consumer.Plan.BusinessID {
			return left.consumer.Plan.BusinessID < right.consumer.Plan.BusinessID
		}
		if left.consumer.Plan.StrategyID != right.consumer.Plan.StrategyID {
			return left.consumer.Plan.StrategyID < right.consumer.Plan.StrategyID
		}
		if left.consumer.LevelID != right.consumer.LevelID {
			return left.consumer.LevelID < right.consumer.LevelID
		}
		return left.series < right.series
	})

	bound := header
	bound.EffectiveTimeFacts = make([]execution.BoundEffectiveTimeFact, 0, len(targets))
	for _, target := range targets {
		fact, ok := prepared[target.consumer]
		if !ok {
			return execution.InternalExecutionHeader{}, errors.New("alarmd worker: prepared ALWAYS EffectiveTime fact is missing")
		}
		bound.EffectiveTimeFacts = append(bound.EffectiveTimeFacts, execution.BoundEffectiveTimeFact{
			Consumer: target.consumer, SeriesIdentity: target.series, Fact: fact,
		})
	}
	return bound, nil
}
