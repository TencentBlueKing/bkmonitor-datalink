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
	"context"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// aliasAggResponse 是按 __ext.labels.app 分桶的聚合响应，该字段配了别名 app。
const aliasAggResponse = `{"aggregations":{"__ext.labels.app":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[` +
	`{"key":"web","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":2.0}}]}},` +
	`{"key":"api","doc_count":1,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":1.0}}]}}` +
	`]}}}`

// multiDimAggResponse 是 app / cluster / signature 三层嵌套分桶的聚合响应，
// 三个字段都配了别名，且 web 下有 c1、c2 两个 cluster，api 下只有 c1。
const multiDimAggResponse = `{"aggregations":{"__ext.labels.app":{"buckets":[` +
	`{"key":"web","doc_count":3,"__ext.cluster":{"buckets":[` +
	`{"key":"c1","doc_count":2,"__dist_05":{"buckets":[` +
	`{"key":"sigA","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":2.0}}]}}]}},` +
	`{"key":"c2","doc_count":1,"__dist_05":{"buckets":[` +
	`{"key":"sigB","doc_count":1,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":1.0}}]}}]}}]}},` +
	`{"key":"api","doc_count":1,"__ext.cluster":{"buckets":[` +
	`{"key":"c1","doc_count":1,"__dist_05":{"buckets":[` +
	`{"key":"sigC","doc_count":1,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":1.0}}]}}]}}]}}` +
	`]}}}`

// newAliasFormatFactory 直接走生产接线 newSeriesFormatFactory，
// 这样别名改写与 aliasFreeEncode 之间的不对称一旦被改动，用例就会红。
func newAliasFormatFactory(ctx context.Context, fieldAlias metadata.FieldAlias, isReference bool) *FormatFactory {
	metadata.GetQueryParams(ctx).SetIsReference(isReference)

	return newSeriesFormatFactory(ctx, &metadata.Query{
		FieldAlias: fieldAlias,
		TimeField: metadata.TimeField{
			Name: DefaultTimeFieldName,
			Type: DefaultTimeFieldType,
			Unit: DefaultTimeFieldUnit,
		},
	}, nil, time.Time{}, time.Time{}, "", 0)
}

func aggLabels(t *testing.T, fact *FormatFactory, dimensions []string) []map[string]string {
	t.Helper()
	return aggLabelsFrom(t, fact, dimensions, aliasAggResponse)
}

func aggLabelsFrom(t *testing.T, fact *FormatFactory, dimensions []string, response string) []map[string]string {
	t.Helper()

	_, _, err := fact.EsAgg(metadata.Aggregates{
		{
			Name:       "max",
			Dimensions: dimensions,
			Window:     time.Hour * 24,
		},
	})
	assert.NoError(t, err)

	var sr *elastic.SearchResult
	assert.NoError(t, json.Unmarshal([]byte(response), &sr))

	qr, err := fact.AggDataFormat(sr.Aggregations, nil)
	assert.NoError(t, err)

	labels := make([]map[string]string, 0, len(qr.Timeseries))
	for _, ts := range qr.Timeseries {
		lb := make(map[string]string, len(ts.Labels))
		for _, l := range ts.Labels {
			lb[l.Name] = l.Value
		}
		labels = append(labels, lb)
	}
	return labels
}

