// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DefinitionSource 是一条定义的生效来源，取值固定，可直接用作指标标签。
type DefinitionSource string

const (
	// DefinitionSourceBuiltin 内置定义，DB 没有同名定义。
	DefinitionSourceBuiltin DefinitionSource = "builtin"
	// DefinitionSourceOverride DB __all__ 的同名定义整条覆盖内置。
	DefinitionSourceOverride DefinitionSource = "override"
	// DefinitionSourceExtension DB __all__ 新增的定义，内置没有同名定义。
	DefinitionSourceExtension DefinitionSource = "extension"
	// DefinitionSourceNamespace 业务 namespace 在 DB 里的定义，覆盖 __all__ 与内置。
	DefinitionSourceNamespace DefinitionSource = "namespace"
)

// DefinitionSources 按固定顺序列出全部来源。
var DefinitionSources = []DefinitionSource{
	DefinitionSourceBuiltin, DefinitionSourceOverride, DefinitionSourceExtension, DefinitionSourceNamespace,
}

// EffectiveDefinition 是某个 namespace 下一条生效的定义。
type EffectiveDefinition[T any] struct {
	Name       string           `json:"name"`
	Source     DefinitionSource `json:"source"`
	Definition *T               `json:"definition,omitempty"`
	// HasBuiltin 内置有同名定义；MatchesBuiltin 与同名内置在读方使用的字段上一致，
	// 两者都为 true 时删掉这条 DB 定义不改变读方行为。
	HasBuiltin     bool `json:"has_builtin"`
	MatchesBuiltin bool `json:"matches_builtin"`
}

// EffectiveSchema 是某个 namespace 的生效定义，按查找顺序给出每条定义的来源。
type EffectiveSchema struct {
	Provider  string `json:"provider"`
	Namespace string `json:"namespace"`
	// Lookup 是查找顺序，例如 [bkcc__2 __all__ builtin]。
	Lookup    []string                                  `json:"lookup"`
	Overlay   OverlayStatus                             `json:"overlay"`
	Resources []EffectiveDefinition[ResourceDefinition] `json:"resources"`
	Relations []EffectiveDefinition[RelationDefinition] `json:"relations"`
}

// SourceCount 是某类定义在某个来源下的生效条数。
type SourceCount struct {
	Kind   string
	Source DefinitionSource
	Count  int
}

// OverlayState 是 Redis 覆盖层的状态：loaded 表示至少完成过一次全量加载（之后读取失败时沿用上次快照），
// unavailable 表示还没有加载成功过，此时只有内置一层。
type OverlayState string

const (
	OverlayStateLoaded      OverlayState = "loaded"
	OverlayStateUnavailable OverlayState = "unavailable"
)

// 覆盖层读取失败的原因，取值固定，可直接用作指标标签。
const (
	// OverlayReasonClientMissing 没有可用的 Redis 客户端，只有内置一层。
	OverlayReasonClientMissing = "client_missing"
	// OverlayReasonReadFailed 读取 Redis 失败（全量加载或按 namespace 重载）。
	OverlayReasonReadFailed = "read_failed"
	// OverlayReasonDecodeFailed 某个 namespace 或某条定义无法解码，该 namespace 沿用上次快照。
	OverlayReasonDecodeFailed = "decode_failed"
)

// OverlayReasons 按固定顺序列出全部失败原因。
var OverlayReasons = []string{OverlayReasonClientMissing, OverlayReasonReadFailed, OverlayReasonDecodeFailed}

// OverlayStatus 是 Redis 覆盖层的读取状态。
type OverlayStatus struct {
	State OverlayState `json:"state"`
	// LastSuccess 最近一次全量加载（启动、订阅建立、周期对账）成功的时间。
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastFailure *time.Time `json:"last_failure,omitempty"`
	LastReason  string     `json:"last_reason,omitempty"`
	// LastError 最近一次失败的错误文本，只用于诊断输出，不进指标。
	LastError string `json:"last_error,omitempty"`
	// Failures 按原因累计失败次数，每个原因都有一项。
	Failures map[string]int64 `json:"failures"`
}

type overlayStatus struct {
	lastSuccess time.Time
	lastFailure time.Time
	lastReason  string
	lastError   string
	failures    map[string]int64
}

func (s *overlayStatus) recordFailure(reason string, err error) {
	if s.failures == nil {
		s.failures = make(map[string]int64, len(OverlayReasons))
	}
	s.failures[reason]++
	s.lastFailure = time.Now()
	s.lastReason = reason
	if err != nil {
		s.lastError = err.Error()
	}
}

