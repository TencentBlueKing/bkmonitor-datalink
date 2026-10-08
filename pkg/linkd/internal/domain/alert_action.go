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
	"maps"
	"reflect"
	"strings"
)

// AlertActionIntent 与获准业务变更同次保存，直到所有目标的原动作已可靠入队才能清除。
// 意图存在时仓储禁止新业务变更，因此当前 Alert 即原动作快照，无需再嵌套复制完整 Alert。
type AlertActionIntent struct {
	// Revision 固定原动作业务版本，不能在重试时更新。
	Revision int64 `json:"revision"`
	// Action 只允许 firing/resolved/close。
	Action string `json:"action"`
	// CauseType 和 CauseID 保存原 Event 或操作身份，不在补扫时重新生成。
	CauseType string `json:"cause_type"`
	CauseID   string `json:"cause_id"`
	// Targets 固定明确启用动作的目标及其不可变来源 Release，最多 16 项。
	Targets map[string]int64 `json:"targets"`
}

// Clone 返回不共享目标集合的意图。
func (i *AlertActionIntent) Clone() *AlertActionIntent {
	if i == nil {
		return nil
	}
	v := *i
	v.Targets = maps.Clone(i.Targets)
	return &v
}

// NewAlertActionIntent 为已获准的原业务快照冻结意图；没有动作目标时返回 nil。
// 调用方负责先完成本次 admission/终态裁决，不得仅凭 active 状态调用。
func NewAlertActionIntent(a Alert, causeType, causeID string) (*AlertActionIntent, error) {
	targets := actionTargets(a)
	if len(targets) == 0 {
		return nil, nil
	}
	action := "firing"
	switch a.Status {
	case AlertStatusRecovered:
		action = "resolved"
	case AlertStatusClosed:
		action = "close"
	}
	i := &AlertActionIntent{Revision: a.Revision, Action: action, CauseType: causeType, CauseID: causeID, Targets: targets}
	return i, i.Validate(a)
}

func actionTargets(a Alert) map[string]int64 {
	targets := map[string]int64{}
	for id, target := range a.Projection.Targets {
		if target.ActionEnabled {
			targets[id] = target.SourceVersion
		}
	}
	return targets
}

// Validate 确认意图仍对应当前冻结业务快照及完整动作目标，投影 ACK 可独立推进。
func (i *AlertActionIntent) Validate(a Alert) error {
	if i == nil {
		return nil
	}
	if i.Revision != a.Revision || len(i.Targets) == 0 || len(i.Targets) > 16 || !maps.Equal(i.Targets, actionTargets(a)) ||
		strings.TrimSpace(i.CauseID) == "" || len(i.CauseID) > EntityIDMaxBytes ||
		(i.CauseType != "source_event" && i.CauseType != "user_operation" && i.CauseType != "system_operation") {
		return fmt.Errorf("invalid action intent identity or targets")
	}
	if a.Admission.AdmittedAt == nil {
		return fmt.Errorf("action intent requires historical admission")
	}
	switch i.Action {
	case "firing":
		if !a.AdmittedActiveMain() || !a.Admission.AdmittedAt.Equal(a.UpdateAt) || a.Admission.CauseType != i.CauseType || a.Admission.CauseID != i.CauseID {
			return fmt.Errorf("firing intent requires this change's admission")
		}
	case "resolved":
		if a.Status != AlertStatusRecovered {
			return fmt.Errorf("resolved intent requires recovered alert")
		}
	case "close":
		if a.Status != AlertStatusClosed {
			return fmt.Errorf("close intent requires closed alert")
		}
	default:
		return fmt.Errorf("invalid action intent action")
	}
	if i.CauseType == "source_event" && i.CauseID != a.LatestEventID {
		return fmt.Errorf("action event differs from latest event")
	}
	for id, version := range i.Targets {
		if version < 1 || version >= 1<<53 || a.Projection.Targets[id].RequiredRevision != a.Revision {
			return fmt.Errorf("action intent target revision differs")
		}
	}
	return nil
}

// validateActionTransition 防止绕过 Lifecycle 的仓储写入丢掉已获准动作；清除意图走独立元数据白名单。
func validateActionTransition(current *Alert, next Alert) error {
	ready := next.Admission.AdmittedAt != nil
	if current != nil {
		ready = ready && (next.Status.Terminal() || !reflect.DeepEqual(current.Admission, next.Admission))
	}
	required := ready && len(actionTargets(next)) > 0
	if required != (next.ActionPending != nil) {
		return fmt.Errorf("business action and durable intent must be saved together")
	}
	return nil
}
