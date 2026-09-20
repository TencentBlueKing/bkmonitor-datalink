// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"fmt"
	"time"
)

// AlertLog 是不可变的告警状态操作与最终输出流水。
type AlertLog struct {
	LogID         string        `json:"log_id"`
	BKTenantID    string        `json:"bk_tenant_id"`
	AlertID       string        `json:"alert_id"`
	OperatorKind  OperatorKind  `json:"operator_kind"`
	OperationKind OperationKind `json:"operation_kind"`
	Params        JSONObject    `json:"params"`
	CreatedTime   time.Time     `json:"created_time"`
}

// Normalize 深拷贝参数、统一时间并校验流水。
func (l AlertLog) Normalize() (AlertLog, error) {
	normalizedParams, err := l.Params.Normalize()
	if err != nil {
		return AlertLog{}, fmt.Errorf("alert log params: %w", err)
	}
	l.Params = normalizedParams
	l.CreatedTime = normalizeTime(l.CreatedTime)
	if err := l.Validate(); err != nil {
		return AlertLog{}, err
	}
	return l, nil
}

// Clone 返回不共享 Params 字节的流水副本。
func (l AlertLog) Clone() AlertLog {
	l.Params = l.Params.Clone()
	return l
}

// Validate 校验 AlertLog 的身份、类型和时间。
func (l AlertLog) Validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"log_id", l.LogID},
		{"bk_tenant_id", l.BKTenantID},
		{"alert_id", l.AlertID},
	}
	for _, field := range required {
		if field.value == "" {
			return fmt.Errorf("alert log %s must not be empty", field.name)
		}
	}
	if err := ValidateIdentityPart("bk_tenant_id", l.BKTenantID, 64); err != nil {
		return err
	}
	if !l.OperatorKind.Valid() {
		return fmt.Errorf("alert log operator_kind is invalid: %q", l.OperatorKind)
	}
	if !l.OperationKind.Valid() {
		return fmt.Errorf("alert log operation_kind is invalid: %q", l.OperationKind)
	}
	if l.CreatedTime.IsZero() {
		return fmt.Errorf("alert log created_time must not be zero")
	}
	if _, err := l.Params.Normalize(); err != nil {
		return fmt.Errorf("alert log params: %w", err)
	}
	return nil
}
