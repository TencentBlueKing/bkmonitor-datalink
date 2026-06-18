// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	FormatESStep     = "."
	FormatProperties = "properties"

	FormatPropertiesType       = "type"
	FormatPropertiesDocValue   = "doc_values"
	FormatPropertiesNormalizer = "normalizer"
	FormatPropertiesAnalyzer   = "analyzer"

	// Analyzer configuration keys
	AnalyzerKeyTokenizeOnChars = "tokenize_on_chars"
	AnalyzerKeyFilter          = "filter"
	AnalyzerKeyType            = "type"

	// Analyzer filter constants
	AnalyzerFilterLowercase  = "lowercase"
	AnalyzerFilterICUFolding = "icu_folding"
)

type IndexOptionFormat struct {
	analyzer   map[string]map[string]any
	filter     map[string]map[string]any
	normalizer map[string]map[string]any
	fieldsMap  metadata.FieldsMap

	fieldAlias              metadata.FieldAlias
	wildcardCaseInsensitive *bool
	hasAnalysisSettings     bool
}

func NewIndexOptionFormat(fieldAlias map[string]string) *IndexOptionFormat {
	return &IndexOptionFormat{
		analyzer:   make(map[string]map[string]any),
		filter:     make(map[string]map[string]any),
		normalizer: make(map[string]map[string]any),
		fieldsMap:  make(metadata.FieldsMap),
		fieldAlias: fieldAlias,
	}
}

func (f *IndexOptionFormat) FieldsMap() metadata.FieldsMap {
	if f.wildcardCaseInsensitive != nil {
		for k, v := range f.fieldsMap {
			// 多索引解析时低版本索引可能后出现，需要在返回前统一回填最终能力值。
			v.WildcardCaseInsensitive = *f.wildcardCaseInsensitive
			f.fieldsMap[k] = v
		}
	}
	return f.fieldsMap
}

func (f *IndexOptionFormat) Parse(settings, mappings map[string]any) {
	f.updateWildcardCaseInsensitive(settings)
	// analyzer/filter/normalizer 都是 index 级 settings，不能跨 Parse 调用复用；
	// 否则上一个 index 的 analysis.analyzer.default 会污染下一个 index。
	f.analyzer = make(map[string]map[string]any)
	f.filter = make(map[string]map[string]any)
	f.normalizer = make(map[string]map[string]any)
	f.hasAnalysisSettings = false
	f.parseAnalysis(settings)
	f.parseMappings(mappings)
}

func (f *IndexOptionFormat) parseAnalysis(settings map[string]any) {
	// 解析 settings 里面的 analysis
	// 支持两种结构：直接 settings["analysis"] 或 settings["index"]["analysis"]
	var analysis map[string]any
	if a, ok := settings["analysis"].(map[string]any); ok {
		analysis = a
	} else if index, ok := settings["index"].(map[string]any); ok {
		analysis, _ = index["analysis"].(map[string]any)
	}

	if analysis != nil {
		f.hasAnalysisSettings = true
		tokenizer, _ := analysis["tokenizer"].(map[string]any)
		analyzer, _ := analysis["analyzer"].(map[string]any)
		filter, _ := analysis["filter"].(map[string]any)
		normalizer, _ := analysis["normalizer"].(map[string]any)

		for k, v := range filter {
			if nv, ok := v.(map[string]any); ok {
				f.filter[k] = nv
			}
		}

		for k, v := range normalizer {
			if nv, ok := v.(map[string]any); ok {
				f.normalizer[k] = nv
			}
		}

		for k, v := range analyzer {
			if nv, ok := v.(map[string]any); ok {
				f.analyzer[k] = nv
				if ck, ok := nv["tokenizer"].(string); ok && ck != "" {
					if cv, ok := tokenizer[ck]; ok {
						if ncv, ok := cv.(map[string]any); ok {
							for tk, tv := range ncv {
								if tk == AnalyzerKeyType {
									continue
								}
								f.analyzer[k][tk] = tv
							}
						}
					}
				}
			}
		}
	}
}

func (f *IndexOptionFormat) parseMappings(mappings map[string]any) {
	if _, ok := mappings[FormatProperties]; ok {
		f.mapMappings("", mappings)
	} else {
		// 有的 es 因为版本不同，properties 不在第一层，所以需要往下一层找
		for _, m := range mappings {
			switch nm := m.(type) {
			case map[string]any:
				f.parseMappings(nm)
			}
		}
	}
}

