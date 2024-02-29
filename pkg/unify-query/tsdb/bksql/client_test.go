// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

var (
	url    string
	code   string
	token  string
	secret string

	client *Client

	once sync.Once
)

func mockClient() *Client {
	once.Do(func() {
		url = "http://127.0.0.1"
		code = "code"
		secret = "secret"
		token = "token"

		client = &Client{
			Address:                    url,
			BkdataAuthenticationMethod: "token",
			BkUsername:                 BkUserName,
			BkAppCode:                  code,
			PreferStorage:              TSpider,
			BkdataDataToken:            token,
			BkAppSecret:                secret,
			Timeout:                    time.Second * 30,
			Log:                        log.DefaultLogger,
			ContentType:                "application/json",
			Curl: &curl.HttpCurl{
				Log: log.DefaultLogger,
			},
		}
	})
	return client
}

func TestClient_QueryAsync(t *testing.T) {
	ctx := context.Background()

	mock.Init()
	mockClient()

	res := client.QueryAsync(ctx, `SELECT * FROM 132_hander_opmon_avg WHERE dtEventTimeStamp >= 1700745780000 AND dtEventTimeStamp < 1700746080000 LIMIT 10`)

	assert.Equal(t, res.Code, StatusOK)
	d, ok := res.Data.(*QueryAsyncData)
	assert.True(t, ok)

	if d != nil {
		assert.NotEmpty(t, d.QueryId)
		log.Infof(ctx, "%+v", d)
	}
}

func TestClient_QueryAsyncState(t *testing.T) {
	ctx := context.Background()

	mock.Init()
	mockClient()

	res := client.QueryAsyncState(ctx, "BK912760164455546880")

	assert.Equal(t, res.Code, StatusOK)
	d, ok := res.Data.(*QueryAsyncStateData)
	assert.True(t, ok)

	if d != nil {
		assert.NotEmpty(t, d.State)
		log.Infof(ctx, "%+v", d)
	}
}

func TestClient_QueryAsyncResult(t *testing.T) {
	ctx := context.Background()

	mock.Init()
	mockClient()

	res := client.QueryAsyncResult(ctx, "BK912760164455546880")

	assert.Equal(t, res.Code, StatusOK)
	d, ok := res.Data.(*QueryAsyncResultData)
	assert.True(t, ok)

	if d != nil {
		assert.NotEmpty(t, d.List)
		log.Infof(ctx, "%+v", d)
	}
}
