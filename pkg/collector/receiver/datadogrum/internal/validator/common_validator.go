// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package validator

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

func (v *Validator) ValidateCommon(common *model.CommonFields) error {
	if common == nil {
		return missing("common")
	}
	if !common.Type.IsValid() {
		return model.ErrUnsupportedEventType
	}
	if common.Date <= 0 && common.Timestamp <= 0 {
		return missing("date")
	}
	if common.Application.ID == "" {
		return missing("application.id")
	}
	if common.Session != nil && common.Session.ID == "" {
		return missing("session.id")
	}
	return nil
}

func missing(field string) error {
	return fmt.Errorf("%w: %s", model.ErrMissingRequiredField, field)
}