func (s *overlayStatus) snapshot() OverlayStatus {
	status := OverlayStatus{State: OverlayStateUnavailable, LastReason: s.lastReason, LastError: s.lastError,
		Failures: make(map[string]int64, len(OverlayReasons))}
	for _, reason := range OverlayReasons {
		status.Failures[reason] = s.failures[reason]
	}
	if !s.lastSuccess.IsZero() {
		status.State = OverlayStateLoaded
		lastSuccess := s.lastSuccess
		status.LastSuccess = &lastSuccess
	}
	if !s.lastFailure.IsZero() {
		lastFailure := s.lastFailure
		status.LastFailure = &lastFailure
	}
	return status
}

// SchemaSourceInspector 由提供内置定义的 Provider 实现，用来观测生效定义来自哪一层。
type SchemaSourceInspector interface {
	EffectiveSchema(namespace string) EffectiveSchema
	// SourceCounts 统计 __all__ 的生效定义（内置、覆盖、新增）与全部业务 namespace 的 DB 定义，
	// 每个 kind 和来源的组合都返回一项，没有定义时 Count 为 0。
	SourceCounts() []SourceCount
	OverlayStatus() OverlayStatus
}

type layeredEntry[T any] struct {
	name       string
	source     DefinitionSource
	definition *T
	builtin    *T
}

// lookupLayered 按 业务 namespace（DB）→ __all__（DB）→ 内置 的顺序查找同名定义。
func lookupLayered[T any](overlay map[string]map[string]*T, builtin map[string]*T, namespace, name string) (*T, bool) {
	if namespace != NamespaceAll {
		if definition, ok := overlay[namespace][name]; ok {
			return definition, true
		}
	}
	if definition, ok := overlay[NamespaceAll][name]; ok {
		return definition, true
	}
	definition, ok := builtin[name]
	return definition, ok
}

// listLayered 按查找顺序合并三层，同名只取上层整条定义，不做字段级合并；
// 输出先按层、层内按名字排序，保证按资源类型匹配时上层优先且结果稳定。
func listLayered[T any](overlay map[string]map[string]*T, builtin map[string]*T, namespace string) []layeredEntry[T] {
	seen := make(map[string]struct{}, len(builtin))
	result := make([]layeredEntry[T], 0, len(builtin))
	add := func(definitions map[string]*T, source func(name string) DefinitionSource) {
		names := make([]string, 0, len(definitions))
		for name := range definitions {
			if _, ok := seen[name]; !ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			seen[name] = struct{}{}
			result = append(result, layeredEntry[T]{
				name: name, source: source(name), definition: definitions[name], builtin: builtin[name],
			})
		}
	}
	if namespace != NamespaceAll {
		add(overlay[namespace], func(string) DefinitionSource { return DefinitionSourceNamespace })
	}
	add(overlay[NamespaceAll], func(name string) DefinitionSource {
		if _, ok := builtin[name]; ok {
			return DefinitionSourceOverride
		}
		return DefinitionSourceExtension
	})
	add(builtin, func(string) DefinitionSource { return DefinitionSourceBuiltin })
	return result
}

func definitionsOf[T any](entries []layeredEntry[T]) []*T {
	result := make([]*T, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.definition)
	}
	return result
}

func effectiveDefinitions[T any](entries []layeredEntry[T], same func(override, builtin *T) bool) []EffectiveDefinition[T] {
	result := make([]EffectiveDefinition[T], 0, len(entries))
	for _, entry := range entries {
		item := EffectiveDefinition[T]{Name: entry.name, Source: entry.source, Definition: entry.definition}
		if entry.builtin != nil {
			item.HasBuiltin = true
			item.MatchesBuiltin = entry.source == DefinitionSourceBuiltin || same(entry.definition, entry.builtin)
		}
		result = append(result, item)
	}
	return result
}

// countSources 统计 __all__ 的三层来源，以及全部业务 namespace 在 DB 里的定义条数。
func countSources[T any](kind string, overlay map[string]map[string]*T, builtin map[string]*T) []SourceCount {
	counts := make(map[DefinitionSource]int, len(DefinitionSources))
	for _, entry := range listLayered(overlay, builtin, NamespaceAll) {
		counts[entry.source]++
	}
	for namespace, definitions := range overlay {
		if namespace != NamespaceAll {
			counts[DefinitionSourceNamespace] += len(definitions)
		}
	}
	result := make([]SourceCount, 0, len(DefinitionSources))
	for _, source := range DefinitionSources {
		result = append(result, SourceCount{Kind: kind, Source: source, Count: counts[source]})
	}
	return result
}

