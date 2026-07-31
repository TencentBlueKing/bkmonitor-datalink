// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package validator

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"

func (v *Validator) ValidateView(event *model.ViewEvent) error {
	if event == nil {
		return missing("view")
	}
	return validateRequiredSection("view", event.View.Present, event.View.ID)
}

func (v *Validator) ValidateAction(event *model.ActionEvent) error {
	if event == nil {
		return missing("action")
	}
	return validateSection("action", event.Action.Present)
}

func (v *Validator) ValidateResource(event *model.ResourceEvent) error {
	if event == nil {
		return missing("resource")
	}
	return validateSection("resource", event.Resource.Present)
}

func (v *Validator) ValidateError(event *model.ErrorEvent) error {
	if event == nil {
		return missing("error")
	}
	return validateSection("error", event.Error.Present)
}

func (v *Validator) ValidateLongTask(event *model.LongTaskEvent) error {
	if event == nil {
		return missing("long_task")
	}
	return validateSection("long_task", event.LongTask.Present)
}

func (v *Validator) ValidateVital(event *model.VitalEvent) error {
	if event == nil {
		return missing("vital")
	}
	return validateRequiredSection("vital", event.Vital.Present, event.Vital.ID)
}

func validateSection(section string, present bool) error {
	if !present {
		return missing(section)
	}
	return nil
}

func validateRequiredSection(section string, present bool, id string) error {
	if err := validateSection(section, present); err != nil {
		return err
	}
	if id == "" {
		return missing(section + ".id")
	}
	return nil
}
