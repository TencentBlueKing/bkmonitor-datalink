// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "fmt"

const defaultSeverityName = "warning"

// SeverityLevel 定义 Linkd 严重程度名称和排序优先级；数值越小越严重。
type SeverityLevel struct {
	Name     string `yaml:"name"`
	Priority int    `yaml:"priority"`
}

// SeverityConfig 是进程级、运行期冻结的严重程度定义表。
type SeverityConfig struct {
	DefaultSeverity string          `yaml:"default_severity"`
	Levels          []SeverityLevel `yaml:"levels"`
}

// DefaultSeverityConfig 返回 define.md 规定的默认等级表。
func DefaultSeverityConfig() SeverityConfig {
	return SeverityConfig{
		DefaultSeverity: defaultSeverityName,
		Levels: []SeverityLevel{
			{Name: "critical", Priority: 1},
			{Name: "warning", Priority: 2},
			{Name: "info", Priority: 3},
		},
	}
}

// WithDefaults 在未自定义等级表时补齐完整默认表。
func (c SeverityConfig) WithDefaults() SeverityConfig {
	if len(c.Levels) == 0 {
		defaults := DefaultSeverityConfig()
		if c.DefaultSeverity != "" {
			defaults.DefaultSeverity = c.DefaultSeverity
		}
		return defaults
	}
	cloned := SeverityConfig{
		DefaultSeverity: c.DefaultSeverity,
		Levels:          append([]SeverityLevel(nil), c.Levels...),
	}
	if cloned.DefaultSeverity == "" {
		cloned.DefaultSeverity = defaultSeverityName
	}
	return cloned
}

// Validate 校验 name、priority 和全局默认值唯一且互相引用有效。
func (c SeverityConfig) Validate() error {
	c = c.WithDefaults()
	names := make(map[string]struct{}, len(c.Levels))
	priorities := make(map[int]struct{}, len(c.Levels))
	for index, level := range c.Levels {
		if err := validateBoundedText("severity.levels.name", level.Name, 1, 32); err != nil {
			return fmt.Errorf("severity.levels[%d]: %w", index, err)
		}
		if _, exists := names[level.Name]; exists {
			return fmt.Errorf("severity level name is duplicated: %q", level.Name)
		}
		if _, exists := priorities[level.Priority]; exists {
			return fmt.Errorf("severity priority is duplicated: %d", level.Priority)
		}
		names[level.Name] = struct{}{}
		priorities[level.Priority] = struct{}{}
	}
	if _, exists := names[c.DefaultSeverity]; !exists {
		return fmt.Errorf("severity.default_severity references unknown level %q", c.DefaultSeverity)
	}
	return nil
}

// Priority 返回 name 的排序值。
func (c SeverityConfig) Priority(name string) (int, bool) {
	for _, level := range c.WithDefaults().Levels {
		if level.Name == name {
			return level.Priority, true
		}
	}
	return 0, false
}

// Has 报告 name 是否存在。
func (c SeverityConfig) Has(name string) bool {
	_, ok := c.Priority(name)
	return ok
}
