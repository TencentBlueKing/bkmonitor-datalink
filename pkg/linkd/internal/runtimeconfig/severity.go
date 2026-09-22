// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package runtimeconfig 提供消费者共享的原子配置快照，不持有上游连接或后台任务。
package runtimeconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
)

// Snapshot 是单次业务处理可冻结的完整等级配置；Digest 不表示可回放历史版本。
type Snapshot struct {
	Enabled     bool                  `json:"enabled"`
	NativeNames bool                  `json:"native_names"`
	Digest      string                `json:"digest"`
	Severity    config.SeverityConfig `json:"severity"`
}

// Normalize 验证快照并生成不受列表顺序影响的内容摘要。
func (s Snapshot) Normalize() (Snapshot, error) {
	if len(s.Severity.Levels) == 0 || len(s.Severity.Levels) > 256 {
		return Snapshot{}, fmt.Errorf("severity requires 1..256 levels")
	}
	if err := s.Severity.Validate(); err != nil {
		return Snapshot{}, err
	}
	s.Severity = s.Severity.WithDefaults()
	slices.SortFunc(s.Severity.Levels, func(a, b config.SeverityLevel) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		return 0
	})
	s.Digest = ""
	b, err := json.Marshal(s)
	if err != nil {
		return Snapshot{}, err
	}
	h := sha256.Sum256(b)
	s.Digest = hex.EncodeToString(h[:])
	return s, nil
}

// Severity 原子替换进程当前快照；消费者在一次处理入口读取，避免同次裁决混用等级。
type Severity struct{ current atomic.Pointer[Snapshot] }

// NewSeverity 使用已经由启动配置校验的 YAML 等级构造内存状态。
func NewSeverity(c config.SeverityConfig) *Severity {
	s := &Severity{}
	v := Snapshot{Severity: c.WithDefaults()}
	// YAML 在进程装配前已校验；保留原值使错误仍由业务构造函数报告。
	if normalized, err := v.Normalize(); err == nil {
		v = normalized
	}
	s.current.Store(&v)
	return s
}

// SeveritySnapshot 返回不共享可变切片的当前快照。
func (s *Severity) SeveritySnapshot() Snapshot {
	v := *s.current.Load()
	v.Severity.Levels = slices.Clone(v.Severity.Levels)
	return v
}

// Install 校验整批后原子安装，失败保留旧值。
func (s *Severity) Install(v Snapshot) error {
	n, err := v.Normalize()
	if err != nil {
		return err
	}
	if v.Digest != "" && v.Digest != n.Digest {
		return fmt.Errorf("severity digest mismatch")
	}
	s.current.Store(&n)
	return nil
}

// Priority 适配等级查询；需要多次比较的调用方必须先取得 SeveritySnapshot。
func (s *Severity) Priority(name string) (int, bool) { return s.current.Load().Severity.Priority(name) }

// FreezeSeverity 适配 Lifecycle 消费方的窄接口，不让领域处理器依赖配置装配包。
func (s *Severity) FreezeSeverity() (lifecycle.SeverityTable, string) {
	snapshot := s.SeveritySnapshot()
	return snapshot.Severity, snapshot.Digest
}
