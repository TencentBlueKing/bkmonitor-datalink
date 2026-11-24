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
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestElasticsearchVersionErrorResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		result string
	}{
		{name: "超时", err: context.DeadlineExceeded, result: "timeout"},
		{name: "取消", err: context.Canceled, result: "canceled"},
		{name: "认证失败", err: &elastic.Error{Status: http.StatusUnauthorized}, result: "unauthorized"},
		{name: "权限不足", err: &elastic.Error{Status: http.StatusForbidden}, result: "forbidden"},
		{name: "服务端错误", err: &elastic.Error{Status: http.StatusServiceUnavailable}, result: "http_error"},
		{name: "网络错误", err: errors.New("connection refused"), result: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.result, elasticsearchVersionErrorResult(fmt.Errorf("version request: %w", tc.err)))
		})
	}
}

func TestInstanceServerVersion(t *testing.T) {
	mock.Init()
	t.Cleanup(mock.Init)
	ctx := context.Background()
	instance, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl, UserName: "test-user", Password: "test-password"},
		Timeout: time.Second,
		Headers: map[string]string{"X-Version-Test": "same-connection"},
	})
	require.NoError(t, err)
	// 同一实例逐次读取响应，集群升级后不继续使用旧版本；错误不能当作旧 ES 成功处理。
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		version string
		errText string
		result  string
	}{
		{name: "旧集群", status: 200, body: `{"version":{"number":"6.8.23"}}`, version: "6.8.23", result: "success"},
		{name: "升级后集群", status: 200, body: `{"version":{"number":"8.19.6"}}`, version: "8.19.6", result: "success"},
		{name: "snapshot", status: 200, body: `{"version":{"number":"9.4.3-SNAPSHOT"}}`, version: "9.4.3-SNAPSHOT", result: "success"},
		{name: "未认证", status: 401, body: `{"error":{"type":"security_exception","reason":"authentication required"},"status":401}`, errText: "get Elasticsearch version", result: "unauthorized"},
		{name: "无根路径权限", status: 403, body: `{"error":{"type":"security_exception","reason":"forbidden"},"status":403}`, errText: "get Elasticsearch version", result: "forbidden"},
		{name: "无效 JSON", status: 200, body: "invalid", errText: "decode Elasticsearch version"},
		{name: "缺少版本", status: 200, body: `{}`, errText: "invalid Elasticsearch version"},
		{name: "空版本", status: 200, body: `{"version":{"number":""}}`, errText: "invalid Elasticsearch version"},
		{name: "非版本字符串", status: 200, body: `{"version":{"number":"unknown"}}`, errText: "invalid Elasticsearch version"},
		{name: "版本缺少 patch", status: 200, body: `{"version":{"number":"7.2"}}`, errText: "invalid Elasticsearch version"},
		{name: "错误字段类型", status: 200, body: `{"version":{"number":8}}`, errText: "decode Elasticsearch version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := setupESTraceRecorder(t)
			result := tc.result
			if result == "" {
				result = "invalid_response"
			}
			labels := map[string]string{"es_major": "unknown", "result": result}
			if tc.version != "" {
				labels["es_major"] = tc.version[:1]
			}
			before := elasticsearchCounterValue(t, "unify_query_elasticsearch_version_requests_total", labels)
			httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/", func(request *http.Request) (*http.Response, error) {
				username, password, ok := request.BasicAuth()
				require.True(t, ok)
				require.Equal(t, "test-user", username)
				require.Equal(t, "test-password", password)
				require.Equal(t, "same-connection", request.Header.Get("X-Version-Test"))
				return httpmock.NewStringResponse(tc.status, tc.body), nil
			})
			version, err := instance.serverVersion(ctx)
			require.Equal(t, before+1, elasticsearchCounterValue(t, "unify_query_elasticsearch_version_requests_total", labels))
			attrs := endedSpanAttrs(t, recorder)
			observedResult, ok := esSpanAttrString(attrs, "es-version-result")
			require.True(t, ok)
			require.Equal(t, result, observedResult)
			observedVersion, ok := esSpanAttrString(attrs, "es-version")
			require.True(t, ok)
			expectedVersion := "unknown"
			if tc.version != "" {
				expectedVersion = tc.version
			}
			require.Equal(t, expectedVersion, observedVersion)
			if tc.errText != "" {
				require.ErrorContains(t, err, tc.errText)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.version, version.String())
		})
	}
}

func elasticsearchCounterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, item := range family.GetMetric() {
			actual := make(map[string]string, len(item.GetLabel()))
			for _, label := range item.GetLabel() {
				actual[label.GetName()] = label.GetValue()
			}
			matches := len(actual) == len(labels)
			for key, value := range labels {
				matches = matches && actual[key] == value
			}
			if matches {
				return item.GetCounter().GetValue()
			}
		}
	}
	return 0
}
