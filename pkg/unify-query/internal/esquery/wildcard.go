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
	"strings"

	elastic "github.com/olivere/elastic/v7"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// Wildcard 按字段的索引规则构造通配符查询，供结构化条件和 Lucene 表达式共用。
func Wildcard(field, value string, option metadata.FieldOption, boost string) elastic.Query {
	if !option.ShouldLowercaseTermPattern() {
		return boostedWildcardQuery(field, value, false, boost)
	}

	// ES 的 case_insensitive 只处理 ASCII，索引的 lowercase 过滤器也处理 Unicode。
	lowerValue := strings.ToLower(value)
	if option.IsMixedCaseSensitivity {
		// 同名字段在不同索引中分别保留原值或写入小写词条。
		original := boostedWildcardQuery(field, value, false, boost)
		if lowerValue == value {
			return original
		}
		return elastic.NewBoolQuery().Should(original, boostedWildcardQuery(field, lowerValue, false, boost))
	}

	return boostedWildcardQuery(field, lowerValue, option.WildcardCaseInsensitive, boost)
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