func (f *IndexOptionFormat) mapMappings(prefix string, data map[string]any) {
	if data == nil {
		return
	}

	if properties, ok := data[FormatProperties].(map[string]any); ok {
		for k, v := range properties {
			if prefix != "" {
				k = fmt.Sprintf("%s%s%s", prefix, FormatESStep, k)
			}

			switch nv := v.(type) {
			case map[string]any:
				f.mapMappings(k, nv)
			}
		}
	}

	if prefix != "" {
		fm := f.esToFieldMap(prefix, data)
		// 忽略为空的类型和 alias 类型，因为别名已经在 unifyquery 实现过了
		if !fm.Existed() || fm.FieldType == "alias" {
			return
		}

		if existing, ok := f.fieldsMap[prefix]; ok {
			// 同一字段可能来自多个索引；不能只保留首个索引的大小写语义。
			f.fieldsMap[prefix] = mergeFieldOption(existing, fm)
			return
		}

		f.fieldsMap[prefix] = fm
	}
}

func (f *IndexOptionFormat) setValue(k string, data map[string]any) any {
	if v, ok := data[k]; ok && v != nil {
		return v
	}

	return nil
}

func (f *IndexOptionFormat) esToFieldMap(k string, data map[string]any) metadata.FieldOption {
	fieldMap := metadata.FieldOption{}
	if k == "" {
		return fieldMap
	}
	if data["type"] == nil {
		return fieldMap
	}

	fieldMap.AliasName = f.fieldAlias.AliasName(k)
	fieldMap.FieldName = k
	fieldMap.FieldType, _ = data["type"].(string)
	fieldMap.IsAgg = !lo.Contains(nonAggTypes, fieldMap.FieldType)

	fieldMap.TokenizeOnChars = make([]string, 0)
	ks := strings.Split(k, ESStep)
	fieldMap.OriginField = ks[0]
	fieldMap.IsAnalyzed = false

	// 如果 mapping 中显式设置了 doc_values，以显式设置为准
	if v, ok := data["doc_values"].(bool); ok {
		fieldMap.IsAgg = v
	}

	fieldMap.IsAnalyzed = fieldMap.FieldType == Text

	// IsCaseSensitive 表示字段索引侧是否保留大小写差异。后续 wildcard 查询会用它判断是否需要手动 lower 用户输入：
	// - keyword 看 normalizer；没有 normalizer 时原值入索引，默认大小写敏感。
	// - text 看索引 analyzer 是否具备 lowercase/casefold 能力；wildcard 不经搜索 analyzer。
	//   参考 ES 文档：analyzer 影响索引分析链路。
	//   https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/analyzer
	// - 未知 analyzer/normalizer 按大小写敏感处理，避免错误 lower 导致查不到保留大小写的索引 term。
	switch fieldMap.FieldType {
	case KeyWord:
		fieldMap.IsCaseSensitive = !f.normalizerLowercases(cast.ToString(data[FormatPropertiesNormalizer]))
		fieldMap.IsCaseInsensitive = !fieldMap.IsCaseSensitive
	case Text:
		indexAnalyzer := cast.ToString(data[FormatPropertiesAnalyzer])
		if indexAnalyzer == "" {
			if _, ok := f.analyzer["default"]; ok {
				// 字段未显式配置 analyzer 时，ES 会优先使用索引级 analysis.analyzer.default。
				indexAnalyzer = "default"
			} else {
				// 索引也没有 default analyzer 时才回退到 ES 内置 standard analyzer。
				indexAnalyzer = "standard"
			}
		}

		if analyzer := f.analyzer[indexAnalyzer]; analyzer != nil {
			toc := cast.ToStringSlice(analyzer[AnalyzerKeyTokenizeOnChars])
			if len(toc) > 0 {
				fieldMap.TokenizeOnChars = toc
			}
		}

		fieldMap.IsCaseSensitive = !f.analyzerLowercases(indexAnalyzer)
	case "wildcard":
		fieldMap.IsCaseSensitive = true
	}
	if f.wildcardCaseInsensitive != nil {
		fieldMap.WildcardCaseInsensitive = *f.wildcardCaseInsensitive
	}

	return fieldMap
}

func mergeFieldOption(existing, next metadata.FieldOption) metadata.FieldOption {
	existingAffectsWildcard := fieldCaseSensitivityAffectsWildcard(existing)
	nextAffectsWildcard := fieldCaseSensitivityAffectsWildcard(next)
	if !existingAffectsWildcard || !nextAffectsWildcard {
		return existing
	}

	merged := existing
	// 任一索引侧会 lowercase 时，查询也需要覆盖 lowercase term；
	// 同时记录混合语义，供 fallback 生成原 pattern + lower pattern。
	merged.IsCaseSensitive = existing.IsCaseSensitive && next.IsCaseSensitive
	merged.IsAnalyzed = existing.IsAnalyzed || next.IsAnalyzed
	merged.IsCaseInsensitive = existing.IsCaseInsensitive || next.IsCaseInsensitive
	merged.IsMixedCaseSensitivity = existing.IsMixedCaseSensitivity || next.IsMixedCaseSensitivity ||
		existing.IsCaseSensitive != next.IsCaseSensitive
	if len(merged.TokenizeOnChars) == 0 && len(next.TokenizeOnChars) > 0 {
		merged.TokenizeOnChars = next.TokenizeOnChars
	}
	return merged
}

