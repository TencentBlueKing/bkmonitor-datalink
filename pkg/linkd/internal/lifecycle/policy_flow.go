// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"encoding/json"

	"linkd/internal/domain"
)

// freezeMergeFlow 在业务 CAS 前固定动作、角色和关系事实；输出重试不再读取关系或策略。
func freezeMergeFlow(change *domain.AlertMergeChange, relation *domain.MergeRelation) error {
	var facts *domain.MergeMemberContext
	if relation != nil {
		value := relation.MemberContext()
		facts = &value
	}
	return freezeMergeMemberFlow(change, facts)
}

func freezeMergeMemberFlow(change *domain.AlertMergeChange, relation *domain.MergeMemberContext) error {
	values := map[string]any{"display_version": 2, "change_kind": change.Kind, "merge_role": "member"}
	if relation != nil {
		values["policy"] = relation.Policy
		values["parent_alert_id"] = relation.ParentAlertID
		values["parent_alert_title"] = relation.ParentTitle
		values["member_count"] = relation.MemberCount
		values["reason_code"] = relation.EndReason
	}
	if change.Kind == "parent_ready" || change.Kind == "parent_recover" {
		values["merge_role"] = "parent"
	}
	if change.Kind == "parent_recover" {
		values["reason_code"] = "merge_members_ended"
	}
	if change.Kind == "release" {
		values["reason_code"] = "window_released"
		for _, wait := range change.Before.Pending {
			if wait.WindowID == change.WindowID {
				values["policy"] = wait.Policy
				values["duration_seconds"] = int64(wait.Deadline.Sub(wait.StartedAt).Seconds())
			}
		}
	}
	params := domain.JSONObject{}
	for key, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		params[key] = raw
	}
	var err error
	change.LogParams, err = params.Normalize()
	return err
}

// eventShieldFlowLogs 只记录实际增加/解除的绑定，多策略部分解除不能被合并成重新屏蔽。
func eventShieldFlowLogs(event domain.Event, alert domain.Alert, before, after []domain.ShieldBinding) ([]domain.AlertLog, error) {
	logs := []domain.AlertLog{}
	for _, item := range []struct {
		kind     domain.OperationKind
		reason   string
		bindings []domain.ShieldBinding
	}{
		{domain.OperationKindUnshield, "shield_released", shieldBindingDifference(before, after)},
		{domain.OperationKindShield, "shield_matched", shieldBindingDifference(after, before)},
	} {
		if len(item.bindings) == 0 {
			continue
		}
		log, err := eventAlertLog(event, alert, item.kind, item.reason, alert.UpdateAt, nil)
		if err != nil {
			return nil, err
		}
		log.Params["display_version"] = json.RawMessage("2")
		for key, value := range map[string][]domain.ShieldBinding{"bindings": item.bindings, "before_bindings": before, "after_bindings": after} {
			raw, err := marshalShieldFlowBindings(value, 2)
			if err != nil {
				return nil, err
			}
			log.Params[key] = raw
		}
		log.LogID = digestStrings("policy-flow-log-v2", log.LogID)
		logs = append(logs, log)
	}
	return logs, nil
}

// marshalShieldFlowBindings 保留完整绑定集合和当次命中的时间段，静态配置按发布版本读取。
// 只修改日志副本；已有意图保持原格式，避免重试覆盖相同身份下的不可变日志。
func marshalShieldFlowBindings(bindings []domain.ShieldBinding, version int) (json.RawMessage, error) {
	if version < 2 {
		return json.Marshal(bindings)
	}
	var facts []domain.JSONObject
	if bindings != nil {
		facts = make([]domain.JSONObject, 0, len(bindings))
	}
	for _, binding := range bindings {
		copy := binding
		copy.Reason = ""
		raw, err := json.Marshal(copy)
		if err != nil {
			return nil, err
		}
		var item domain.JSONObject
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		facts = append(facts, item)
	}
	return json.Marshal(facts)
}
