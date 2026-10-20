// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// hasAction 判断 Action 是否携带可上报的内容。
func hasAction(action actionInfo) bool {
	return action.ID != "" || action.ActionType != "" || action.ActionName != "" || action.ActionTargetName != "" || action.Timestamp != 0
}

func (c *spanConverter) appendAction(record d2Record) ptrace.Span {
	action := record.Fields.Action
	name := actionNameDefault
	if action.ActionType != "" {
		name = actionNamePrefix + action.ActionType
	}
	if action.ActionType == actionTypeCustom && action.ActionName != "" {
		name = actionNamePrefix + action.ActionName
	}
	span := c.newSpan(record, action.Timestamp, rum.Action, name)
	attrs := span.Attributes()
	attrs.PutString(attrActionType, action.ActionType)
	if action.ActionType != actionTypeCustom {
		attrs.PutString(attrActionTargetName, action.ActionTargetName)
	}
	return span
}
