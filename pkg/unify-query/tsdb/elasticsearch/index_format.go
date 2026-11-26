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
	analyzer   map[string]any
	tokenizer  map[string]any
	filter     map[string]any
	normalizer map[string]any
	fieldsMap  metadata.FieldsMap

	fieldAlias              metadata.FieldAlias
	wildcardCaseInsensitive *bool
	hasSettings             bool
	parsedIndexes           int
	analyzerIndexCounts     map[string]int
}

func NewIndexOptionFormat(fieldAlias map[string]string) *IndexOptionFormat {
	return &IndexOptionFormat{
		fieldsMap:           make(metadata.FieldsMap),
		fieldAlias:          fieldAlias,
		analyzerIndexCounts: make(map[string]int),
	}
}

func (f *IndexOptionFormat) FieldsMap() metadata.FieldsMap {
	if f.wildcardCaseInsensitive != nil {
		for k, v := range f.fieldsMap {
			// 多索引解析时低版本索引可能后出现，需要在返回前统一回填最终能力值。
			v.WildcardCaseInsensitive = *f.wildcardCaseInsensitive
			if f.analyzerIndexCounts[k] != f.parsedIndexes {
				v.WildcardAnalyzer = ""
			}
			f.fieldsMap[k] = v
		}
	}
	return f.fieldsMap
}

func (f *IndexOptionFormat) Parse(settings, mappings map[string]any) {
	f.ParseIndex("", settings, mappings)
}

// ParseIndex 合并物理索引的字段配置，并返回该索引可用于折叠查询的字段名。
func (f *IndexOptionFormat) ParseIndex(index string, settings, mappings map[string]any) map[string]bool {
	f.parsedIndexes++
	f.updateWildcardCaseInsensitive(settings)
	f.parseAnalysis(settings)
	fields := mappingFields(mappings)
	names := make(map[string]bool, len(fields))
	for name, field := range fields {
		names[name] = true
		mapping := field.definition
		if mapping[FormatPropertiesType] == "alias" {
			// ES 字段别名引用同一物理索引内的完整路径，也可以指向多字段。
			path, _ := mapping["path"].(string)
			mapping = fields[path].definition
		}
		option := f.esToFieldMap(name, mapping)
		if !option.Existed() {
			continue
		}
		option.SearchOnly = field.searchOnly
		if option.WildcardAnalyzer != "" {
			f.analyzerIndexCounts[name]++
		}
		if existing, ok := f.fieldsMap[name]; ok {
			option = mergeFieldOption(existing, option, index)
		}
		f.fieldsMap[name] = option
	}
	return names
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

	// IndexGet 成功但没有 analysis 时可使用内置默认值。仅有 _mapping 时无法判断索引是否覆盖了默认分析器。
	f.hasSettings = settings != nil
	f.tokenizer, _ = analysis["tokenizer"].(map[string]any)
	f.analyzer, _ = analysis["analyzer"].(map[string]any)
	f.filter, _ = analysis["filter"].(map[string]any)
	f.normalizer, _ = analysis["normalizer"].(map[string]any)
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
	fieldMap.WildcardCase = metadata.WildcardOriginal
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

	// keyword 可以用原始模式调用 ES 自身的 normalizer。text 的模式依据索引分析器写入的词条。
	switch fieldMap.FieldType {
	case KeyWord:
		normalizer := cast.ToString(data[FormatPropertiesNormalizer])
		fieldMap.IsCaseSensitive = !f.normalizerLowercases(normalizer)
		fieldMap.IsCaseInsensitive = !fieldMap.IsCaseSensitive
		if normalizer != "" {
			fieldMap.WildcardCase = metadata.WildcardFieldNormalization
		}
	case Text:
		indexAnalyzer := cast.ToString(data[FormatPropertiesAnalyzer])
		if indexAnalyzer == "" {
			indexAnalyzer = "default"
		}

		analyzer, _ := f.analyzer[indexAnalyzer].(map[string]any)
		tokenizerName := cast.ToString(analyzer["tokenizer"])
		tokenizer, _ := f.tokenizer[tokenizerName].(map[string]any)
		chars, ok := tokenizer[AnalyzerKeyTokenizeOnChars]
		if !ok {
			chars = analyzer[AnalyzerKeyTokenizeOnChars]
		}
		if toc := cast.ToStringSlice(chars); len(toc) > 0 {
			fieldMap.TokenizeOnChars = toc
		}

		if tokenizer != nil {
			tokenizerName = cast.ToString(tokenizer[AnalyzerKeyType])
		}
		if f.hasSettings {
			fieldMap.WildcardCase = f.analyzerWildcardCase(indexAnalyzer, analyzer, tokenizerName)
		} else if _, builtin := builtinAnalyzerCase(indexAnalyzer); builtin {
			// 内置名称在各个索引均可用，ES 会采用该索引对同名分析器的配置，包括 default。
			fieldMap.WildcardCase = metadata.WildcardCase(indexAnalyzer)
		} else {
			fieldMap.WildcardAnalyzer = indexAnalyzer
			if searchAnalyzer, _ := data["search_analyzer"].(string); searchAnalyzer == "" || searchAnalyzer == indexAnalyzer {
				// 自定义分析器仅在个别索引出现时，使用该字段自己的搜索分析器归一化通配符。
				fieldMap.WildcardCase = metadata.WildcardFieldNormalization
			}
		}
		switch fieldMap.WildcardCase {
		case metadata.WildcardOriginal, metadata.WildcardFieldNormalization, "whitespace", "keyword":
			fieldMap.IsCaseSensitive = true
		}
	case "wildcard":
		fieldMap.IsCaseSensitive = true
	}
	if f.wildcardCaseInsensitive != nil {
		fieldMap.WildcardCaseInsensitive = *f.wildcardCaseInsensitive
	}

	return fieldMap
}

