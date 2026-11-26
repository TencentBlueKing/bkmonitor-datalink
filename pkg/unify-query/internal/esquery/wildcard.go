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

	elastic "github.com/olivere/elastic/v7"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// Wildcard 按字段的索引规则构造通配符查询，供结构化条件和 Lucene 表达式共用。
func Wildcard(field, value string, option metadata.FieldOption, boost string) elastic.Query {
	mode := option.WildcardCase
	if mode == metadata.WildcardCaseFromField {
		mode = metadata.WildcardOriginal
		if !option.IsMixedCaseSensitivity && option.ShouldLowercaseTermPattern() {
			mode = metadata.WildcardLowercase
		}
	}
	base := wildcardForCase(field, value, mode, option.WildcardCaseInsensitive, boost)
	if len(option.WildcardIndexGroups) == 0 {
		return base
	}

	// 每个索引只执行自己的 wildcard 子句，外层 must_not 也按同样的范围取反。
	remainder := elastic.NewBoolQuery().Must(base)
	query := elastic.NewBoolQuery()
	for _, otherMode := range slices.Sorted(maps.Keys(option.WildcardIndexGroups)) {
		indexes := elastic.NewTermsQueryFromStrings("_index", option.WildcardIndexGroups[otherMode]...)
		remainder.MustNot(indexes)
		pattern := wildcardForCase(field, value, otherMode, option.WildcardCaseInsensitive, boost)
		query.Should(elastic.NewBoolQuery().Must(pattern).Filter(indexes))
	}
	return query.Should(remainder)
}

func wildcardForCase(field, value string, mode metadata.WildcardCase, caseInsensitive bool, boost string) elastic.Query {
	switch mode {
	case metadata.WildcardLowercase:
		// ES 的 case_insensitive 只处理 ASCII，索引的 lowercase 过滤器也处理 Unicode。
		return boostedWildcardQuery(field, strings.ToLower(value), caseInsensitive, boost)
	case metadata.WildcardUppercase:
		return boostedWildcardQuery(field, strings.ToUpper(value), caseInsensitive, boost)
	case metadata.WildcardOriginalAndLowercase:
		lower := strings.ToLower(value)
		folded := boostedWildcardQuery(field, lower, caseInsensitive, boost)
		if lower == value {
			return folded
		}
		return elastic.NewBoolQuery().Should(boostedWildcardQuery(field, value, false, boost), folded)
	default:
		return boostedWildcardQuery(field, value, false, boost)
	}
}

func boostedWildcardQuery(field, value string, caseInsensitive bool, boost string) *elastic.WildcardQuery {
	query := elastic.NewWildcardQuery(field, value)
	if caseInsensitive {
		query.CaseInsensitive(true)
	}
	if boost != "" {
		query.Boost(cast.ToFloat64(boost))
	}
	return query
}
