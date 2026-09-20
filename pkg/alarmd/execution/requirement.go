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
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type LogicalQueryRef string

type ResultWindowPolicy string

const ResultWindowExactHalfOpen ResultWindowPolicy = "EXACT_HALF_OPEN"

type ReadinessClass string

const (
	ReadinessEager             ReadinessClass = "EAGER"
	ReadinessFinalizedRequired ReadinessClass = "FINALIZED_REQUIRED"
)

type RelativeQueryWindow struct {
	StartOffsetSeconds int64 `json:"start_offset_seconds"`
	EndOffsetSeconds   int64 `json:"end_offset_seconds"`
	HalfOpen           bool  `json:"half_open"`
}

type DataRequirementConsumer struct {
	Consumer                           ConsumerRef
	ConsumerDeadlineUnixMilli          int64
	DownstreamExecutionReserveMilliSec int64
}

// InputProjection separates fields needed for evaluation from fields that
// define the stable series/state identity. Dimension fields are not promoted
// to identity merely because they are returned by the query.
type InputProjection struct {
	ValueFields     []string `json:"value_fields"`
	DimensionFields []string `json:"dimension_fields"`
	IdentityFields  []string `json:"identity_fields"`
}

type NamedInputPoint struct {
	Name          string `json:"name"`
	OffsetSeconds int64  `json:"offset_seconds"`
}

func (projection InputProjection) Validate() error {
	if len(projection.ValueFields) == 0 || !sortedUniqueFields(projection.ValueFields) ||
		!sortedUniqueFields(projection.DimensionFields) || !sortedUniqueFields(projection.IdentityFields) {
		return errors.New("alarmd execution: invalid input projection")
	}
	return nil
}

func (projection InputProjection) RequiredColumns() []string {
	columns := make(map[string]struct{}, len(projection.ValueFields)+len(projection.DimensionFields)+len(projection.IdentityFields))
	for _, group := range [][]string{projection.ValueFields, projection.DimensionFields, projection.IdentityFields} {
		for _, field := range group {
			if field != "" {
				columns[field] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(columns))
	for field := range columns {
		result = append(result, field)
	}
	sort.Strings(result)
	return result
}

func sortedUniqueFields(fields []string) bool {
	for index, field := range fields {
		if field == "" || (index > 0 && field <= fields[index-1]) {
			return false
		}
	}
	return true
}

// DataRequirementTemplate is the slot-independent portion of one logical
// input dependency. Binding a consumer deadline produces a DataRequirement.
type DataRequirementTemplate struct {
	RequirementID       RequirementID       `json:"requirement_id"`
	DatasetName         DatasetName         `json:"dataset_name"`
	Role                InputRole           `json:"role"`
	ConsumerLevelID     uint32              `json:"consumer_level_id"`
	LogicalQueryRef     LogicalQueryRef     `json:"logical_query_ref"`
	RelativeWindow      RelativeQueryWindow `json:"relative_window"`
	StepMillis          int64               `json:"step_millis"`
	AlignmentMillis     int64               `json:"alignment_millis"`
	ResultWindowPolicy  ResultWindowPolicy  `json:"result_window_policy"`
	ReadinessClass      ReadinessClass      `json:"readiness_class"`
	InputProjection     InputProjection     `json:"input_projection"`
	RequiredColumns     []string            `json:"required_columns"`
	PointOffsetsSeconds []int64             `json:"point_offsets_seconds,omitempty"`
	NamedPoints         []NamedInputPoint   `json:"named_points,omitempty"`
}

func BuildDataRequirementTemplate(template DataRequirementTemplate) (DataRequirementTemplate, error) {
	if template.RequirementID != "" {
		return DataRequirementTemplate{}, errors.New("alarmd execution: DataRequirement template builder owns requirement identity")
	}
	template.InputProjection = cloneInputProjection(template.InputProjection)
	template.PointOffsetsSeconds = append([]int64(nil), template.PointOffsetsSeconds...)
	template.NamedPoints = append([]NamedInputPoint(nil), template.NamedPoints...)
	if err := template.validateFacts(); err != nil {
		return DataRequirementTemplate{}, err
	}
	template.RequiredColumns = template.InputProjection.RequiredColumns()
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-data-requirement-template-v1", struct {
		DatasetName         DatasetName         `json:"dataset_name"`
		Role                InputRole           `json:"role"`
		ConsumerLevelID     uint32              `json:"consumer_level_id"`
		LogicalQueryRef     LogicalQueryRef     `json:"logical_query_ref"`
		RelativeWindow      RelativeQueryWindow `json:"relative_window"`
		StepMillis          int64               `json:"step_millis"`
		AlignmentMillis     int64               `json:"alignment_millis"`
		ResultWindowPolicy  ResultWindowPolicy  `json:"result_window_policy"`
		ReadinessClass      ReadinessClass      `json:"readiness_class"`
		InputProjection     InputProjection     `json:"input_projection"`
		PointOffsetsSeconds []int64             `json:"point_offsets_seconds"`
		NamedPoints         []NamedInputPoint   `json:"named_points"`
	}{template.DatasetName, template.Role, template.ConsumerLevelID, template.LogicalQueryRef, template.RelativeWindow, template.StepMillis,
		template.AlignmentMillis, template.ResultWindowPolicy, template.ReadinessClass, template.InputProjection,
		template.PointOffsetsSeconds, template.NamedPoints})
	if err != nil {
		return DataRequirementTemplate{}, fmt.Errorf("alarmd execution: derive DataRequirement identity: %w", err)
	}
	template.RequirementID = RequirementID(digest)
	return template, nil
}

