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
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// 独立运行：设置 BKMONITOR_TEST_ES_URL，可另设 BKMONITOR_TEST_ES_SECOND_URL 比较新旧集群。
// 只创建和删除本次测试命名的索引；未配置真实 ES 时跳过。
func TestDateHistogramIntegration(t *testing.T) {
	if os.Getenv("BKMONITOR_TEST_ES_URL") == "" {
		t.Skip("设置 BKMONITOR_TEST_ES_URL 后独立运行真实 ES 日期聚合测试")
	}
	log.InitTestLogger()
	metadata.InitMetadata()
	for _, prefix := range []string{"BKMONITOR_TEST_ES", "BKMONITOR_TEST_ES_SECOND"} {
		address := os.Getenv(prefix + "_URL")
		if address == "" {
			continue
		}
		t.Run(prefix, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(metadata.InitHashID(context.Background()), 2*time.Minute)
			defer cancel()
			metadata.GetQueryParams(ctx).SetTime(time.Unix(0, 0), time.Unix(0, 0), time.Unix(1, 0), time.Second, function.Millisecond, "UTC")
			instance, err := NewInstance(ctx, &InstanceOption{
				Connect: Connect{Address: address, UserName: os.Getenv(prefix + "_USERNAME"), Password: os.Getenv(prefix + "_PASSWORD")},
				Timeout: 10 * time.Second,
			})
			require.NoError(t, err)
			version, err := instance.serverVersion(ctx)
			require.NoError(t, err)
			t.Logf("ES version=%s", version)
			client, err := instance.getClient(ctx, instance.connect)
			require.NoError(t, err)
			t.Cleanup(client.Stop)
			index := fmt.Sprintf("uq_date_histogram_%d", time.Now().UnixNano())
			mappings := map[string]any{"properties": map[string]any{
				"time":   map[string]any{"type": "date", "format": "epoch_millis"},
				"second": map[string]any{"type": "long"}, "millisecond": map[string]any{"type": "long"},
				"microsecond": map[string]any{"type": "long"}, "nanosecond": map[string]any{"type": "long"},
				"value": map[string]any{"type": "long"},
			}}
			if version.Major < 7 {
				mappings = map[string]any{"_doc": mappings}
			}
			_, err = client.CreateIndex(index).BodyJson(map[string]any{
				"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0}, "mappings": mappings,
			}).Do(ctx)
			require.NoError(t, err)
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_, deleteErr := client.DeleteIndex(index).Do(cleanupCtx)
				require.NoError(t, deleteErr)
			})

			// 三组相同取值分别验证普通时间窗、春季 DST 和秋季 DST。
			timestamps := []time.Time{time.UnixMilli(0), time.UnixMilli(20), time.UnixMilli(40)}
			for _, date := range []time.Time{
				time.Date(2024, 3, 9, 6, 0, 0, 0, time.UTC), time.Date(2024, 11, 2, 6, 0, 0, 0, time.UTC),
			} {
				for day := range 3 {
					timestamps = append(timestamps, date.AddDate(0, 0, day))
				}
			}
			values := []int64{2, 3, 5}
			bulk := client.Bulk().Refresh("wait_for")
			for ordinal, timestamp := range timestamps {
				document := histogramTimeValues(timestamp)
				document["time"] = timestamp.UnixMilli()
				document["value"] = values[ordinal%len(values)]
				request := elastic.NewBulkIndexRequest().Index(index).Id(strconv.Itoa(ordinal)).Doc(document)
				if version.Major < 7 {
					request.Type("_doc")
				}
				bulk.Add(request)
			}
			indexed, err := bulk.Do(ctx)
			require.NoError(t, err)
			require.False(t, indexed.Errors)

			for _, tc := range []struct {
				name     string
				field    string
				unit     string
				window   time.Duration
				start    time.Time
				keys     []int64
				timezone string
				offset   int64
			}{
				{name: "10ms", window: 10 * time.Millisecond},
				{name: "1s", window: time.Second},
				{name: "1m", window: time.Minute},
				{name: "1h", window: time.Hour},
				{name: "1d", window: 24 * time.Hour},
				{name: "2d", window: 48 * time.Hour},
				{name: "7d", window: 7 * 24 * time.Hour},
				{name: "62m", window: 62 * time.Minute},
				{name: "3612s", window: time.Hour + 12*time.Second},
				{name: "long_second", field: function.Second, unit: function.Second, window: time.Minute},
				{name: "long_millisecond", field: function.Millisecond, unit: function.Millisecond, window: time.Minute},
				{name: "long_microsecond", field: function.Microsecond, unit: function.Microsecond, window: 3600 * time.Millisecond},
				{name: "long_nanosecond", field: function.Nanosecond, unit: function.Nanosecond, window: time.Millisecond},
				// extended_bounds 与 offset 会保留最后一个空桶；旧 interval 和显式间隔均需保持此结果。
				{name: "long_offset", field: function.Second, unit: function.Second, window: time.Hour, offset: 1800000, keys: []int64{-1800, 1800, 5400, 9000, 12600}},
				{
					name: "spring_DST", window: 24 * time.Hour, timezone: "America/New_York", start: time.Date(2024, 3, 9, 5, 0, 0, 0, time.UTC),
					keys: []int64{1709960400000, 1710046800000, 1710129600000, 1710216000000},
				},
				{
					name: "autumn_DST", window: 24 * time.Hour, timezone: "America/New_York", start: time.Date(2024, 11, 2, 4, 0, 0, 0, time.UTC),
					keys: []int64{1730520000000, 1730606400000, 1730696400000, 1730782800000},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					field := metadata.TimeField{Name: "time", Type: Date, Unit: function.Millisecond}
					if tc.field != "" {
						field = metadata.TimeField{Name: tc.field, Type: Long, Unit: tc.unit}
					}
					start := tc.start
					if start.IsZero() {
						start = time.Unix(0, 0)
					}
					end := start.Add(3 * tc.window)
					zone := "UTC"
					if tc.timezone != "" {
						zone = tc.timezone
						end = time.UnixMilli(tc.keys[len(tc.keys)-1])
					}
					keys := tc.keys
					if keys == nil {
						width := histogramTimeValues(time.Unix(0, int64(tc.window)))[field.Unit]
						keys = []int64{0, width, 2 * width, 3 * width}
					}
					query := &metadata.Query{
						DB: index, Field: "value", TimeField: field,
						Aggregates: metadata.Aggregates{{Name: Sum, Window: tc.window, TimeZone: zone, TimeZoneOffset: tc.offset}},
					}
					prepared, err := instance.PrepareRawQuery(ctx, query, start, end, nil)
					require.NoError(t, err)
					if version.Major >= 8 {
						require.NotContains(t, prepared.body, `"interval":`)
					} else if version.Major < 7 || version.Major == 7 && version.Minor < 2 {
						require.Contains(t, prepared.body, `"interval":`)
					}
					for _, path := range []string{"series", "prepared_raw"} {
						t.Run(path, func(t *testing.T) {
							var result *elastic.SearchResult
							if path == "series" {
								factory := newSeriesFormatFactory(ctx, query, prepared.FieldsMap(), start, end, function.Millisecond, 0)
								result, err = instance.esQuery(ctx, prepared.queryOption, factory)
							} else {
								result, err = instance.executeESQuery(ctx, prepared.queryOption, prepared.fact, prepared.source, prepared.countQuery, prepared.body)
							}
							require.NoError(t, err)
							histogram, ok := result.Aggregations.DateHistogram(field.Name)
							require.True(t, ok)
							actualKeys := make([]int64, 0, len(histogram.Buckets))
							for _, bucket := range histogram.Buckets {
								actualKeys = append(actualKeys, int64(bucket.Key))
							}
							require.Equal(t, keys, actualKeys)
							for bucketIndex, bucket := range histogram.Buckets {
								require.Equal(t, float64(keys[bucketIndex]), bucket.Key)
								var count, sum int64
								for ordinal, timestamp := range timestamps {
									value := histogramTimeValues(timestamp)[field.Unit]
									if value < histogramTimeValues(start)[field.Unit] || value > histogramTimeValues(end)[field.Unit] || value < keys[bucketIndex] {
										continue
									}
									if bucketIndex+1 < len(keys) && value >= keys[bucketIndex+1] {
										continue
									}
									count++
									sum += values[ordinal%len(values)]
								}
								require.Equal(t, count, bucket.DocCount)
								metric, ok := bucket.Sum(FieldValue)
								require.True(t, ok)
								require.NotNil(t, metric.Value)
								require.Equal(t, float64(sum), *metric.Value)
							}
						})
					}
				})
			}

			for _, timeout := range []time.Duration{0, -time.Second} {
				t.Run(fmt.Sprintf("version_timeout_%s", timeout), func(t *testing.T) {
					timeoutCtx, timeoutCancel := context.WithTimeout(ctx, timeout)
					defer timeoutCancel()
					_, err := instance.serverVersion(timeoutCtx)
					require.ErrorIs(t, err, context.DeadlineExceeded)
				})
			}
		})
	}
}

func histogramTimeValues(timestamp time.Time) map[string]int64 {
	return map[string]int64{
		function.Second: timestamp.Unix(), function.Millisecond: timestamp.UnixMilli(),
		function.Microsecond: timestamp.UnixMicro(), function.Nanosecond: timestamp.UnixNano(),
	}
}
