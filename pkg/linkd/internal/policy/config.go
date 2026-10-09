// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
)

// Kind 区分三类策略的配置及运行边界。
type Kind string

const (
	Suppression Kind = "suppression"
	Shield      Kind = "shield"
	Merge       Kind = "merge"
)

func (k Kind) Valid() bool { return k == Suppression || k == Shield || k == Merge }

// FieldMapping 显式声明自定义 KAC 字段的有效 Event/Alert 路径与查询类型，禁止原始 payload 兜底。
type FieldMapping struct {
	Path string    `json:"path"`
	Kind FieldKind `json:"kind"`
}

// CommonSpec 保存三类策略公共配置；租户和策略身份在发布信封中提供。
type CommonSpec struct {
	Name          string                     `json:"name"`
	Enabled       bool                       `json:"is_enable"`
	UpdatedAt     time.Time                  `json:"updated_at"`
	SpaceCode     string                     `json:"space_code"`
	Timezone      string                     `json:"timezone"`
	Times         []ActiveTime               `json:"activate_times"`
	ModelID       string                     `json:"model_id,omitempty"`
	Targets       *onemodel.TargetDescriptor `json:"target_descriptor,omitempty"`
	FieldMappings map[string]FieldMapping    `json:"field_mappings,omitempty"`
}

// Scheme 保留防抖/聚合配置，Seconds 由发布时计算，不能由同步端伪造。
type Scheme struct {
	Name         string   `json:"name,omitempty"`
	Type         string   `json:"type"`
	Duration     int64    `json:"duration"`
	DurationType string   `json:"duration_type"`
	Count        int      `json:"count,omitempty"`
	Fields       []string `json:"fields,omitempty"`
}

// SuppressionSpec 按防抖后聚合顺序执行。
type SuppressionSpec struct {
	CommonSpec
	Policy  json.RawMessage `json:"policy"`
	Schemes []Scheme        `json:"scheme"`
}

