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
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		bksql.QuerySyncRequest{
			SQL: fmt.Sprintf(
				`SELECT * FROM restriction_table WHERE dtEventTimeStamp >= %d AND dtEventTimeStamp < %d LIMIT 5`,
				start.UnixMilli(),
				end.UnixMilli(),
			),
		},
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

type captureCurl struct {
	body []byte
}

func (c *captureCurl) WithDecoder(func(context.Context, io.Reader, any) (int, error)) {}

func (c *captureCurl) Request(_ context.Context, _ string, opt curl.Options, res any) (int, error) {
	c.body = append([]byte(nil), opt.Body...)
	if r, ok := res.(*bksql.Result); ok {
		r.Result = true
		r.Code = bksql.StatusOK
	}
	return len(opt.Body), nil
}

func TestClient_QuerySyncWithClusterNameSerializesProperties(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	cc := &captureCurl{}

	res := (&bksql.Client{}).WithUrl(mock.BkBaseUrl).WithCurl(cc).QuerySync(
		ctx,
		bksql.QuerySyncRequest{
			SQL:         "SELECT * FROM `bkbase_table`.doris",
			ClusterName: "doris_default",
		},
		nil,
	)

	require.Equal(t, bksql.StatusOK, res.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(cc.body, &body))
	assert.Equal(t, "SELECT * FROM `bkbase_table`.doris", body["sql"])
	assert.Equal(t, "bk_code", body["bk_app_code"])
	assert.Equal(t, "admin", body["bk_username"])
	assert.Equal(t, "123456", body["bkdata_data_token"])
	require.Contains(t, body, "properties")
	assert.Equal(t, map[string]any{"cluster_name": "doris_default"}, body["properties"])
}
