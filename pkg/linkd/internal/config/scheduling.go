// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ReplicaCount 的零值表示 all；显式数字 0 表示停止该角色。
type ReplicaCount struct{ Number *int }

// Limit 返回候选 worker 数量约束后的目标值。
func (r ReplicaCount) Limit(workers int) int {
	if r.Number == nil {
		return workers
	}
	return min(workers, *r.Number)
}

// Validate 限制单来源角色的副本配置。
func (r ReplicaCount) Validate() error {
	if r.Number != nil && (*r.Number < 0 || *r.Number > 10000) {
		return fmt.Errorf("replicas must be all or an integer between 0 and 10000")
	}
	return nil
}

// MarshalJSON 保留 all 与数字 0 的区别。
func (r ReplicaCount) MarshalJSON() ([]byte, error) {
	if r.Number == nil {
		return []byte(`"all"`), nil
	}
	return json.Marshal(*r.Number)
}

// UnmarshalJSON 严格接受 all 或非负整数。
func (r *ReplicaCount) UnmarshalJSON(data []byte) error {
	if string(data) == `"all"` {
		r.Number = nil
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil || string(data) == "null" {
		return fmt.Errorf("replicas must be all or an integer")
	}
	r.Number = &n
	return r.Validate()
}

// MarshalYAML 编码调度数量。
func (r ReplicaCount) MarshalYAML() (any, error) {
	if r.Number == nil {
		return "all", nil
	}
	return *r.Number, nil
}

// UnmarshalYAML 解码调度数量，不把字符串数字当整数。
func (r *ReplicaCount) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Value == "all" {
		r.Number = nil
		return nil
	}
	if n.Tag != "!!int" {
		return fmt.Errorf("replicas must be all or an integer")
	}
	v, err := strconv.Atoi(n.Value)
	if err != nil {
		return err
	}
	r.Number = &v
	return r.Validate()
}

// Placement 定义一种角色的副本数和 AND 标签选择器。
type Placement struct {
	Replicas ReplicaCount      `yaml:"replicas" json:"replicas"`
	Selector map[string]string `yaml:"selector,omitempty" json:"selector,omitempty"`
}

// Matches 同时检查显式选择器要求与全部标签。
func (p Placement) Matches(labels map[string]string, explicit bool) bool {
	if explicit && len(p.Selector) == 0 {
		return false
	}
	for k, v := range p.Selector {
		if value, ok := labels[k]; !ok || value != v {
			return false
		}
	}
	return true
}

// SourceScheduling 将两种角色的数量和选择器独立管理。
type SourceScheduling struct {
	Cleaner   Placement `yaml:"cleaner" json:"cleaner"`
	Lifecycle Placement `yaml:"lifecycle" json:"lifecycle"`
}

// Clone 返回不共享可变标签/数量的副本。
func (s SourceScheduling) Clone() SourceScheduling {
	s.Cleaner = clonePlacement(s.Cleaner)
	s.Lifecycle = clonePlacement(s.Lifecycle)
	return s
}

func clonePlacement(p Placement) Placement {
	p.Selector = maps.Clone(p.Selector)
	if len(p.Selector) == 0 {
		p.Selector = nil
	}
	if p.Replicas.Number != nil {
		n := *p.Replicas.Number
		p.Replicas.Number = &n
	}
	return p
}

// Validate 校验副本与有界标签。
func (s SourceScheduling) Validate() error {
	for _, p := range []Placement{s.Cleaner, s.Lifecycle} {
		if err := p.Replicas.Validate(); err != nil {
			return err
		}
		if err := ValidateLabels(p.Selector); err != nil {
			return err
		}
	}
	return nil
}

// ValidateLabels 限制控制协议的标签载荷。
func ValidateLabels(labels map[string]string) error {
	if len(labels) > 32 {
		return fmt.Errorf("at most 32 labels allowed")
	}
	for k, v := range labels {
		if strings.TrimSpace(k) == "" || len(k) > 128 || len(v) > 256 {
			return fmt.Errorf("invalid label size")
		}
	}
	return nil
}
