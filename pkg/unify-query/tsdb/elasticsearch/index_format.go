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
	analyzer   map[string]any
	tokenizer  map[string]any
	filter     map[string]any
	normalizer map[string]any
	fieldsMap  metadata.FieldsMap

	fieldAlias              metadata.FieldAlias
	wildcardCaseInsensitive *bool
	hasAnalysisSettings     bool
}

func NewIndexOptionFormat(fieldAlias map[string]string) *IndexOptionFormat {
	return &IndexOptionFormat{
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
	f.ParseIndex("", settings, mappings)
}

// ParseIndex 记录物理索引名，使混合映射的通配符可以按索引选择模式。
func (f *IndexOptionFormat) ParseIndex(index string, settings, mappings map[string]any) {
	f.updateWildcardCaseInsensitive(settings)
	f.parseAnalysis(settings)
	f.parseMappings(index, mappings)
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

	f.hasAnalysisSettings = analysis != nil
	f.tokenizer, _ = analysis["tokenizer"].(map[string]any)
	f.analyzer, _ = analysis["analyzer"].(map[string]any)
	f.filter, _ = analysis["filter"].(map[string]any)
	f.normalizer, _ = analysis["normalizer"].(map[string]any)
}

func (f *IndexOptionFormat) parseMappings(index string, mappings map[string]any) {
	if _, ok := mappings[FormatProperties]; ok {
		f.mapMappings(index, "", mappings)
	} else {
		// 有的 es 因为版本不同，properties 不在第一层，所以需要往下一层找
		for _, m := range mappings {
			switch nm := m.(type) {
			case map[string]any:
				f.parseMappings(index, nm)
			}
		}
	}
}

func (f *IndexOptionFormat) mapMappings(index, prefix string, data map[string]any) {
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
				f.mapMappings(index, k, nv)
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
			f.fieldsMap[prefix] = mergeFieldOption(existing, fm, index)
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
		fieldMap.IsCaseSensitive = !f.normalizerLowercases(cast.ToString(data[FormatPropertiesNormalizer]))
		fieldMap.IsCaseInsensitive = !fieldMap.IsCaseSensitive
		if fieldMap.IsCaseInsensitive {
			fieldMap.WildcardCase = metadata.WildcardLowercase
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
		fieldMap.WildcardCase = f.analyzerWildcardCase(indexAnalyzer, analyzer, tokenizerName)
		fieldMap.IsCaseSensitive = fieldMap.WildcardCase == metadata.WildcardOriginal || fieldMap.WildcardCase == metadata.WildcardUppercase
	case "wildcard":
		fieldMap.IsCaseSensitive = true
	}
	if f.wildcardCaseInsensitive != nil {
		fieldMap.WildcardCaseInsensitive = *f.wildcardCaseInsensitive
	}

	return fieldMap
}

func mergeFieldOption(existing, next metadata.FieldOption, index string) metadata.FieldOption {
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

// normalizerLowercases 判断 keyword 的模式能否提前使用通用小写转换。
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
		return builtinAnalyzerCase(name, !f.hasAnalysisSettings)
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
		return builtinAnalyzerCase(analyzerType, false)
	}

	return f.filterWildcardCase(cast.ToStringSlice(analyzer[AnalyzerKeyFilter]), tokenizerType == AnalyzerFilterLowercase)
}

func builtinAnalyzerCase(name string, assumeLowercase bool) metadata.WildcardCase {
	switch strings.ToLower(name) {
	case "default", "standard", "simple", "stop", "pattern", "fingerprint",
		"arabic", "armenian", "basque", "bengali", "brazilian", "bulgarian",
		"catalan", "cjk", "czech", "danish", "dutch", "english", "estonian",
		"finnish", "french", "galician", "german", "hindi",
		"hungarian", "indonesian", "italian", "latvian",
		"lithuanian", "norwegian", "persian", "portuguese", "romanian",
		"russian", "serbian", "sorani", "spanish", "swedish", "thai":
		return metadata.WildcardLowercase
	case "keyword", "whitespace", "greek", "irish", "turkish":
		return metadata.WildcardOriginal
	}
	if assumeLowercase {
		return metadata.WildcardLowercase
	}
	return metadata.WildcardOriginal
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
			// 提前用 Go 转小写会丢失语言规则需要的原字母，例如土耳其语的 I。
			if cast.ToString(filter["language"]) != "" {
				return metadata.WildcardOriginal
			}
			mode = metadata.WildcardLowercase
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
