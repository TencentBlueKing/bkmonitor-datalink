// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestWildcardCustomAnalyzerIntegration(t *testing.T) {
	address := os.Getenv("BKMONITOR_TEST_ES_URL")
	if address == "" {
		t.Skip("设置 BKMONITOR_TEST_ES_URL 后运行自定义分析器查询测试")
	}
	ctx, cancel := context.WithTimeout(metadata.InitHashID(context.Background()), time.Minute)
	defer cancel()
	instance, err := NewInstance(ctx, &InstanceOption{Connect: Connect{
		Address: address, UserName: os.Getenv("BKMONITOR_TEST_ES_USERNAME"), Password: os.Getenv("BKMONITOR_TEST_ES_PASSWORD"),
	}, Timeout: 10 * time.Second})
	require.NoError(t, err)
	version, err := instance.serverVersion(ctx)
	require.NoError(t, err)
	client, err := instance.getClient(ctx, instance.connect)
	require.NoError(t, err)
	t.Cleanup(client.Stop)

	// tr_log 将大写 I 先转为土耳其语 ı，再折叠为 i。search_analyzer 保留原文。
	mapping := map[string]any{"properties": map[string]any{
		"message": map[string]any{"type": "text", "analyzer": "tr_log", "search_analyzer": "keyword"},
	}}
	if version.Major < 7 {
		mapping = map[string]any{"_doc": mapping}
	}
	index := fmt.Sprintf("uq_wildcard_analyzer_%d", time.Now().UnixNano())
	_, err = client.CreateIndex(index).BodyJson(map[string]any{
		"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0, "analysis": map[string]any{
			"filter":   map[string]any{"tr_lower": map[string]any{"type": "lowercase", "language": "turkish"}},
			"analyzer": map[string]any{"tr_log": map[string]any{"type": "custom", "tokenizer": "keyword", "filter": []string{"tr_lower", "asciifolding"}}},
		}},
		"mappings": mapping,
	}).Do(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, err := client.DeleteIndex(index).Do(cleanupCtx)
		require.NoError(t, err)
	})
	for _, city := range []string{"ISPARTA", "IZMIR"} {
		_, err = client.Index().Index(index).Id(city).BodyJson(map[string]string{"message": city}).Do(ctx)
		require.NoError(t, err)
	}
	_, err = client.Refresh(index).Do(ctx)
	require.NoError(t, err)
	fields, err := instance.fieldMap(ctx, nil, index)
	require.NoError(t, err)
	factory := NewFormatFactory(ctx).WithFieldMap(fields)
	for _, tc := range []struct{ name, operator, expression, want string }{
		{"结构化包含", metadata.ConditionContains, "*ISP*", "ISPARTA"},
		{"结构化排除", metadata.ConditionNotContains, "*ISP*", "IZMIR"},
		{"Lucene 通配符", "", "message: *ISP*", "ISPARTA"},
		{"Lucene 正则", "", "message: /.*ISP.*/", "ISPARTA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q elastic.Query
			if tc.operator == "" {
				q = factory.ParserQueryString(ctx, tc.expression, false)
			} else {
				var err error
				q, err = factory.Query(metadata.AllConditions{{{DimensionName: "message", Operator: tc.operator, IsWildcard: true, Value: []string{tc.expression}}}})
				require.NoError(t, err)
			}
			result, err := client.Search(index).Query(q).Do(ctx)
			require.NoError(t, err)
			require.Zero(t, result.Shards.Failed)
			require.Len(t, result.Hits.Hits, 1)
			require.Equal(t, tc.want, result.Hits.Hits[0].Id)
		})
	}
}
