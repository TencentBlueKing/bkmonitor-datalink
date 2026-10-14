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
	"encoding/json"

	"github.com/google/uuid"
)

// AlertChange 是业务保存后的轻量通知，消费者按身份读取当前告警。
type AlertChange struct {
	BKTenantID string       `json:"bk_tenant_id"`
	AlertID    string       `json:"alert_id"`
	Action     *AlertAction `json:"action,omitempty"`
}

// AlertAction 仅携带处置实际需要的原因和终态信息，不复制告警正文。
type AlertAction struct {
	ID           string             `json:"action_id"`
	Type         string             `json:"action"`
	Cause        AlertActionCause   `json:"cause"`
	EndOperation *AlertEndOperation `json:"end_operation,omitempty"`
	Severity     string             `json:"severity,omitempty"`
	EndType      AlertEndType       `json:"end_type,omitempty"`
}

// AlertActionCause 标识原事件或控制操作。
type AlertActionCause struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// NewAlertChange 复用业务身份生成动作 ID，重投不依赖发送时间或进程计数。
// actionReady 来自调用方已完成的准入裁决；普通状态变更不自行放行动作。
func NewAlertChange(a Alert, cause AlertActionCause, actionReady bool) AlertChange {
	out := AlertChange{BKTenantID: a.BKTenantID, AlertID: a.AlertID}
	if !actionReady {
		return out
	}
	action := "firing"
	if a.Status == AlertStatusRecovered {
		action = "resolved"
	}
	if a.Status == AlertStatusClosed {
		action = "close"
	}
	identity, _ := json.Marshal([]any{"linkd:action:v3", a.BKTenantID, a.AlertID, a.Revision, action, cause.Type, cause.ID})
	out.Action = &AlertAction{ID: uuid.NewSHA1(uuid.NameSpaceURL, identity).String(), Type: action, Cause: cause, Severity: a.Severity, EndType: a.EndType}
	if a.EndOperation != nil {
		operation := *a.EndOperation
		out.Action.EndOperation = &operation
	}
	return out
}
