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

type LogicalQueryRef string

type ResultWindowPolicy string

const ResultWindowExactHalfOpen ResultWindowPolicy = "EXACT_HALF_OPEN"

type ReadinessClass string

const (
	ReadinessEager             ReadinessClass = "EAGER"
	ReadinessFinalizedRequired ReadinessClass = "FINALIZED_REQUIRED"
)

type RelativeQueryWindow struct {
	StartOffsetSeconds int64
	EndOffsetSeconds   int64
	HalfOpen           bool
}

type DataRequirementConsumer struct {
	Consumer                           ConsumerRef
	ConsumerDeadlineUnixMilli          int64
	DownstreamExecutionReserveMilliSec int64
}

// DataRequirement is the immutable logical dependency compiled by the
// algorithm capability layer and consumed by Access planning. It contains no
// UQ request DTO or execution-time endpoint.
type DataRequirement struct {
	RequirementID      RequirementID
	DatasetName        DatasetName
	Role               InputRole
	LogicalQueryRef    LogicalQueryRef
	RelativeWindow     RelativeQueryWindow
	StepMillis         int64
	AlignmentMillis    int64
	ResultWindowPolicy ResultWindowPolicy
	ReadinessClass     ReadinessClass
	RequiredColumns    []string
	Consumers          []DataRequirementConsumer
}

func (requirement DataRequirement) Validate(plans map[PlanIdentity]DuePlan) error {
	if requirement.RequirementID == "" || requirement.DatasetName == "" || requirement.LogicalQueryRef == "" {
		return errors.New("alarmd execution: complete DataRequirement identity is required")
	}
	if requirement.Role != InputRolePrimary && requirement.Role != InputRoleAlgorithmDependency {
		return errors.New("alarmd execution: invalid DataRequirement role")
	}
	if !requirement.RelativeWindow.HalfOpen || requirement.RelativeWindow.StartOffsetSeconds >= requirement.RelativeWindow.EndOffsetSeconds {
		return errors.New("alarmd execution: DataRequirement requires a non-empty half-open relative window")
	}
	if requirement.StepMillis <= 0 || requirement.AlignmentMillis <= 0 || requirement.ResultWindowPolicy != ResultWindowExactHalfOpen {
		return errors.New("alarmd execution: invalid DataRequirement result window contract")
	}
	if requirement.ReadinessClass != ReadinessEager && requirement.ReadinessClass != ReadinessFinalizedRequired {
		return errors.New("alarmd execution: invalid DataRequirement readiness class")
	}
	if len(requirement.RequiredColumns) == 0 || len(requirement.Consumers) == 0 {
		return errors.New("alarmd execution: DataRequirement requires columns and consumers")
	}
	columns := make(map[string]struct{}, len(requirement.RequiredColumns))
	for _, column := range requirement.RequiredColumns {
		if column == "" {
			return errors.New("alarmd execution: empty DataRequirement required column")
		}
		if _, duplicate := columns[column]; duplicate {
			return errors.New("alarmd execution: duplicate DataRequirement required column")
		}
		columns[column] = struct{}{}
	}
	consumers := make(map[ConsumerRef]struct{}, len(requirement.Consumers))
	for _, consumer := range requirement.Consumers {
		due, ok := plans[consumer.Consumer.Plan]
		if !ok {
			return errors.New("alarmd execution: DataRequirement references an unknown Plan")
		}
		if err := validateOptionalLevel(consumer.Consumer.LevelID, consumer.Consumer.HasLevel); err != nil {
			return err
		}
		if consumer.Consumer.HasLevel && !compiledPlanHasLevel(due.CompiledPlan, consumer.Consumer.LevelID) {
			return errors.New("alarmd execution: DataRequirement references an unknown compiled Level")
		}
		if consumer.ConsumerDeadlineUnixMilli <= 0 || consumer.DownstreamExecutionReserveMilliSec <= 0 {
			return errors.New("alarmd execution: invalid DataRequirement consumer deadline or reserve")
		}
		if consumer.ConsumerDeadlineUnixMilli != due.CompletionDeadlineUnixMilli {
			return errors.New("alarmd execution: DataRequirement consumer deadline differs from frozen Plan deadline")
		}
		if _, duplicate := consumers[consumer.Consumer]; duplicate {
			return errors.New("alarmd execution: duplicate DataRequirement consumer")
		}
		consumers[consumer.Consumer] = struct{}{}
	}
	return nil
}

func (requirement DataRequirement) AbsoluteWindow(evaluationTime EvaluationTime) QueryWindow {
	return QueryWindow{
		Start: int64(evaluationTime) + requirement.RelativeWindow.StartOffsetSeconds,
		End:   int64(evaluationTime) + requirement.RelativeWindow.EndOffsetSeconds,
	}
}
