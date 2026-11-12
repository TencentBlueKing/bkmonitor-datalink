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
	Name       string
	Source     DefinitionSource
	Definition *T
	// HasBuiltin 内置有同名定义；MatchesBuiltin 与同名内置在读方使用的字段上一致，
	// 两者都为 true 时删掉这条 DB 定义不改变读方行为。
	HasBuiltin     bool
	MatchesBuiltin bool
}

// EffectiveSchema 是某个 namespace 的生效定义，按查找顺序给出每条定义的来源。
type EffectiveSchema struct {
	Provider  string
	Namespace string
	// Lookup 是查找顺序，例如 [bkcc__2 __all__ builtin]。
	Lookup    []string
	Resources []EffectiveDefinition[ResourceDefinition]
	Relations []EffectiveDefinition[RelationDefinition]
}

// SourceCount 是某类定义在某个来源下的生效条数。
type SourceCount struct {
	Kind   string
	Source DefinitionSource
	Count  int
}

// SchemaSourceInspector 由提供内置定义的 Provider 实现，用来观测生效定义来自哪一层。
type SchemaSourceInspector interface {
	EffectiveSchema(namespace string) EffectiveSchema
	// SourceCounts 统计 __all__ 的生效定义（内置、覆盖、新增）与全部业务 namespace 的 DB 定义，
	// 每个 kind 和来源的组合都返回一项，没有定义时 Count 为 0。
	SourceCounts() []SourceCount
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
