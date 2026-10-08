// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"fmt"
	"slices"

	"linkd/internal/domain"
)

// MergeDecision 保存首次入窗或跳过证据，不把等待合并写成 Event 被抑制。
type MergeDecision struct {
	Severity     string      `json:"severity"`
	BypassReason string      `json:"bypass_reason,omitempty"`
	Steps        []MergeStep `json:"steps,omitempty"`
}

// MergeStep 指向真实等待窗口；FromWaiting 使用原窗口冻结版本，不强行换成本 Event 的版本。
type MergeStep struct {
	Policy      PolicyReleaseRef  `json:"policy"`
	Outcome     string            `json:"outcome"`
	ReasonCode  string            `json:"reason_code,omitempty"`
	Wait        *domain.MergeWait `json:"wait,omitempty"`
	FromWaiting bool              `json:"from_waiting,omitempty"`
}

// Clone 保持诊断与 Alert 等待摘要互不共享。
func (d *MergeDecision) Clone() *MergeDecision {
	if d == nil {
		return nil
	}
	v := *d
	v.Steps = slices.Clone(d.Steps)
	for i := range v.Steps {
		if v.Steps[i].Wait != nil {
			w := v.Steps[i].Wait.Clone()
			v.Steps[i].Wait = &w
		}
	}
	return &v
}

// Validate 对每条步骤的策略/窗口引用和容量做校验。
func (d *MergeDecision) Validate() error {
	if d == nil {
		return nil
	}
	if d.Severity == "" || len(d.Severity) > 32 || len(d.Steps) > 288 {
		return fmt.Errorf("invalid merge decision")
	}
	if d.BypassReason != "" {
		if (d.BypassReason != "aggregate_alert" && d.BypassReason != "merged_member") || len(d.Steps) > 0 {
			return fmt.Errorf("invalid merge bypass")
		}
		return nil
	}
	for _, step := range d.Steps {
		if step.Policy.Kind != "merge" || step.Policy.Validate() != nil || len(step.ReasonCode) > 80 {
			return fmt.Errorf("invalid merge policy reference")
		}
		switch step.Outcome {
		case "joined", "retained", "not_matched", "skipped":
		default:
			return fmt.Errorf("invalid merge outcome")
		}
		if (step.Outcome == "joined" || step.Outcome == "retained") != (step.Wait != nil) {
			return fmt.Errorf("merge result requires waiting reference")
		}
		if step.FromWaiting != (step.Outcome == "retained") {
			return fmt.Errorf("invalid existing merge reference")
		}
		if step.Wait != nil {
			if step.Wait.Validate() != nil || step.Wait.Policy.ID != step.Policy.ID || step.Wait.Policy.Version != step.Policy.Version || step.Wait.Policy.Digest != step.Policy.Digest {
				return fmt.Errorf("merge window policy mismatch")
			}
		}
	}
	return nil
}
