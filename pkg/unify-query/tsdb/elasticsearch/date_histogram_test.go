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
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/blang/semver"
	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestUnsupportedExplicitDateHistogramInterval(t *testing.T) {
	const reason = "[date_histogram] unknown field [fixed_interval], parser not found"
	for _, tc := range []struct {
		name   string
		status int
		detail *elastic.ErrorDetails
		want   bool
	}{
		{name: "旧版本未知间隔字段", status: http.StatusBadRequest, detail: &elastic.ErrorDetails{Type: "illegal_argument_exception", Reason: reason}, want: true},
		{name: "ES6 未知间隔字段", status: http.StatusBadRequest, detail: &elastic.ErrorDetails{Type: "x_content_parse_exception", Reason: "[1:67] " + reason}, want: true},
		{name: "根原因包含未知间隔字段", status: http.StatusBadRequest, detail: &elastic.ErrorDetails{Type: "parsing_exception", RootCause: []*elastic.ErrorDetails{{Type: "illegal_argument_exception", Reason: reason}}}, want: true},
		{name: "权限错误保持失败", status: http.StatusForbidden, detail: &elastic.ErrorDetails{Type: "illegal_argument_exception", Reason: reason}},
		{name: "缺少错误详情", status: http.StatusBadRequest},
		{name: "其他错误类型", status: http.StatusBadRequest, detail: &elastic.ErrorDetails{Type: "security_exception", Reason: reason}},
		{name: "其他聚合未知字段", status: http.StatusBadRequest, detail: &elastic.ErrorDetails{Type: "illegal_argument_exception", Reason: "[histogram] unknown field [fixed_interval]"}},
		{name: "其他未知字段", status: http.StatusBadRequest, detail: &elastic.ErrorDetails{Type: "illegal_argument_exception", Reason: "[date_histogram] unknown field [invalid_interval]"}},
		{name: "间隔值非法", status: http.StatusBadRequest, detail: &elastic.ErrorDetails{Type: "illegal_argument_exception", Reason: "[date_histogram] invalid fixed_interval"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 只把确定的旧版本语法错误转换为能力结果，其余服务端错误继续返回。
			err := fmt.Errorf("query failed: %w", &elastic.Error{Status: tc.status, Details: tc.detail})
			require.Equal(t, tc.want, unsupportedExplicitDateHistogramInterval(err))
		})
	}
}

func TestSupportsExplicitDateHistogramInterval(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{version: "5.4.0"},
		{version: "6.8.23"},
		{version: "7.0.1"},
		{version: "7.1.1"},
		{version: "7.2.0", want: true},
		{version: "7.17.29", want: true},
		{version: "8.0.0", want: true},
		{version: "9.4.3", want: true},
		{version: "9.4.3-SNAPSHOT", want: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			version, err := semver.Parse(tc.version)
			require.NoError(t, err)
			require.Equal(t, tc.want, supportsExplicitDateHistogramInterval(version))
		})
	}
}

func TestHasDateHistogram(t *testing.T) {
	for _, name := range []string{DateHistogram, Max, Min, Avg, Sum, Count, Cardinality, Percentiles, "unsupported"} {
		for _, tc := range []struct {
			name    string
			window  time.Duration
			without bool
		}{
			{name: "时间聚合", window: time.Minute},
			{name: "无窗口"},
			{name: "without 保留窗口", window: time.Minute, without: true},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				aggregates := metadata.Aggregates{{Name: name, Window: tc.window, Without: tc.without}}
				// 直接检查 EsAgg 生成的聚合，保证版本请求条件与实际 DSL 一致。
				factory := NewFormatFactory(context.Background()).WithQuery("value",
					metadata.TimeField{Name: "time", Type: TimeFieldTypeTime, Unit: function.Millisecond},
					time.Unix(0, 0), time.Unix(60, 0), function.Millisecond, 0)
				_, agg, err := factory.EsAgg(aggregates)
				if name == "unsupported" {
					require.Error(t, err)
					require.False(t, hasDateHistogram(aggregates))
					return
				}
				require.NoError(t, err)
				source, err := agg.Source()
				require.NoError(t, err)
				body, err := json.Marshal(source)
				require.NoError(t, err)
				var decoded map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(body, &decoded))
				_, containsHistogram := decoded[DateHistogram]
				require.Equal(t, containsHistogram, hasDateHistogram(aggregates))
			})
		}
	}
	for _, tc := range []struct {
		name       string
		aggregates metadata.Aggregates
		want       bool
	}{
		{name: "空聚合列表"},
		{name: "后续聚合包含时间窗口", aggregates: metadata.Aggregates{{Name: Count, Without: true}, {Name: Sum, Window: time.Minute}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, hasDateHistogram(tc.aggregates))
		})
	}
}

