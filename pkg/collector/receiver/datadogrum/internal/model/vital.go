// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

type VitalEvent struct {
	CommonFields
	Vital Vital `json:"vital"`
}

func (e *VitalEvent) GetType() EventType { return EventTypeVital }
func (e *VitalEvent) GetCommon() *CommonFields {
	return &e.CommonFields
}

type Vital struct {
	Present       bool               `json:"-"`
	ID            string             `json:"id,omitempty"`
	Type          VitalType          `json:"type,omitempty"`
	Name          string             `json:"name,omitempty"`
	Description   string             `json:"description,omitempty"`
	StepType      VitalStepType      `json:"step_type,omitempty"`
	OperationKey  string             `json:"operation_key,omitempty"`
	Duration      *int64             `json:"duration,omitempty"`
	FailureReason VitalFailureReason `json:"failure_reason,omitempty"`
	Internal      *VitalInternal     `json:"_dd,omitempty"`
}

type VitalType string
type VitalStepType string
type VitalFailureReason string

type VitalInternal struct {
	ComputedValue *float64 `json:"computed_value,omitempty"`
}