func lookupOrder(namespace string) []string {
	if namespace == NamespaceAll {
		return []string{NamespaceAll, string(DefinitionSourceBuiltin)}
	}
	return []string{namespace, NamespaceAll, string(DefinitionSourceBuiltin)}
}

// SameResourceDefinition 判断覆盖定义与内置定义在读方使用的字段上是否一致：
// 字段名、是否主键与顺序；字段缺类型时读方按内置类型补齐，视为一致。
func SameResourceDefinition(override, builtin *ResourceDefinition) bool {
	if override == nil || builtin == nil || len(override.Fields) != len(builtin.Fields) {
		return false
	}
	for i, field := range override.Fields {
		want := builtin.Fields[i]
		if field.Name != want.Name || field.Required != want.Required {
			return false
		}
		if field.Type != "" && field.Type != want.Type {
			return false
		}
	}
	return true
}

// SameRelationDefinition 判断覆盖定义与内置定义在读方使用的字段上是否一致：
// 两端资源、类别、方向、从属标记与 labels 里的 metric_name。
func SameRelationDefinition(override, builtin *RelationDefinition) bool {
	if override == nil || builtin == nil {
		return false
	}
	return override.FromResource == builtin.FromResource &&
		override.ToResource == builtin.ToResource &&
		ToRelationCategory(override.Category) == ToRelationCategory(builtin.Category) &&
		override.IsDirectional == builtin.IsDirectional &&
		override.IsBelongsTo == builtin.IsBelongsTo &&
		override.Labels["metric_name"] == builtin.Labels["metric_name"]
}

// PrintEffectiveSchema 以文本输出 namespace 的生效定义及来源，content 为 true 时附带定义内容。
func PrintEffectiveSchema(schema EffectiveSchema, content bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider: %s\nnamespace: %s\nlookup: %s\n", schema.Provider, schema.Namespace, strings.Join(schema.Lookup, " -> "))
	writeOverlayStatus(&b, schema.Overlay)
	writeCounts := func(kind string, sources []DefinitionSource, differs int) {
		fmt.Fprintf(&b, "%s:", kind)
		for _, source := range DefinitionSources {
			count := 0
			for _, item := range sources {
				if item == source {
					count++
				}
			}
			fmt.Fprintf(&b, " %s=%d", source, count)
		}
		fmt.Fprintf(&b, " differs_from_builtin=%d\n", differs)
	}
	resourceSources, resourceDiffers := summarize(schema.Resources)
	relationSources, relationDiffers := summarize(schema.Relations)
	writeCounts(KindResourceDefinition, resourceSources, resourceDiffers)
	writeCounts(KindRelationDefinition, relationSources, relationDiffers)
	writeEffectiveDefinitions(&b, KindResourceDefinition, schema.Resources, content)
	writeEffectiveDefinitions(&b, KindRelationDefinition, schema.Relations, content)
	return b.String()
}

func writeOverlayStatus(b *strings.Builder, status OverlayStatus) {
	formatTime := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.UTC().Format(time.RFC3339)
	}
	reason := status.LastReason
	if reason == "" {
		reason = "-"
	}
	fmt.Fprintf(b, "overlay: %s last_success=%s last_failure=%s last_reason=%s failures:", status.State,
		formatTime(status.LastSuccess), formatTime(status.LastFailure), reason)
	for _, item := range OverlayReasons {
		fmt.Fprintf(b, " %s=%d", item, status.Failures[item])
	}
	b.WriteString("\n")
	if status.LastError != "" {
		fmt.Fprintf(b, "overlay_last_error: %s\n", status.LastError)
	}
}

func summarize[T any](items []EffectiveDefinition[T]) ([]DefinitionSource, int) {
	sources := make([]DefinitionSource, 0, len(items))
	differs := 0
	for _, item := range items {
		sources = append(sources, item.Source)
		if item.HasBuiltin && !item.MatchesBuiltin {
			differs++
		}
	}
	return sources, differs
}

func writeEffectiveDefinitions[T any](b *strings.Builder, kind string, items []EffectiveDefinition[T], content bool) {
	fmt.Fprintf(b, "-----------------------------------\n%s\n", kind)
	for _, item := range items {
		builtin := "none"
		if item.HasBuiltin {
			builtin = "same"
			if !item.MatchesBuiltin {
				builtin = "differs"
			}
		}
		fmt.Fprintf(b, "%s\t%s\tbuiltin=%s\n", item.Name, item.Source, builtin)
		if content {
			data, err := json.Marshal(item.Definition)
			if err != nil {
				fmt.Fprintf(b, "\tencode error: %v\n", err)
				continue
			}
			fmt.Fprintf(b, "\t%s\n", data)
		}
	}
}