func (template DataRequirementTemplate) validateFacts() error {
	if template.DatasetName == "" || template.ConsumerLevelID == 0 || template.LogicalQueryRef == "" {
		return errors.New("alarmd execution: complete DataRequirement template identity is required")
	}
	if template.Role != InputRolePrimary && template.Role != InputRoleAlgorithmDependency {
		return errors.New("alarmd execution: invalid DataRequirement template role")
	}
	if !template.RelativeWindow.HalfOpen || template.RelativeWindow.StartOffsetSeconds >= template.RelativeWindow.EndOffsetSeconds ||
		template.StepMillis <= 0 || template.AlignmentMillis <= 0 || template.ResultWindowPolicy != ResultWindowExactHalfOpen ||
		(template.ReadinessClass != ReadinessEager && template.ReadinessClass != ReadinessFinalizedRequired) {
		return errors.New("alarmd execution: invalid DataRequirement template window contract")
	}
	if err := validatePointOffsets(template.PointOffsetsSeconds); err != nil {
		return err
	}
	if err := validateNamedPoints(template.NamedPoints, template.PointOffsetsSeconds); err != nil {
		return err
	}
	return template.InputProjection.Validate()
}

func (template DataRequirementTemplate) Bind(consumer DataRequirementConsumer) DataRequirement {
	return DataRequirement{
		RequirementID: template.RequirementID, DatasetName: template.DatasetName, Role: template.Role,
		LogicalQueryRef: template.LogicalQueryRef, RelativeWindow: template.RelativeWindow,
		StepMillis: template.StepMillis, AlignmentMillis: template.AlignmentMillis,
		ResultWindowPolicy: template.ResultWindowPolicy, ReadinessClass: template.ReadinessClass,
		InputProjection:     cloneInputProjection(template.InputProjection),
		RequiredColumns:     append([]string(nil), template.RequiredColumns...),
		PointOffsetsSeconds: append([]int64(nil), template.PointOffsetsSeconds...),
		NamedPoints:         append([]NamedInputPoint(nil), template.NamedPoints...), Consumers: []DataRequirementConsumer{consumer},
	}
}

func cloneInputProjection(projection InputProjection) InputProjection {
	projection.ValueFields = append([]string(nil), projection.ValueFields...)
	projection.DimensionFields = append([]string(nil), projection.DimensionFields...)
	projection.IdentityFields = append([]string(nil), projection.IdentityFields...)
	return projection
}

// DataRequirement is the immutable logical dependency compiled by the
// algorithm capability layer and consumed by Access planning. It contains no
// UQ request DTO or execution-time endpoint.
type DataRequirement struct {
	RequirementID       RequirementID
	DatasetName         DatasetName
	Role                InputRole
	LogicalQueryRef     LogicalQueryRef
	RelativeWindow      RelativeQueryWindow
	StepMillis          int64
	AlignmentMillis     int64
	ResultWindowPolicy  ResultWindowPolicy
	ReadinessClass      ReadinessClass
	InputProjection     InputProjection
	RequiredColumns     []string
	PointOffsetsSeconds []int64
	NamedPoints         []NamedInputPoint
	Consumers           []DataRequirementConsumer
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
	if err := validatePointOffsets(requirement.PointOffsetsSeconds); err != nil {
		return err
	}
	if err := validateNamedPoints(requirement.NamedPoints, requirement.PointOffsetsSeconds); err != nil {
		return err
	}
	if len(requirement.RequiredColumns) == 0 || len(requirement.Consumers) == 0 {
		return errors.New("alarmd execution: DataRequirement requires columns and consumers")
	}
	if len(requirement.InputProjection.ValueFields) > 0 {
		if err := requirement.InputProjection.Validate(); err != nil {
			return err
		}
		expected := requirement.InputProjection.RequiredColumns()
		if len(expected) != len(requirement.RequiredColumns) {
			return errors.New("alarmd execution: DataRequirement columns differ from input projection")
		}
		for index := range expected {
			if expected[index] != requirement.RequiredColumns[index] {
				return errors.New("alarmd execution: DataRequirement columns differ from input projection")
			}
		}
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

func validatePointOffsets(offsets []int64) error {
	var previous int64
	for index, offset := range offsets {
		if offset <= 0 || (index > 0 && offset <= previous) {
			return errors.New("alarmd execution: invalid DataRequirement point offsets")
		}
		previous = offset
	}
	return nil
}

func validateNamedPoints(points []NamedInputPoint, offsets []int64) error {
	if len(points) != len(offsets) {
		return errors.New("alarmd execution: named input points differ from point offsets")
	}
	names := make(map[string]struct{}, len(points))
	for index, point := range points {
		if point.Name == "" || point.OffsetSeconds != offsets[index] {
			return errors.New("alarmd execution: invalid named input point")
		}
		if _, duplicate := names[point.Name]; duplicate {
			return errors.New("alarmd execution: duplicate named input point")
		}
		names[point.Name] = struct{}{}
	}
	return nil
}

func (requirement DataRequirement) AbsoluteWindow(evaluationTime EvaluationTime) QueryWindow {
	return QueryWindow{
		Start: int64(evaluationTime) + requirement.RelativeWindow.StartOffsetSeconds,
		End:   int64(evaluationTime) + requirement.RelativeWindow.EndOffsetSeconds,
	}
}