func mergeFieldOption(existing, next metadata.FieldOption, index string) metadata.FieldOption {
	if existing.WildcardAnalyzer != next.WildcardAnalyzer {
		existing.WildcardAnalyzer = ""
	}
	existing.SearchOnly = existing.SearchOnly && next.SearchOnly
	existing.HasMixedTypes = existing.HasMixedTypes || next.HasMixedTypes || existing.FieldType != next.FieldType
	existingAffectsWildcard := fieldCaseSensitivityAffectsWildcard(existing)
	nextAffectsWildcard := fieldCaseSensitivityAffectsWildcard(next)
	if !existingAffectsWildcard || !nextAffectsWildcard {
		return existing
	}

	merged := existing
	merged.IsCaseSensitive = existing.IsCaseSensitive && next.IsCaseSensitive
	// 保留任一 text mapping 的 analyzed 状态，否则 keyword 先出现时会丢掉后续 text 索引的 lower 覆盖。
	merged.IsAnalyzed = existing.IsAnalyzed || next.IsAnalyzed
	merged.IsCaseInsensitive = existing.IsCaseInsensitive || next.IsCaseInsensitive
	merged.IsMixedCaseSensitivity = existing.IsMixedCaseSensitivity || next.IsMixedCaseSensitivity ||
		existing.IsCaseSensitive != next.IsCaseSensitive
	if len(merged.TokenizeOnChars) == 0 && len(next.TokenizeOnChars) > 0 {
		merged.TokenizeOnChars = next.TokenizeOnChars
	}
	if merged.WildcardCase == next.WildcardCase {
		return merged
	}
	if index == "" {
		// 缺少物理索引名时将原始模式交给 ES。
		merged.WildcardCase = metadata.WildcardOriginal
		return merged
	}
	if merged.WildcardIndexGroups == nil {
		merged.WildcardIndexGroups = make(map[metadata.WildcardCase][]string)
	}
	indexes := merged.WildcardIndexGroups[next.WildcardCase]
	// 旧版 ES 的多个 mapping type 可以来自同一物理索引。
	if len(indexes) == 0 || indexes[len(indexes)-1] != index {
		merged.WildcardIndexGroups[next.WildcardCase] = append(indexes, index)
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

// normalizerLowercases 识别 keyword 的大小写标记，查询始终由 ES 执行完整的 normalizer。
func (f *IndexOptionFormat) normalizerLowercases(name string) bool {
	normalizer, _ := f.normalizer[name].(map[string]any)
	if normalizer == nil {
		return name == AnalyzerFilterLowercase || name == AnalyzerFilterICUFolding
	}
	// 字符替换可能依赖原始大小写，由 ES 对 keyword 使用完整的 normalizer。
	if len(cast.ToStringSlice(normalizer["char_filter"])) > 0 {
		return false
	}
	return f.filterWildcardCase(cast.ToStringSlice(normalizer[AnalyzerKeyFilter]), false) == metadata.WildcardLowercase
}

// analyzerWildcardCase 根据索引分析器实际写入的词条选择通配符模式。
func (f *IndexOptionFormat) analyzerWildcardCase(name string, analyzer map[string]any, tokenizerType string) metadata.WildcardCase {
	if analyzer == nil {
		mode, _ := builtinAnalyzerCase(name)
		return mode
	}
	analyzerType := cast.ToString(analyzer[AnalyzerKeyType])
	if strings.EqualFold(analyzerType, "pattern") {
		// pattern analyzer 有 lowercase 开关，显式 false 时必须保留大小写。
		if lowercase, ok := analyzer["lowercase"]; ok && !cast.ToBool(lowercase) {
			return metadata.WildcardOriginal
		}
		return metadata.WildcardLowercase
	}
	if analyzerType != "" && analyzerType != "custom" {
		// 自定义名称可包装内置 analyzer，例如 {"type":"standard"}，需按 type 判定。
		mode, _ := builtinAnalyzerCase(strings.ToLower(analyzerType))
		return mode
	}

	return f.filterWildcardCase(cast.ToStringSlice(analyzer[AnalyzerKeyFilter]), tokenizerType == AnalyzerFilterLowercase)
}

func builtinAnalyzerCase(name string) (metadata.WildcardCase, bool) {
	switch name {
	case "default", "standard", "simple", "stop", "pattern", "fingerprint",
		"arabic", "armenian", "basque", "bengali", "brazilian", "bulgarian",
		"catalan", "cjk", "czech", "danish", "dutch", "english", "estonian",
		"finnish", "french", "galician", "german", "hindi",
		"hungarian", "indonesian", "italian", "latvian",
		"lithuanian", "norwegian", "persian", "portuguese", "romanian",
		"russian", "serbian", "sorani", "spanish", "swedish", "thai":
		return metadata.WildcardLowercase, true
	case "keyword", "whitespace":
		return metadata.WildcardOriginal, true
	case "turkish":
		return metadata.WildcardTurkish, true
	case "greek":
		return metadata.WildcardGreek, true
	case "irish":
		return metadata.WildcardIrish, true
	}
	return metadata.WildcardOriginal, false
}

// filterWildcardCase 从分词器输出开始，按索引配置的顺序判断最终词条。
func (f *IndexOptionFormat) filterWildcardCase(filters []string, lowercaseTokenizer bool) metadata.WildcardCase {
	mode := metadata.WildcardOriginal
	if lowercaseTokenizer {
		mode = metadata.WildcardLowercase
	}
	for _, name := range filters {
		filter, _ := f.filter[name].(map[string]any)
		if filter != nil {
			name = cast.ToString(filter[AnalyzerKeyType])
		}
		switch name {
		case AnalyzerFilterLowercase:
			if language := cast.ToString(filter["language"]); language != "" {
				// ES 的语言过滤器使用对应语言的多词项归一化规则。
				mode, _ = builtinAnalyzerCase(strings.ToLower(language))
			} else if mode != metadata.WildcardTurkish && mode != metadata.WildcardGreek && mode != metadata.WildcardIrish {
				mode = metadata.WildcardLowercase
			}
		case AnalyzerFilterICUFolding:
			mode = metadata.WildcardLowercase
		case "uppercase":
			mode = metadata.WildcardUppercase
		case "stop", "length", "trim", "unique", "asciifolding", "stemmer":
			// 沿用分词器或前一个过滤器确定的大小写。
		default:
			// 例如 pattern_replace 能将已转小写的词条替换成大写文本。
			if mode == metadata.WildcardLowercase {
				mode = metadata.WildcardOriginalAndLowercase
			} else if mode == metadata.WildcardUppercase {
				mode = metadata.WildcardOriginal
			}
		}
	}
	return mode
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