// TestAggDataFormat_AliasFreeDimensionKey 覆盖配了别名的字段按原始字段名分组的场景。
// PromQL 的 by 子句只做格式转换、不认别名，聚合结果若只留别名键，按原始字段名分组会匹配不上而丢维度。
func TestAggDataFormat_AliasFreeDimensionKey(t *testing.T) {
	metadata.InitMetadata()

	fieldAlias := metadata.FieldAlias{"app": "__ext.labels.app"}
	// PromQL 语句里的维度名只经过格式转换，这里对齐同一套规则。
	promQLKey := metadata.GetFieldFormat(context.Background()).EncodeFunc()("__ext.labels.app")

	t.Run("group by original field keeps both keys", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		labels := aggLabels(t, newAliasFormatFactory(ctx, fieldAlias, false), []string{"__ext.labels.app"})

		assert.Len(t, labels, 2)
		for _, lb := range labels {
			assert.Len(t, lb, 2)
			assert.Equal(t, lb["app"], lb[promQLKey], "两个维度键必须指向同一个值")
			assert.Contains(t, []string{"web", "api"}, lb["app"])
		}
	})

	// 用别名分组本来就能匹配上，不该多补一个键，避免无谓地改变既有响应。
	for name, c := range map[string]struct {
		fieldAlias  metadata.FieldAlias
		dimensions  []string
		isReference bool
	}{
		"group by alias":           {fieldAlias: fieldAlias, dimensions: []string{"app"}},
		"reference query":          {fieldAlias: fieldAlias, dimensions: []string{"__ext.labels.app"}, isReference: true},
		"field without alias":      {dimensions: []string{"__ext.labels.app"}},
		"reference group by alias": {fieldAlias: fieldAlias, dimensions: []string{"app"}, isReference: true},
	} {
		t.Run(name+" keeps single key", func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			labels := aggLabels(t, newAliasFormatFactory(ctx, c.fieldAlias, c.isReference), c.dimensions)

			assert.Len(t, labels, 2)
			for _, lb := range labels {
				assert.Len(t, lb, 1)
			}
		})
	}
}

// TestAggDataFormat_AliasFreeKeyIsPromQLCompatible 固定补出来的键与 PromQL 侧的口径一致，
// 避免后续改动只补了原始字段名——那样带点的字段在 PromQL 里依然匹配不上。
func TestAggDataFormat_AliasFreeKeyIsPromQLCompatible(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	labels := aggLabels(t, newAliasFormatFactory(ctx, metadata.FieldAlias{"app": "__ext.labels.app"}, false), []string{"__ext.labels.app"})
	assert.NotEmpty(t, labels)

	for _, lb := range labels {
		assert.NotContains(t, lb, "__ext.labels.app", "带点的键在 PromQL 里不合法，必须是格式转换后的形式")
		assert.Contains(t, lb, "__ext__bk_46__labels__bk_46__app")
		assert.Contains(t, lb, "app")
	}
}

// TestAggDataFormat_AliasFreeDimensionKeyWithNestedBuckets 复刻线上聚类告警的形态：
// signature 用别名请求、另外两个用原始字段名请求，且分桶不对称（web 有两个 cluster，api 只有一个）。
// 同层的多个桶共用一份 labels，这里验证嵌套桶下补键跟对了各自的值，没有残留上一个桶的。
func TestAggDataFormat_AliasFreeDimensionKeyWithNestedBuckets(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	fieldAlias := metadata.FieldAlias{
		"signature": "__dist_05",
		"cluster":   "__ext.cluster",
		"app":       "__ext.labels.app",
	}
	labels := aggLabelsFrom(t, newAliasFormatFactory(ctx, fieldAlias, false),
		[]string{"signature", "__ext.cluster", "__ext.labels.app"}, multiDimAggResponse)

	assert.Len(t, labels, 3)
	for _, lb := range labels {
		assert.Equal(t, lb["app"], lb["__ext__bk_46__labels__bk_46__app"], "app 补键必须与别名键同值")
		assert.Equal(t, lb["cluster"], lb["__ext__bk_46__cluster"], "cluster 补键必须与别名键同值")
		// signature 是用别名请求的，本来就匹配得上，不该多补键
		assert.NotEmpty(t, lb["signature"])
		assert.NotContains(t, lb, "__dist_05")
	}

	got := make(map[string][2]string, len(labels))
	for _, lb := range labels {
		got[lb["signature"]] = [2]string{lb["__ext__bk_46__labels__bk_46__app"], lb["__ext__bk_46__cluster"]}
	}
	assert.Equal(t, [2]string{"web", "c1"}, got["sigA"])
	assert.Equal(t, [2]string{"web", "c2"}, got["sigB"])
	assert.Equal(t, [2]string{"api", "c1"}, got["sigC"])
}
