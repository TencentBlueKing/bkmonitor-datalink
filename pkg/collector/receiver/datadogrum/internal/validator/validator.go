// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package validator

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

// Validator checks common and event-specific RUM requirements.
type Validator struct{}

// New creates a RUM event validator.
func New() *Validator {
	return &Validator{}
}

// Validate checks an event's common fields and concrete event section.
func (v *Validator) Validate(event model.Event) error {
	if event == nil || isNilEvent(event) {
		return model.ErrInvalidPayload
	}
	if err := v.ValidateCommon(event.GetCommon()); err != nil {
		return err
	}

	switch ev := event.(type) {
	case *model.ViewEvent:
		// EventTypeViewUpdate shares the ViewEvent concrete type.
		return v.ValidateView(ev)
	case *model.ActionEvent:
		return v.ValidateAction(ev)
	case *model.ResourceEvent:
		return v.ValidateResource(ev)
	case *model.ErrorEvent:
		return v.ValidateError(ev)
	case *model.LongTaskEvent:
		return v.ValidateLongTask(ev)
	case *model.VitalEvent:
		return v.ValidateVital(ev)
	default:
		return model.ErrUnsupportedEventType
	}
}

func isNilEvent(event model.Event) bool {
	switch v := event.(type) {
	case *model.ViewEvent:
		return v == nil
	case *model.ActionEvent:
		return v == nil
	case *model.ResourceEvent:
		return v == nil
	case *model.ErrorEvent:
		return v == nil
	case *model.LongTaskEvent:
		return v == nil
	case *model.VitalEvent:
		return v == nil
	default:
		return false
	}
}
