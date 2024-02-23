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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestPingServerSubscriptionConfigSvc_RefreshPingConf(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	patchA := gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "api/plugin/search/") {
			data = `{"result":true,"data":{"total":2,"list":[{"status":"RUNNING","inner_ip":"1.0.0.1","bk_addressing":"static","bk_host_name":"1-0-0-1","bk_biz_id":10,"bk_agent_id":"xxx","cpu_arch":"x86_64","os_type":"LINUX","inner_ipv6":"","bk_cloud_id":0,"node_type":"Agent","node_from":"NODE_MAN","ap_id":2,"bk_host_id":1,"version":"v2.1.3-beta.11","status_display":"正常","bk_cloud_name":"直连区域","bk_biz_name":"xxx","job_result":{},"plugin_status":[{"name":"bkunifylogbeat","status":"UNREGISTER","version":"","host_id":58},{"name":"bkmonitorproxy","status":"RUNNING","version":"1.22.1419","host_id":58}],"operate_permission":true,"setup_path":"/usr/local/gse2_paas3_dev"}]},"code":0,"message":"","request_id":"96f7a8aa498646b8a84c5f446f286893"}`
		}

		if strings.Contains(req.URL.Path, "api/host/proxies/") {
			fmt.Println(11111)
			data = `{"result":true,"data":[{"bk_cloud_id":1,"bk_host_id":3,"inner_ip":"1.0.0.3","inner_ipv6":"","outer_ip":"1.0.0.3","outer_ipv6":"","login_ip":".0.0.3","data_ip":"","bk_biz_id":11,"is_manual":false,"extra_data":{"data_path":"/data/gse2_paas3_dev/file_cache","bt_speed_limit":null,"enable_compression":false,"peer_exchange_switch_for_agent":0},"bk_biz_name":"xxxx","ap_id":-1,"ap_name":"xx","status":"RUNNING","status_display":"异常","version":"","account":"root","auth_type":"PASSWORD","port":22,"re_certification":true,"job_result":{},"pagent_count":0,"permissions":{"operate":true}}],"code":0,"message":"","request_id":"2eda6ccc75f14fdd843b18f139f288f1"}`
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

	patchB := gomonkey.ApplyFunc(apiservice.CMDBService.GetAllHost, func(s apiservice.CMDBService) ([]apiservice.Host, error) {
		return []apiservice.Host{
			{
				ListBizHostsTopoDataInfoHost: cmdb.ListBizHostsTopoDataInfoHost{
					BkCloudId:     0,
					BkHostId:      1,
					BkHostInnerip: "1.0.0.1",
					BkState:       nil,
				},
				BkBizId: 10,
			},
			{
				ListBizHostsTopoDataInfoHost: cmdb.ListBizHostsTopoDataInfoHost{
					BkCloudId:     0,
					BkHostId:      2,
					BkHostInnerip: "1.0.0.2",
					BkState:       nil,
				},
				BkBizId: 10,
			},
			{
				ListBizHostsTopoDataInfoHost: cmdb.ListBizHostsTopoDataInfoHost{
					BkCloudId:     1,
					BkHostId:      3,
					BkHostInnerip: "1.0.0.3",
					BkState:       nil,
				},
				BkBizId: 11,
			},
			{
				ListBizHostsTopoDataInfoHost: cmdb.ListBizHostsTopoDataInfoHost{
					BkCloudId:     1,
					BkHostId:      4,
					BkHostInnerip: "1.0.0.4",
				},
				BkBizId: 11,
			},
		}, nil
	})
	defer patchB.Reset()
	patchC := gomonkey.ApplyFunc(apiservice.CMDBService.GetHostWithoutBiz, func(s apiservice.CMDBService, ips []string, bkCloudIds []int) ([]cmdb.ListHostsWithoutBizDataInfo, error) {
		return []cmdb.ListHostsWithoutBizDataInfo{
			{
				BkCloudId:     0,
				BkHostId:      1,
				BkHostInnerip: "1.0.0.1",
			},
		}, nil
	})
	defer patchC.Reset()

	patchD := gomonkey.ApplyFunc(apiservice.CMDBService.FindHostBizRelationMap, func(s apiservice.CMDBService, bkHostIds []int) (map[int]int, error) {
		return map[int]int{1: 10}, nil
	})
	defer patchD.Reset()

	patchE := gomonkey.ApplyFunc(apiservice.CMDBService.FindHostBizRelationMap, func(s apiservice.CMDBService, bkHostIds []int) (map[int]int, error) {
		return map[int]int{1: 10}, nil
	})
	defer patchE.Reset()

	patchF := gomonkey.ApplyFunc(PingServerSubscriptionConfigSvc.CreateSubscription, func(s PingServerSubscriptionConfigSvc, bkCloudId int, items map[int][]map[string]interface{}, targetHosts []hostInfo, pluginName string) error {
		if bkCloudId == 0 {
			assert.Equal(t, 2, len(items[1]))
			assert.Equal(t, 1, targetHosts[0].BkHostId)
			assert.Equal(t, 10, targetHosts[0].BkBizId)
			assert.Equal(t, "1.0.0.1", targetHosts[0].IP)
			return nil
		}
		if bkCloudId == 1 {
			assert.Equal(t, 2, len(items[1]))
			assert.Equal(t, 3, targetHosts[0].BkHostId)
			assert.Equal(t, 11, targetHosts[0].BkBizId)
			assert.Equal(t, "1.0.0.3", targetHosts[0].IP)
			return nil
		}
		return nil
	})
	defer patchF.Reset()

	svc := NewPingServerSubscriptionConfigSvc(nil)
	err := svc.RefreshPingConf("bkmonitorproxy")
	assert.NoError(t, err)
}
