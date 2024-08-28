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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

var (
	address string
	code    string
	token   string
	secret  string

	client *Client

	once sync.Once
)

func mockClient() *Client {
	once.Do(func() {
		address = viper.GetString("mock.bk_sql.address")
		code = viper.GetString("mock.bk_sql.code")
		secret = viper.GetString("mock.bk_sql.secret")
		token = viper.GetString("mock.bk_sql.token")

		client = &Client{
			url:  address,
			curl: &curl.HttpCurl{Log: log.DefaultLogger},
		}
	})
	return client
}

func TestClient_QuerySync(t *testing.T) {
	ctx := context.Background()

	mock.Init()
	mockClient()

	ctx = metadata.InitHashID(ctx)

	end := time.Now()
	start := end.Add(time.Minute * -5)

	res := client.QuerySync(
		ctx,
		fmt.Sprintf(
			`SELECT * FROM 132_hander_opmon_avg WHERE dtEventTimeStamp >= %d AND dtEventTimeStamp < %d LIMIT 10`,
			start.UnixMilli(),
			end.UnixMilli(),
		),
		nil,
	)

	assert.Equal(t, StatusOK, res.Code)
	d, ok := res.Data.(*QuerySyncResultData)
	assert.True(t, ok)

	if d != nil {
		assert.NotEmpty(t, d.List)
		log.Infof(ctx, "%+v", d)
	}
}
