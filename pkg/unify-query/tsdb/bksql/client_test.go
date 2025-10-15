// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
)

var client *bksql.Client

func MockClient() *bksql.Client {
	if client == nil {
		client = (&bksql.Client{}).WithUrl(mock.BkBaseUrl).WithCurl(&curl.HttpCurl{})
	}

	return client
}

func TestClient_QuerySync(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	start := time.UnixMilli(1729838623416)
	end := time.UnixMilli(1729838923416)

	mock.BkSQL.Set(map[string]any{
		`SELECT * FROM restriction_table WHERE dtEventTimeStamp >= 1729838623416 AND dtEventTimeStamp < 1729838923416 LIMIT 5`: &bksql.Result{
			Result: true,
			Code:   bksql.StatusOK,
			Data: &bksql.QuerySyncResultData{
				TotalRecords: 5,
				SelectFieldsOrder: []string{
					"dtEventTimeStamp",
					"value",
					"metric_type",
				},
				List: []map[string]any{
					{
						"dtEventTimeStamp": 1726732280000,
						"value":            2.0,
						"metric_type":      "hermes-server",
					},
					{
						"dtEventTimeStamp": 1726732280000,
						"value":            0.02,
						"metric_type":      "hermes-server",
					},
					{
						"dtEventTimeStamp": 1726732280000,
						"value":            1072.0,
						"metric_type":      "hermes",
					},
					{
						"dtEventTimeStamp": 1726732280000,
						"value":            0.4575,
						"metric_type":      "hermes",
					},
					{
						"dtEventTimeStamp": 1726732280000,
						"value":            2.147483648e9,
						"metric_type":      "hermes",
					},
				},
			},
		},
	})

	res := MockClient().QuerySync(
		ctx,
		fmt.Sprintf(
			`SELECT * FROM restriction_table WHERE dtEventTimeStamp >= %d AND dtEventTimeStamp < %d LIMIT 5`,
			start.UnixMilli(),
			end.UnixMilli(),
		),
		nil,
	)

	assert.Equal(t, bksql.StatusOK, res.Code)
	d, ok := res.Data.(*bksql.QuerySyncResultData)
	assert.True(t, ok)
	assert.Equal(t, d.TotalRecords, 5)

	if d != nil {
		assert.NotEmpty(t, d.List)
	}
}
