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
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"linkd/internal/domain"
)

// PolicyReleaseRef 是 Event 首次策略裁决冻结的版本，租户作用域继承所属 Event。
type PolicyReleaseRef struct {
	Kind    string `json:"type"`
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

// Validate 校验不含租户的版本引用；租户必须由所属 Event 校验。
func (ref PolicyReleaseRef) Validate() error {
	if ref.Kind != "suppression" && ref.Kind != "shield" && ref.Kind != "merge" {
		return fmt.Errorf("invalid policy reference type")
	}
	if err := domain.ValidateIdentityPart("policy id", ref.ID, 80); err != nil {
		return err
	}
	raw, err := hex.DecodeString(ref.Digest)
	if err != nil || len(raw) != 32 || ref.Version < 1 || ref.Version >= 1<<53 {
		return fmt.Errorf("invalid policy reference")
	}
	return nil
}

// PolicyContext 在任何策略副作用前 CAS 保存；计划撤销、终态提交和重试均不能清除或替换。
// 配置依赖失败时保存明确跳过原因，不能伪装成确实没有匹配策略。
type PolicyContext struct {
	EvaluatedAt time.Time          `json:"evaluated_at"`
	Releases    []PolicyReleaseRef `json:"releases"`
	ReasonCode  string             `json:"reason_code,omitempty"`
}

// Clone 深拷贝版本引用，保持首次裁决时间。
func (p *PolicyContext) Clone() *PolicyContext {
	if p == nil {
		return nil
	}
	result := *p
	result.Releases = slices.Clone(p.Releases)
	return &result
}

// Validate 限制策略数量和身份，禁止重复引用、非法摘要或跳过状态携带不完整配置集。
func (p *PolicyContext) Validate() error {
	if p == nil {
		return nil
	}
	if p.EvaluatedAt.IsZero() || len(p.Releases) > 256 {
		return fmt.Errorf("invalid policy context time/count")
	}
	if p.ReasonCode != "" && (p.ReasonCode != "policy_load_failed" || len(p.Releases) > 0) {
		return fmt.Errorf("invalid policy context skip reason")
	}
	seen := map[string]bool{}
	for _, ref := range p.Releases {
		if err := ref.Validate(); err != nil {
			return err
		}
		key := ref.Kind + ":" + ref.ID
		if seen[key] {
			return fmt.Errorf("invalid or duplicate policy reference")
		}
		seen[key] = true
	}
	return nil
}
