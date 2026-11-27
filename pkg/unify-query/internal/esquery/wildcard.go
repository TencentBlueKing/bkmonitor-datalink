// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esquery

import (
	"maps"
	"slices"
	"strings"
	"unicode"

	elastic "github.com/olivere/elastic/v7"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// Wildcard 按字段的索引规则构造通配符查询，供结构化条件和 Lucene 表达式共用。
func Wildcard(field, value string, option metadata.FieldOption, boost string) elastic.Query {
	return termPattern{field: field, value: value, boost: boost}.query(option)
}

// Regexp 为 Lucene 正则选择与同一字段通配符一致的索引范围和大小写规则。
func Regexp(field, value string, option metadata.FieldOption, boost string) elastic.Query {
	return termPattern{field: field, value: value, boost: boost, isRegexp: true}.query(option)
}

type termPattern struct {
	field, value, boost string
	isRegexp            bool
}

func (p termPattern) query(option metadata.FieldOption) elastic.Query {
	if option.WildcardAnalyzer != "" {
		return p.analyzedQuery(option.WildcardAnalyzer)
	}
	mode := option.WildcardCase
	if mode == metadata.WildcardCaseFromField {
		mode = metadata.WildcardOriginal
		if option.ShouldLowercaseTermPattern() {
			if !option.IsMixedCaseSensitivity {
				mode = metadata.WildcardLowercase
			} else if p.isRegexp {
				// 旧字段元数据没有物理索引名，保留 Lucene 正则原有的双模式。
				mode = metadata.WildcardOriginalAndLowercase
			}
		}
	}
	base := p.forCase(mode, option.WildcardCaseInsensitive)
	base = queryByIndex(base, option.WildcardIndexGroups, func(mode metadata.WildcardCase) elastic.Query {
		return p.forCase(mode, option.WildcardCaseInsensitive)
	})
	return queryByIndex(base, option.WildcardAnalyzerGroups, p.analyzedQuery)
}

func queryByIndex[T ~string](base elastic.Query, groups map[T][]string, build func(T) elastic.Query) elastic.Query {
	if len(groups) == 0 {
		return base
	}

	// 每个索引只执行自己的 wildcard 子句，外层 must_not 也按同样的范围取反。
	remainder := elastic.NewBoolQuery().Must(base)
	query := elastic.NewBoolQuery()
	for _, rule := range slices.Sorted(maps.Keys(groups)) {
		indexes := elastic.NewTermsQueryFromStrings("_index", groups[rule]...)
		remainder.MustNot(indexes)
		query.Should(elastic.NewBoolQuery().Must(build(rule)).Filter(indexes))
	}
	return query.Should(remainder)
}

func (p termPattern) forCase(mode metadata.WildcardCase, caseInsensitive bool) elastic.Query {
	switch mode {
	case metadata.WildcardLowercase:
		// ES 的 case_insensitive 只处理 ASCII，索引的 lowercase 过滤器也处理 Unicode。
		return p.termQuery(strings.ToLower(p.value), caseInsensitive)
	case metadata.WildcardUppercase:
		return p.termQuery(strings.ToUpper(p.value), caseInsensitive)
	case metadata.WildcardOriginalAndLowercase:
		lower := strings.ToLower(p.value)
		folded := p.termQuery(lower, caseInsensitive)
		if lower == p.value {
			return folded
		}
		return elastic.NewBoolQuery().Should(p.termQuery(p.value, false), folded)
	case metadata.WildcardOriginal:
		return p.termQuery(p.value, false)
	case metadata.WildcardSearchNormalization:
		return p.analyzedQuery("")
	case metadata.WildcardFieldNormalization:
		// 仅有 mapping 或旧 ES 时，用字段分析器处理字面片段。ES 6 的 wildcard 会把星号也送进 keyword normalizer。
		if !p.isRegexp && !caseInsensitive {
			return p.analyzedQuery("")
		}
		return p.termQuery(p.value, false)
	default:
		return p.analyzedQuery(string(mode))
	}
}

func (p termPattern) termQuery(value string, caseInsensitive bool) elastic.Query {
	if p.isRegexp {
		query := elastic.NewRegexpQuery(p.field, value)
		if p.boost != "" {
			query.Boost(cast.ToFloat64(p.boost))
		}
		return query
	}
	query := elastic.NewWildcardQuery(p.field, value)
	if caseInsensitive {
		query.CaseInsensitive(true)
	}
	if p.boost != "" {
		query.Boost(cast.ToFloat64(p.boost))
	}
	return query
}

func (p termPattern) analyzedQuery(analyzer string) elastic.Query {
	query := elastic.NewQueryStringQuery(queryStringPattern(p.value, p.isRegexp)).DefaultField(p.field)
	if analyzer != "" {
		query.Analyzer(analyzer)
	}
	if p.boost != "" {
		query.Boost(cast.ToFloat64(p.boost))
	}
	return query
}

// Lucene 正则中的这三个字符在 query_string 的普通通配符中都是字面字符。
var regexpLiterals = strings.NewReplacer(".", "\\.", "@", "\\@", "#", "\\#")

// queryStringPattern 将整个输入限定为单个模式，保留通配符、字面字符和正则分隔符的转义。
func queryStringPattern(value string, isRegexp bool) string {
	var expr strings.Builder
	escaped, wildcard := false, false
	if isRegexp {
		expr.WriteByte('/')
		for _, r := range value {
			if r == '/' && !escaped {
				expr.WriteByte('\\')
			}
			expr.WriteRune(r)
			escaped = r == '\\' && !escaped
		}
		expr.WriteByte('/')
		return expr.String()
	}
	for _, r := range value {
		if !escaped && r == '\\' {
			escaped = true
			continue
		}
		if !escaped && (r == '*' || r == '?') {
			wildcard = true
		} else if unicode.IsSpace(r) || strings.ContainsRune("+-=&|!(){}[]^\"~*?:\\/<>", r) {
			expr.WriteByte('\\')
		}
		expr.WriteRune(r)
		escaped = false
	}
	if escaped {
		expr.WriteString("\\\\")
	}
	if !wildcard {
		// 没有通配符时仍需查询完整词条，避免转成经过分词的普通文本查询。
		return "/" + regexpLiterals.Replace(expr.String()) + "/"
	}
	if value == "*" {
		// query_string 把单个 * 解释为字段存在。** 保留通配符的词条语义。
		return "**"
	}
	return expr.String()
}