func fieldCaseSensitivityAffectsWildcard(field metadata.FieldOption) bool {
	return field.IsAnalyzed || field.FieldType == KeyWord || field.FieldType == "wildcard"
}

func (f *IndexOptionFormat) updateWildcardCaseInsensitive(settings map[string]any) {
	support := settingsSupportsWildcardCaseInsensitive(settings)
	if f.wildcardCaseInsensitive == nil {
		f.wildcardCaseInsensitive = &support
		return
	}
	// 一个查询可能覆盖多个索引；只有所有索引都支持时才可下发 case_insensitive。
	*f.wildcardCaseInsensitive = *f.wildcardCaseInsensitive && support
}

// normalizerLowercases 判断 keyword 的 normalizer 是否会把索引值归一化为小写。
func (f *IndexOptionFormat) normalizerLowercases(name string) bool {
	if name == "" {
		return false
	}
	if filterLowercasesByType(name) {
		return true
	}

	return f.filtersLowercase(cast.ToStringSlice(f.normalizer[name][AnalyzerKeyFilter]))
}

// analyzerLowercases 判断 text 的索引 analyzer 是否会把 token 转成小写。
func (f *IndexOptionFormat) analyzerLowercases(name string) bool {
	if builtinAnalyzerLowercases(name) {
		return true
	}

	analyzer := f.analyzer[name]
	if analyzer == nil {
		return !f.hasAnalysisSettings
	}
	analyzerType := cast.ToString(analyzer[AnalyzerKeyType])
	if strings.EqualFold(analyzerType, "pattern") {
		// pattern analyzer 有 lowercase 开关，显式 false 时必须保留大小写。
		if lowercase, ok := analyzer["lowercase"]; ok {
			return cast.ToBool(lowercase)
		}
		return true
	}
	if analyzerType != "" && analyzerType != "custom" {
		// 自定义名称可包装内置 analyzer，例如 {"type":"standard"}，需按 type 判定。
		if builtinAnalyzerLowercases(analyzerType) {
			return true
		}
	}

	return f.filtersLowercase(cast.ToStringSlice(analyzer[AnalyzerKeyFilter]))
}

func builtinAnalyzerLowercases(name string) bool {
	switch strings.ToLower(name) {
	case "standard", "simple", "stop", "pattern", "fingerprint",
		"arabic", "armenian", "basque", "bengali", "brazilian", "bulgarian",
		"catalan", "cjk", "czech", "danish", "dutch", "english", "estonian",
		"finnish", "french", "galician", "german", "greek", "hindi",
		"hungarian", "indonesian", "irish", "italian", "latvian",
		"lithuanian", "norwegian", "persian", "portuguese", "romanian",
		"russian", "serbian", "sorani", "spanish", "swedish", "turkish", "thai":
		return true
	default:
		return false
	}
}

// filtersLowercase 只要过滤链中存在 lowercase/casefold 类 filter，就认为该链路会统一大小写。
func (f *IndexOptionFormat) filtersLowercase(filters []string) bool {
	for _, name := range filters {
		if f.filterLowercases(name) {
			return true
		}
	}

	return false
}

// filterLowercases 同时支持内置 filter 名称和自定义 filter 名称；自定义 filter 需继续查看 analysis.filter[name].type。
func (f *IndexOptionFormat) filterLowercases(name string) bool {
	if filterLowercasesByType(name) {
		return true
	}

	filter := f.filter[name]
	if filter == nil {
		return false
	}

	switch cast.ToString(filter[AnalyzerKeyType]) {
	case AnalyzerFilterLowercase, AnalyzerFilterICUFolding:
		return true
	default:
		return false
	}
}

func filterLowercasesByType(name string) bool {
	switch strings.ToLower(name) {
	case AnalyzerFilterLowercase, AnalyzerFilterICUFolding:
		return true
	default:
		return false
	}
}

func settingsSupportsWildcardCaseInsensitive(settings map[string]any) bool {
	versionCreated := indexVersionCreated(settings)
	// wildcard.case_insensitive 是 ES 7.10 引入的参数，旧版本会直接拒绝查询。
	return versionCreated >= 7100000
}

func indexVersionCreated(settings map[string]any) int {
	if settings == nil {
		return 0
	}
	if version := cast.ToInt(settings["index.version.created"]); version > 0 {
		return version
	}
	index, _ := settings["index"].(map[string]any)
	if index == nil {
		return 0
	}
	if version := cast.ToInt(index["version.created"]); version > 0 {
		return version
	}
	versionMap, _ := index["version"].(map[string]any)
	return cast.ToInt(versionMap["created"])
}
