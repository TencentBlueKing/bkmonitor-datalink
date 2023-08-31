// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestInstance_QueryRaw(t *testing.T) {
	ctx := context.Background()
	mock.SetRedisClient(ctx, "test")
	mockCurl := curl.NewMockCurl(map[string]string{}, log.OtLogger)

	testCases := map[string]struct {
		query    *metadata.Query
		expected string
	}{
		"test query without timezone": {
			query: &metadata.Query{
				DB:          "db",
				Measurement: "measurement",
				Field:       "field",
			},
			expected: `http://127.0.0.1:80/query?db=db&q=select+%22field%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+%22measurement%22+where+time+%3E+1693454553000000000+and+time+%3C+1693454853000000000+`,
		},
		"test query with timezone": {
			query: &metadata.Query{
				DB:          "db",
				Measurement: "transfer_uptime",
				Field:       "value",
				Timezone:    "Asia/Shanghai",
			},
			expected: `http://127.0.0.1:80/query?db=db&q=select+%22value%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+transfer_uptime+where+time+%3E+1693454553000000000+and+time+%3C+1693454853000000000++tz%28%27Asia%2FShanghai%27%29`,
		},
	}
	hints := &storage.SelectHints{
		Start: 1693454553000,
		End:   1693454853000,
		Step:  60,
	}
	option := Options{
		Host:    "127.0.0.1",
		Port:    80,
		Timeout: time.Hour,
		Curl:    mockCurl,
	}

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			ctx, _ = context.WithCancel(ctx)
			instance := NewInstance(ctx, option)
			instance.QueryRaw(ctx, c.query, hints, nil)

			assert.Equal(t, c.expected, mockCurl.Url)
		})
	}
}
