// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetrics

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

const (
	TestTime  = "2022-11-28 10:00:00"
	ParseTime = "2006-01-02 15:04:05"
)

var (
	start = time.Unix(0, 0)
	end   = time.Unix(60*60*6, 0)
	step  = time.Minute

	url       = "http://127.0.0.1/query_engine"
	sourceKey = "username:kit"
	spaceUid  = "space_103"

	resultTableList     = []string{"victor_metrics_result_table_1"}
	bkDataAuthorization = map[string]string{"bkdata_authentication_method": "user", "bkdata_data_token": "", "bk_username": "admin"}
)

func mockInstance(ctx context.Context, mockCurl *curl.MockCurl) *Instance {
	headers := map[string]string{}

	instance, _ := NewInstance(ctx, &Options{
		Address:          url,
		Timeout:          time.Minute,
		Curl:             mockCurl,
		InfluxCompatible: true,
		UseNativeOr:      true,
		Headers:          headers,
	})

	return instance
}

func mockData(ctx context.Context) {
	mock.Init()

	metadata.SetExpand(ctx, &metadata.VmExpand{
		ResultTableList: resultTableList,
	})
	metadata.SetUser(ctx, sourceKey, spaceUid, "")
}

func TestOptions(t *testing.T) {
	ctx := context.Background()
	ctx = metadata.InitHashID(ctx)
	mockData(ctx)

	mockCurl := &curl.MockCurl{}
	instance := mockInstance(ctx, mockCurl)

	q := "count(my_metric)"
	_, _ = instance.QueryRange(ctx, q, start, end, step)

	assert.Equal(t, mockCurl.Opts.Headers[metadata.SpaceUIDHeader], spaceUid)
	assert.Equal(t, mockCurl.Opts.Headers[metadata.BkQuerySourceHeader], sourceKey)

	params := make(map[string]string)
	err := json.Unmarshal(mockCurl.Opts.Body, &params)
	assert.Nil(t, err)

	if err == nil {
		for k, v := range bkDataAuthorization {
			assert.Equal(t, params[k], v)
		}
	}
}