// ShieldSpec 的 Policy 选主，RelyPolicy 选被屏蔽候选；Before/After 单位为分钟。
type ShieldSpec struct {
	CommonSpec
	Policy     json.RawMessage `json:"policy"`
	RelyPolicy json.RawMessage `json:"rely_policy,omitempty"`
	ShieldType string          `json:"shield_type"`
	ShieldMode string          `json:"shield_mode,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Before     int64           `json:"time_range_before,omitempty"`
	After      int64           `json:"time_range_after,omitempty"`
	AlarmTags  []int64         `json:"alarm_tags,omitempty"`
}

// TemplateField 使用 KAC 的 key/value 模板写法。
type TemplateField struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// MergeSpec 的 Policy 是条件组，周期和聚合字段不可按普通条件对象解释。
type MergeSpec struct {
	CommonSpec
	Policy    []json.RawMessage `json:"policy"`
	Cycle     int64             `json:"merge_cycle"`
	Cyclic    bool              `json:"is_cycle_merge"`
	Fields    []string          `json:"aggregate_fields"`
	Template  []TemplateField   `json:"new_alarm_config"`
	AlarmTags []int64           `json:"alarm_tags,omitempty"`
	// MaxMergeFieldLength 限制每个 cw_merged 变量的 Unicode 字符数；省略时在发布阶段固定为 500。
	MaxMergeFieldLength int `json:"max_merge_field_length"`
}

// NormalizedScheme 保存以秒为单位的可执行预算。
type NormalizedScheme struct {
	Type    string   `json:"type"`
	Seconds int64    `json:"seconds"`
	Count   int      `json:"count,omitempty"`
	Fields  []string `json:"fields,omitempty"`
}

// Summary 是可查询的编译结果；表达式运行对象由不可变原始配置重新编译。
type Summary struct {
	CompilerVersion int                `json:"compiler_version"`
	Digest          string             `json:"digest"`
	Timezone        string             `json:"timezone"`
	Schemes         []NormalizedScheme `json:"scheme,omitempty"`
	ConditionGroups int                `json:"condition_groups"`
	TargetSelectors int                `json:"target_selectors"`
}

// Compiled 固定配置、字段目录与表达式；运行时还必须验证租户、业务范围和目标集合。
type Compiled struct {
	Kind        Kind
	Common      CommonSpec
	Suppression *SuppressionSpec
	Shield      *ShieldSpec
	Merge       *MergeSpec
	Template    *MergeTemplate
	Conditions  []*Expression
	Rely        *Expression
	Schedule    *Schedule
	Summary     Summary
	Canonical   json.RawMessage
}

// Compile 拒绝未知字段、无效目标和不支持的条件，不把空配置解释成全匹配。
func Compile(kind Kind, raw json.RawMessage) (*Compiled, error) {
	if !kind.Valid() || len(raw) == 0 || len(raw) > 2<<20 {
		return nil, fmt.Errorf("invalid policy type or configuration size")
	}
	normalized, err := (domain.JSONObject{"spec": raw}).Normalize()
	if err != nil {
		return nil, fmt.Errorf("invalid or duplicate configuration JSON")
	}
	raw = normalized["spec"]
	if len(raw) > 2<<20 {
		return nil, fmt.Errorf("normalized policy exceeds 2 MiB")
	}
	result := &Compiled{Kind: kind, Summary: Summary{CompilerVersion: 1}}
	var policies []json.RawMessage
	var canonical any
	switch kind {
	case Suppression:
		result.Suppression = &SuppressionSpec{}
		if err := strictDecode(raw, result.Suppression); err != nil {
			return nil, err
		}
		result.Common = result.Suppression.CommonSpec
		policies = []json.RawMessage{result.Suppression.Policy}
		canonical = result.Suppression
	case Shield:
		result.Shield = &ShieldSpec{}
		if err := strictDecode(raw, result.Shield); err != nil {
			return nil, err
		}
		result.Common = result.Shield.CommonSpec
		policies = []json.RawMessage{result.Shield.Policy}
		canonical = result.Shield
	case Merge:
		result.Merge = &MergeSpec{}
		if err := strictDecode(raw, result.Merge); err != nil {
			return nil, err
		}
		if result.Merge.MaxMergeFieldLength == 0 {
			var supplied map[string]json.RawMessage
			if err := json.Unmarshal(raw, &supplied); err != nil {
				return nil, err
			}
			if _, present := supplied["max_merge_field_length"]; !present {
				result.Merge.MaxMergeFieldLength = 500
			}
		}
		result.Common = result.Merge.CommonSpec
		policies = result.Merge.Policy
		canonical = result.Merge
	}
	if strings.TrimSpace(result.Common.Name) == "" || len(result.Common.Name) > 256 || result.Common.UpdatedAt.IsZero() || result.Common.SpaceCode == "" || len(result.Common.SpaceCode) > 128 {
		return nil, fmt.Errorf("name, updated_at and bounded space_code are required")
	}
	if _, err := onemodel.ParseBusinessSpace(result.Common.SpaceCode); err != nil {
		return nil, err
	}
	if result.Common.Timezone == "" {
		result.Common.Timezone = "Asia/Shanghai"
	}
	result.Common.UpdatedAt = result.Common.UpdatedAt.UTC()
	if (result.Common.ModelID == "") != (result.Common.Targets == nil) {
		return nil, fmt.Errorf("model_id and target_descriptor must be provided together")
	}
	if result.Common.Targets != nil {
		if result.Common.Targets.ModelID != result.Common.ModelID {
			return nil, fmt.Errorf("target_descriptor model mismatch")
		}
		if err := result.Common.Targets.Validate(); err != nil {
			return nil, err
		}
		result.Summary.TargetSelectors = len(result.Common.Targets.Selectors)
	}
	schedule, err := CompileSchedule(result.Common.Timezone, result.Common.Times)
	if err != nil {
		return nil, err
	}
	result.Schedule = schedule
	fields := KACFields()
	if len(result.Common.FieldMappings) > 64 {
		return nil, fmt.Errorf("field_mappings exceeds 64 entries")
	}
	for key, mapping := range result.Common.FieldMappings {
		if key == "" || len(key) > 128 || fields[key] != "" {
			return nil, fmt.Errorf("custom field cannot be empty or override built-in field")
		}
		target, err := jsonpath.ParseTarget(mapping.Path)
		if err != nil {
			return nil, fmt.Errorf("field_mappings.%s: invalid exact path", key)
		}
		parts := target.Parts()
		if len(parts) < 2 || (parts[0] != "labels" && parts[0] != "extra_data") {
			return nil, fmt.Errorf("field_mappings.%s: only labels/extra_data are readable", key)
		}
		switch mapping.Kind {
		case FieldText, FieldKeyword, FieldNumber, FieldBoolean, FieldDate:
		default:
			return nil, fmt.Errorf("field_mappings.%s: invalid kind", key)
		}
		fields[key] = mapping.Kind
	}
	if len(policies) == 0 || len(policies) > 32 {
		return nil, fmt.Errorf("policy requires 1..32 condition groups")
	}
	for i, policy := range policies {
		expression, err := CompileExpression(policy, fields)
		if err != nil {
			return nil, fmt.Errorf("policy[%d]: %w", i, err)
		}
		result.Conditions = append(result.Conditions, expression)
	}
	switch kind {
	case Suppression:
		spec := result.Suppression
		spec.CommonSpec = result.Common
		if len(spec.Schemes) == 0 || len(spec.Schemes) > 2 {
			return nil, fmt.Errorf("scheme requires one clip and/or aggregation")
		}
		seen := map[string]bool{}
		for _, scheme := range spec.Schemes {
			if seen[scheme.Type] {
				return nil, fmt.Errorf("duplicate scheme type")
			}
			seen[scheme.Type] = true
			seconds, err := durationSeconds(scheme.Duration, scheme.DurationType)
			if err != nil {
				return nil, err
			}
			switch scheme.Type {
			case "clip":
				if scheme.Count < 1 || scheme.Count > 10000 || len(scheme.Fields) != 0 {
					return nil, fmt.Errorf("clip count must be 1..10000 without fields")
				}
			case "aggregation":
				if scheme.Count != 0 || len(scheme.Fields) == 0 {
					return nil, fmt.Errorf("aggregation requires fields without count")
				}
				if err := validateGroupFields(scheme.Fields, fields); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("unsupported scheme type")
			}
			result.Summary.Schemes = append(result.Summary.Schemes, NormalizedScheme{Type: scheme.Type, Seconds: seconds, Count: scheme.Count, Fields: slices.Clone(scheme.Fields)})
		}
		slices.SortFunc(result.Summary.Schemes, func(a, b NormalizedScheme) int {
			if a.Type == b.Type {
				return 0
			}
			if a.Type == "clip" {
				return -1
			}
			return 1
		})
	case Shield:
		spec := result.Shield
		spec.CommonSpec = result.Common
		if spec.Targets == nil {
			return nil, fmt.Errorf("shield requires target_descriptor")
		}
		if len(spec.Reason) > 4096 || spec.Before < 0 || spec.Before > 10080 || spec.After < 0 || spec.After > 10080 {
			return nil, fmt.Errorf("shield reason/time range exceeds budget")
		}
		if err := validateTags(spec.AlarmTags); err != nil {
			return nil, err
		}
		switch spec.ShieldType {
		case "time_shield":
			if spec.ShieldMode != "" || len(spec.RelyPolicy) != 0 || spec.Before != 0 || spec.After != 0 {
				return nil, fmt.Errorf("time shield cannot include dependency fields")
			}
		case "rely_shield":
			if spec.ShieldMode != "custom_shield" && spec.ShieldMode != "cmdb_shield" {
				return nil, fmt.Errorf("invalid shield_mode")
			}
			result.Rely, err = compileExpression(spec.RelyPolicy, fields, true)
			if err != nil {
				return nil, fmt.Errorf("rely_policy: %w", err)
			}
		default:
			return nil, fmt.Errorf("unsupported shield_type")
		}
	case Merge:
		spec := result.Merge
		spec.CommonSpec = result.Common
		if spec.Cycle < 1 || spec.Cycle > 86400 {
			return nil, fmt.Errorf("merge_cycle must be 1..86400 seconds")
		}
		if err := validateGroupFields(spec.Fields, fields); err != nil {
			return nil, err
		}
		if err := validateTags(spec.AlarmTags); err != nil {
			return nil, err
		}
		result.Template, err = compileMergeTemplate(*spec, fields)
		if err != nil {
			return nil, err
		}
	}
	result.Summary.Timezone = result.Common.Timezone
	result.Summary.ConditionGroups = len(result.Conditions)
	result.Canonical, err = json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(append([]byte("policy-compiler-v1:"+string(kind)+":"), result.Canonical...))
	result.Summary.Digest = hex.EncodeToString(digest[:])
	return result, nil
}

func durationSeconds(value int64, unit string) (int64, error) {
	multiplier := int64(0)
	switch unit {
	case "second":
		multiplier = 1
	case "minute":
		multiplier = 60
	case "hour":
		multiplier = 3600
	default:
		return 0, fmt.Errorf("duration_type must be second/minute/hour")
	}
	if value <= 0 || value > 2592000/multiplier {
		return 0, fmt.Errorf("duration must be positive and at most 30 days")
	}
	return value * multiplier, nil
}

func validateGroupFields(values []string, fields FieldCatalog) error {
	if len(values) > 32 {
		return fmt.Errorf("group fields exceeds 32")
	}
	seen := map[string]bool{}
	for _, field := range values {
		if fields[field] == "" || seen[field] {
			return fmt.Errorf("unsupported or duplicate group field %q", field)
		}
		seen[field] = true
	}
	return nil
}

func validateTags(tags []int64) error {
	if len(tags) > 128 {
		return fmt.Errorf("alarm_tags exceeds 128")
	}
	seen := map[int64]bool{}
	for _, tag := range tags {
		if tag <= 0 || tag >= 1<<53 || seen[tag] {
			return fmt.Errorf("alarm_tags requires distinct positive IDs below 2^53")
		}
		seen[tag] = true
	}
	return nil
}

// Active 只回答启用/时段门槛；依赖屏蔽忽略时段，目标与业务范围仍须单独求交集。
func (c *Compiled) Active(at time.Time) bool {
	return c.Common.Enabled && ((c.Shield != nil && c.Shield.ShieldType == "rely_shield") || c.Schedule.Active(at))
}
