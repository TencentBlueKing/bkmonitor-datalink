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
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func TestGetTagRouter(t *testing.T) {
	testCases := map[string]struct {
		tagKey    []string
		condition string
		expected  string
	}{
		"tr1": {
			tagKey:    []string{"bk_biz_id"},
			condition: "bk_biz_id = '2' and bcs_cluster_id = 'test' or ip='127.0.0.1'",
			expected:  "bk_biz_id==2",
		},
		"tr2": {
			tagKey:    []string{"bk_biz_id", "bcs_cluster_id"},
			condition: "bk_biz_id = '2' and bcs_cluster_id = 'test' or ip='127.0.0.1'",
			expected:  "bk_biz_id==2###bcs_cluster_id==test",
		},
		"tr3": {
			condition: "bk_biz_id = '2' and bcs_cluster_id = 'test' or ip='127.0.0.1'",
			expected:  "",
		},
		"tr4": {
			tagKey:    []string{"bk_biz_id"},
			condition: "namespace = 'test' and ip='127.0.0.1'",
			expected:  "",
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			actual, err := GetTagRouter(context.Background(), c.tagKey, c.condition)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.expected, actual)
			}
		})
	}
}

func MockRouterWithHostInfo(hostInfo influxdb.HostInfo) *Router {
	ir := GetInfluxDBRouter()
	ir.hostInfo = hostInfo
	ir.hostStatusInfo = make(influxdb.HostStatusInfo, len(hostInfo))
	// 将hostInfo 里面的信息初始化到 hostStatusInfo 并且初始化 Read 状态为 true
	for _, v := range hostInfo {
		ir.hostStatusInfo[v.DomainName] = &influxdb.HostStatus{Read: true}
	}
	return ir
}

func TestRouterPingInfluxdb(t *testing.T) {

	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		if req.URL.Host == "127.0.0.1:6371" && strings.Contains(req.URL.Path, "/ping") {
			return &http.Response{
				StatusCode: http.StatusNoContent,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusBadRequest,
		}, nil
	})

	testCases := []struct {
		HostInfo influxdb.HostInfo
		Expected bool
	}{
		{
			HostInfo: map[string]*influxdb.Host{
				"127.0.0.1": {
					DomainName: "127.0.0.1",
					Port:       6371,
					Protocol:   "http",
				},
			},
			Expected: true,
		}, {
			HostInfo: map[string]*influxdb.Host{
				"127.0.0.2": {
					DomainName: "127.0.0.2",
					Port:       6371,
					Protocol:   "http",
				},
			},
			Expected: false,
		},
	}

	for _, v := range testCases {
		ir := MockRouterWithHostInfo(v.HostInfo)
		ir.Ping(context.Background(), time.Second*1, 3)
		for _, j := range ir.hostStatusInfo {
			assert.Equal(t, v.Expected, j.Read)
		}
	}
}
