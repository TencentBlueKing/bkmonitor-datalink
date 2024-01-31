// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestAutoDeployProxySvc_Refresh(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	patchA := gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "api/plugin/operate/") {
			data = `{"result":true,"data":{},"code":0,"message":"","request_id":"96f7a8aa498646b8a84c5f446f286893"}`
		}
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	defer patchA.Reset()

	patchB := gomonkey.ApplyFunc(apiservice.NodemanService.PluginInfo, func(s apiservice.NodemanService, pluginName, version string) ([]nodeman.PluginInfoData, error) {
		return []nodeman.PluginInfoData{
			{Version: "v0.1.2", IsReady: true},
			{Version: "V0.2.1", IsReady: true},
			{Version: "v2.1.3", IsReady: true},
			{Version: "V3.1.2", IsReady: true},
			{Version: "v3.2.2", IsReady: true},
		}, nil
	})
	defer patchB.Reset()

	patchC := gomonkey.ApplyFunc(apiservice.CMDBService.SearchCloudArea, func(s apiservice.CMDBService) ([]cmdb.SearchCloudAreaDataInfo, error) {
		return []cmdb.SearchCloudAreaDataInfo{
			{BkCloudId: 0},
			{BkCloudId: 1},
			{BkCloudId: 2},
		}, nil
	})
	defer patchC.Reset()

	patchD := gomonkey.ApplyFunc(apiservice.NodemanService.GetProxies, func(s apiservice.NodemanService, bkCloudId int) ([]nodeman.ProxyData, error) {
		if bkCloudId == 1 {
			return []nodeman.ProxyData{
				{
					BkCloudId: bkCloudId,
					BkHostId:  100,
					InnerIp:   "1.0.0.100",
					BkBizId:   50,
					Status:    "RUNNING",
					Version:   "",
				},
				{
					BkCloudId: bkCloudId,
					BkHostId:  101,
					InnerIp:   "1.0.0.101",
					BkBizId:   50,
					Status:    "RUNNING",
					Version:   "",
				},
			}, nil
		} else {
			return nil, nil
		}
	})

	defer patchD.Reset()

	patchE := gomonkey.ApplyFunc(apiservice.CMDBService.GetHostWithoutBiz, func(s apiservice.CMDBService, ips []string, bkCloudIds []int) ([]cmdb.ListHostsWithoutBizDataInfo, error) {
		return []cmdb.ListHostsWithoutBizDataInfo{
			{
				BkHostId:      110,
				BkHostInnerip: "1.0.0.110",
			},
			{
				BkHostId:      111,
				BkHostInnerip: "1.0.0.111",
			},
		}, nil
	})
	defer patchE.Reset()

	patchF := gomonkey.ApplyFunc(apiservice.NodemanService.PluginSearch, func(s apiservice.NodemanService, bkBizIds, bkHostIds, excludeHosts []int, conditions []interface{}) ([]nodeman.PluginSearchDataItem, error) {
		dataMap := map[int]nodeman.PluginSearchDataItem{
			100: {
				InnerIp:   "1.0.0.100",
				BkCloudId: 1,
				BkHostId:  100,
				Version:   "",
				PluginStatus: []nodeman.PluginSearchDataItemPluginStatus{
					{
						Name:    "bk-collector",
						Version: "v1.2.3",
						HostId:  0,
					},
				},
			},
			101: {
				InnerIp:      "1.0.0.101",
				BkCloudId:    1,
				BkHostId:     101,
				Version:      "",
				PluginStatus: nil,
			},
			110: {
				InnerIp:   "1.0.0.110",
				BkCloudId: 0,
				BkHostId:  110,
				Version:   "",
				PluginStatus: []nodeman.PluginSearchDataItemPluginStatus{
					{
						Name:    "bk-collector",
						Version: "v3.2.2",
						HostId:  0,
					},
				},
			},
			111: {
				InnerIp:   "1.0.0.111",
				BkCloudId: 0,
				BkHostId:  111,
				Version:   "",
				PluginStatus: []nodeman.PluginSearchDataItemPluginStatus{
					{
						Name:    "bk-collector",
						Version: "v1.2.2",
						HostId:  0,
					},
				},
			},
		}
		var result []nodeman.PluginSearchDataItem
		for _, id := range bkHostIds {
			p, ok := dataMap[id]
			if ok {
				result = append(result, p)
			}
		}
		return result, nil
	})
	defer patchF.Reset()

	svc := NewAutoDeployProxySvc()
	err := svc.Refresh("")
	assert.NoError(t, err)
}
