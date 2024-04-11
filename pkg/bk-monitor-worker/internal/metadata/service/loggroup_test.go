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

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestLogGroupSvc_Refresh(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	lg := customreport.LogGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 77668,
			BkBizID:  50,
			TableID:  "log_group_test_for_refresh_rt",
			MaxRate:  2,
			IsEnable: true,
		},
		LogGroupName: "log_group_test_for_refresh",
	}
	db.Delete(&customreport.LogGroup{}, "log_group_name = ?", lg.LogGroupName)
	assert.NoError(t, lg.Create(db))
	db.Delete(&customreport.LogSubscriptionConfig{}, "log_name = ?", lg.LogGroupName)

	patchA := gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "subscription_update/") || strings.Contains(req.URL.Path, "subscription_run/") {
			data = `{"result":true,"data":{},"code":0,"message":"","request_id":"96f7a8aa498646b8a84c5f446f286893"}`
		}
		if strings.Contains(req.URL.Path, "subscription_create/") || strings.Contains(req.URL.Path, "subscription_run/") {
			data = `{"result":true,"data":{"subscription_id":12211},"code":0,"message":"","request_id":"96f7a8aa498646b8a84c5f446f286893"}`
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

	patchB := gomonkey.ApplyFunc(apiservice.CMDBService.SearchCloudArea, func(s apiservice.CMDBService) ([]cmdb.SearchCloudAreaDataInfo, error) {
		return []cmdb.SearchCloudAreaDataInfo{
			{BkCloudId: 0},
			{BkCloudId: 1},
			{BkCloudId: 2},
		}, nil
	})
	defer patchB.Reset()

	patchC := gomonkey.ApplyFunc(apiservice.NodemanService.GetProxies, func(s apiservice.NodemanService, bkCloudId int) ([]nodeman.ProxyData, error) {
		if bkCloudId == 1 {
			return []nodeman.ProxyData{
				{
					BkCloudId: bkCloudId,
					BkHostId:  101,
					InnerIp:   "1.0.0.101",
					BkBizId:   50,
					Status:    "RUNNING",
					Version:   "",
				},
			}, nil
		} else if bkCloudId == 2 {
			return []nodeman.ProxyData{
				{
					BkCloudId: bkCloudId,
					BkHostId:  102,
					InnerIp:   "1.0.0.102",
					BkBizId:   50,
					Status:    "RUNNING",
					Version:   "",
				},
				{
					BkCloudId: bkCloudId,
					BkHostId:  103,
					InnerIp:   "1.0.0.103",
					BkBizId:   50,
					Status:    "STOP",
					Version:   "",
				},
			}, nil
		} else {
			return nil, nil
		}
	})
	defer patchC.Reset()
	cfg.GlobalCustomReportDefaultProxyIp = []string{"1.0.0.100"}
	cfg.BkdataAESKey = ""
	svc := NewLogGroupSvc(&lg)
	assert.NoError(t, svc.Refresh())
	var subscrip customreport.LogSubscriptionConfig
	assert.NoError(t, customreport.NewLogSubscriptionConfigQuerySet(db).LogNameEq(lg.LogGroupName).BkBizIdEq(lg.BkBizID).SubscriptionIdEq(12211).One(&subscrip))
	equal, _ := jsonx.CompareJson(subscrip.Config, `{"scope":{"node_type":"INSTANCE","nodes":[{"bk_host_id":{"bk_cloud_id":0,"bk_supplier_id":0,"ip":"1.0.0.100"}},{"bk_host_id":{"bk_cloud_id":1,"bk_supplier_id":0,"ip":"1.0.0.101"}},{"bk_host_id":{"bk_cloud_id":2,"bk_supplier_id":0,"ip":"1.0.0.102"}}],"object_type":"HOST"},"steps":[{"config":{"config_templates":[{"name":"bk-collector-application.conf","version":"latest"}],"plugin_name":"bk-collector","plugin_version":"latest"},"id":"bk-collector","params":{"context":{"bk_app_name":"log_group_test_for_refresh","bk_biz_id":50,"bk_data_token":"Ymtia2JrYmtia2JrYmtia2mibzchCKm4u0m8pTJwt3qgDDmF5m0OzKrhTyhW048Ui9ym61WRKa6dd+InCBNjkg==","qps_config":{"bk_app_name":"log_group_test_for_refresh","name":"rate_limiter/token_bucket","qps_config":2,"type":"token_bucket"}}},"type":"PLUGIN"}]}`)
	assert.True(t, equal)

	assert.NoError(t, svc.Refresh())
	assert.NoError(t, customreport.NewLogSubscriptionConfigQuerySet(db).LogNameEq(lg.LogGroupName).BkBizIdEq(lg.BkBizID).SubscriptionIdEq(12211).One(&subscrip))
	equal, _ = jsonx.CompareJson(subscrip.Config, `{"subscription_id":12211, "run_immediately":true ,"scope":{"node_type":"INSTANCE","nodes":[{"bk_host_id":{"bk_cloud_id":0,"bk_supplier_id":0,"ip":"1.0.0.100"}},{"bk_host_id":{"bk_cloud_id":1,"bk_supplier_id":0,"ip":"1.0.0.101"}},{"bk_host_id":{"bk_cloud_id":2,"bk_supplier_id":0,"ip":"1.0.0.102"}}],"object_type":"HOST"},"steps":[{"config":{"config_templates":[{"name":"bk-collector-application.conf","version":"latest"}],"plugin_name":"bk-collector","plugin_version":"latest"},"id":"bk-collector","params":{"context":{"bk_app_name":"log_group_test_for_refresh","bk_biz_id":50,"bk_data_token":"Ymtia2JrYmtia2JrYmtia2mibzchCKm4u0m8pTJwt3qgDDmF5m0OzKrhTyhW048Ui9ym61WRKa6dd+InCBNjkg==","qps_config":{"bk_app_name":"log_group_test_for_refresh","name":"rate_limiter/token_bucket","qps_config":2,"type":"token_bucket"}}},"type":"PLUGIN"}]}`)
	assert.True(t, equal)

}