func TestConfigureDateHistogramWithoutVersionRequest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceType string
		aggregates metadata.Aggregates
	}{
		{name: "原始查询"},
		{name: "无时间聚合", aggregates: metadata.Aggregates{{Name: Count, Window: time.Minute, Without: true}}},
		{name: "BKData 时间聚合", sourceType: structured.BkData, aggregates: metadata.Aggregates{{Name: DateHistogram, Window: time.Minute}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 无需版本的路径必须能在没有 ES 连接的情况下完成准备。
			ctx := context.Background()
			instance, err := NewInstance(ctx, &InstanceOption{Timeout: time.Second})
			require.NoError(t, err)
			factory := NewFormatFactory(ctx)
			require.NoError(t, instance.configureDateHistogram(ctx, &metadata.Query{SourceType: tc.sourceType, Aggregates: tc.aggregates}, factory, nil))
			require.False(t, factory.explicitDateHistogramInterval)
		})
	}
}

func TestFormatFactory_DateHistogramInterval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       string
		unit       string
		window     time.Duration
		key        string
		interval   string
		offset     int64
		wantOffset string
	}{
		{name: "日历秒", kind: Date, unit: function.Millisecond, window: time.Second, key: "calendar_interval", interval: "1s"},
		{name: "日历分钟", kind: Date, unit: function.Millisecond, window: time.Minute, key: "calendar_interval", interval: "1m"},
		{name: "日历小时", kind: Date, unit: function.Millisecond, window: time.Hour, key: "calendar_interval", interval: "1h"},
		{name: "日历日", kind: Date, unit: function.Millisecond, window: 24 * time.Hour, key: "calendar_interval", interval: "1d"},
		{name: "固定多日", kind: Date, unit: function.Millisecond, window: 48 * time.Hour, key: "fixed_interval", interval: "2d"},
		{name: "固定七日", kind: Date, unit: function.Millisecond, window: 7 * 24 * time.Hour, key: "fixed_interval", interval: "7d"},
		{name: "固定混合分钟", kind: Date, unit: function.Millisecond, window: 62 * time.Minute, key: "fixed_interval", interval: "62m"},
		{name: "固定混合秒", kind: Date, unit: function.Millisecond, window: time.Hour + 12*time.Second, key: "fixed_interval", interval: "3612s"},
		{name: "固定毫秒", kind: Date, unit: function.Millisecond, window: 10 * time.Millisecond, key: "fixed_interval", interval: "10ms"},
		{name: "long 秒", kind: Long, unit: function.Second, window: time.Minute, key: "fixed_interval", interval: "60ms"},
		{name: "long 毫秒", kind: Long, unit: function.Millisecond, window: time.Minute, key: "calendar_interval", interval: "1m"},
		{name: "long 微秒", kind: Long, unit: function.Microsecond, window: 3600 * time.Millisecond, key: "calendar_interval", interval: "1h"},
		{name: "long 纳秒", kind: Long, unit: function.Nanosecond, window: time.Millisecond, key: "fixed_interval", interval: "1000s"},
		{name: "long 正 offset", kind: Long, unit: function.Second, window: time.Hour, key: "fixed_interval", interval: "3600ms", offset: 8 * 3600000, wantOffset: "+28800ms"},
		{name: "long 负 offset", kind: Long, unit: function.Millisecond, window: 24 * time.Hour, key: "calendar_interval", interval: "1d", offset: -5 * 3600000, wantOffset: "-5h"},
	} {
		for _, mode := range []struct {
			name     string
			explicit bool
		}{
			{name: "legacy"},
			{name: "explicit", explicit: true},
		} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				start, end := time.Unix(0, 0), time.Unix(10, 0)
				factory := NewFormatFactory(context.Background()).WithExplicitDateHistogramInterval(mode.explicit).
					WithQuery("value", metadata.TimeField{Name: "time", Type: tc.kind, Unit: tc.unit}, start, end, function.Millisecond, 0)
				_, agg, err := factory.EsAgg(metadata.Aggregates{{Name: Sum, Window: tc.window, TimeZone: "America/New_York", TimeZoneOffset: tc.offset}})
				require.NoError(t, err)
				source, err := agg.Source()
				require.NoError(t, err)
				body := source.(map[string]any)
				histogram := body[DateHistogram].(map[string]any)
				key := "interval"
				if mode.explicit {
					key = tc.key
				}
				want := map[string]any{
					"field": "time", key: tc.interval, "min_doc_count": int64(0),
					"extended_bounds": map[string]any{"min": factory.timeFieldUnix(start), "max": factory.timeFieldUnix(end)},
				}
				if tc.kind == Date && function.IsAlignTime(tc.window) {
					want["time_zone"] = "America/New_York"
				}
				if tc.wantOffset != "" {
					want["offset"] = tc.wantOffset
				}
				require.Equal(t, want, histogram)
				require.Equal(t, map[string]any{"_value": map[string]any{"sum": map[string]any{"field": "value"}}}, body["aggregations"])
			})
		}
	}
}
