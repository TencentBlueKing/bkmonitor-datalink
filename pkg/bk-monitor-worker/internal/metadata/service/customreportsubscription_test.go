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
	"strconv"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkmonitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestCustomReportSubscriptionSvc_getProtocol(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	db.Delete(&customreport.TimeSeriesGroup{}, "bk_data_id in (?)", []int{32321, 32322, 32323})
	tsJ := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 32322,
			BkBizID:  50,
			TableID:  "test_custom_ts_detail_table_j",
			MaxRate:  0,
			IsEnable: true,
			IsDelete: false,
		},
		TimeSeriesGroupName: "test_custom_ts_detail_table_j_name",
	}
	tsP := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 32323,
			BkBizID:  50,
			TableID:  "test_custom_ts_detail_table_p",
			MaxRate:  0,
			IsEnable: true,
			IsDelete: false,
		},
		TimeSeriesGroupName: "test_custom_ts_detail_table_p_name",
	}
	assert.NoError(t, tsJ.Create(db))
	assert.NoError(t, tsP.Create(db))

	patchA := gomonkey.ApplyFunc(apiservice.MetadataService.CustomTimeSeriesDetail, func(s apiservice.MetadataService, bkBizId int, timeSeriesGroupId uint, modelOnly bool) (*bkmonitor.CustomTimeSeriesDetailData, error) {
		if bkBizId == tsJ.BkBizID && timeSeriesGroupId == tsJ.TimeSeriesGroupID {
			return &bkmonitor.CustomTimeSeriesDetailData{
				Protocol: "json",
			}, nil
		}
		if bkBizId == tsP.BkBizID && timeSeriesGroupId == tsP.TimeSeriesGroupID {
			return &bkmonitor.CustomTimeSeriesDetailData{
				Protocol: "prometheus",
			}, nil
		}
		return &bkmonitor.CustomTimeSeriesDetailData{}, nil
	})
	defer patchA.Reset()

	svc := NewCustomReportSubscriptionSvc(nil)
	// 非ts记录
	protocol, err := svc.getProtocol(32321)
	assert.NoError(t, err)
	assert.Equal(t, "json", protocol)

	// protocol = json
	protocol, err = svc.getProtocol(32322)
	assert.NoError(t, err)
	assert.Equal(t, "json", protocol)

	// protocol = prometheus
	protocol, err = svc.getProtocol(32323)
	assert.NoError(t, err)
	assert.Equal(t, "prometheus", protocol)

}

func TestCustomReportSubscriptionSvc_GetCustomConfig(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	eg := customreport.EventGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 32331,
			BkBizID:  60,
			TableID:  "test_for_get_custom_config_eg_table",
			MaxRate:  10,
			IsEnable: true,
		},
		EventGroupName: "test_for_get_custom_config_eg",
	}
	ts := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID:           32332,
			BkBizID:            60,
			TableID:            "test_for_get_custom_config_ts_table",
			MaxRate:            20,
			IsEnable:           true,
			IsSplitMeasurement: false,
		},
		TimeSeriesGroupName: "test_for_get_custom_config_ts",
	}
	db.Delete(&customreport.EventGroup{}, "event_group_name = ?", eg.EventGroupName)
	db.Delete(&customreport.TimeSeriesGroup{}, "time_series_group_name = ?", ts.TimeSeriesGroupName)
	dsEg := resulttable.DataSource{
		BkDataId:        eg.BkDataID,
		Token:           "token_for_eg",
		DataName:        "ds_for_custom_config_eg_test",
		DataDescription: "ds_for_custom_config_eg_test",
		IsCustomSource:  true,
		IsEnable:        true,
	}
	dsTs := resulttable.DataSource{
		BkDataId:        ts.BkDataID,
		Token:           "token_for_ts",
		DataName:        "ds_for_custom_config_ts_test",
		DataDescription: "ds_for_custom_config_ts_test",
		IsCustomSource:  true,
		IsEnable:        true,
	}
	db.Delete(&resulttable.DataSource{}, "bk_data_id in (?)", []uint{eg.BkDataID, ts.BkDataID})
	db.Delete(&customreport.TimeSeriesGroup{}, "bk_data_id = ?", ts.BkDataID)
	db.Delete(&customreport.EventGroup{}, "bk_data_id = ?", eg.BkDataID)

	assert.NoError(t, eg.Create(db))
	assert.NoError(t, ts.Create(db))
	assert.NoError(t, dsEg.Create(db))
	assert.NoError(t, dsTs.Create(db))

	patchA := gomonkey.ApplyFunc(apiservice.MetadataService.CustomTimeSeriesDetail, func(s apiservice.MetadataService, bkBizId int, timeSeriesGroupId uint, modelOnly bool) (*bkmonitor.CustomTimeSeriesDetailData, error) {
		if bkBizId == ts.BkBizID && timeSeriesGroupId == ts.TimeSeriesGroupID {
			return &bkmonitor.CustomTimeSeriesDetailData{
				Protocol: "prometheus",
			}, nil
		}
		return &bkmonitor.CustomTimeSeriesDetailData{}, nil
	})
	defer patchA.Reset()

	svc := NewCustomReportSubscriptionSvc(nil)
	bizId := ts.BkBizID
	// event bk-collector
	config, err := svc.GetCustomConfig(&bizId, "event", "bk-collector")
	assert.NoError(t, err)
	configStr, err := jsonx.MarshalString(config)
	assert.NoError(t, err)
	equal, err := jsonx.CompareJson(`{"60":[{"bk_data_id":32331,"bk_data_token":"token_for_eg","qps_config":{"name":"rate_limiter/token_bucket","qps":10,"type":"token_bucket"},"sub_config_name":"bk-collector-report-v2.conf","token_config":{"name":"token_checker/proxy","proxy_dataid":32331,"proxy_token":"token_for_eg"},"validator_config":{"max_future_time_offset":3600,"name":"proxy_validator/common","type":"event","version":"v2"}}]}`, configStr)
	assert.NoError(t, err)
	assert.True(t, equal)

	// ts bk-collector
	config, err = svc.GetCustomConfig(&bizId, "time_series", "bk-collector")
	assert.NoError(t, err)
	delete(config[bizId][0], "bk_data_token")
	configStr, err = jsonx.MarshalString(config)
	assert.NoError(t, err)
	equal, err = jsonx.CompareJson(`{"60":[{"bk_app_name":"prometheus_report","bk_biz_id":60,"bk_data_id":32332,"qps_config":{"name":"rate_limiter/token_bucket","qps":20,"type":"token_bucket"},"sub_config_name":"bk-collector-application.conf"}]}`, configStr)
	assert.NoError(t, err)
	assert.True(t, equal)

	// event bkmonitorproxy
	config, err = svc.GetCustomConfig(&bizId, "event", "bkmonitorproxy")
	assert.NoError(t, err)
	configStr, err = jsonx.MarshalString(config)
	assert.NoError(t, err)
	equal, err = jsonx.CompareJson(`{"60":[{"access_token":"token_for_eg","dataid":32331,"datatype":"event","max_future_time_offset":3600,"max_rate":10,"version":"v2"}]}`, configStr)
	assert.NoError(t, err)
	assert.True(t, equal)

	// ts bkmonitorproxy
	config, err = svc.GetCustomConfig(&bizId, "time_series", "bkmonitorproxy")
	assert.NoError(t, err)
	configStr, err = jsonx.MarshalString(config)
	assert.NoError(t, err)
	equal, err = jsonx.CompareJson(`{"60":[{"access_token":"token_for_ts","dataid":32332,"datatype":"time_series","max_future_time_offset":3600,"max_rate":20,"version":"v2"}]}`, configStr)
	assert.NoError(t, err)
	assert.True(t, equal)
}

func TestCustomReportSubscriptionSvc_RefreshCustomReport2Config(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	patchA := gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "plugin_info") {
			data = `{"result":true,"data":[{"id":16,"module":"gse_plugin","project":"bk-collector","version":"0.4.1.61","os":"linux","cpu_arch":"x86_64","pkg_name":"bk-collector-0.4.1.61.tgz","pkg_size":11299931,"pkg_mtime":"2023-05-24 04:32:06.384391+00:00","md5":"2e570ca6622bfddc1b7ac73d1ca8a2d5","creator":"admin","location":"http://127.0.0.1/download/linux/x86_64","is_ready":true,"is_release_version":true,"name":"bk-collector","source_app_code":"bk_monitorv3"},{"id":284,"module":"gse_plugin","project":"bk-collector","version":"0.32.1231","os":"linux","cpu_arch":"x86_64","pkg_name":"bk-collector-0.32.1231.tgz","pkg_size":14982323,"pkg_mtime":"2023-09-26 12:05:25.857274+00:00","md5":"de3bedac4b24ba303d657f0df2e746aa","creator":"admin","location":"http://127.0.0.1/download/linux/x86_64","is_ready":true,"is_release_version":true,"name":"bk-collector","source_app_code":"bk_monitorv3"},{"id":418,"module":"gse_plugin","project":"bk-collector","version":"0.36.1342","os":"linux","cpu_arch":"x86_64","pkg_name":"bk-collector-0.36.1342.tgz","pkg_size":18841357,"pkg_mtime":"2023-11-17 06:37:31.785533+00:00","md5":"f70c8389dfec8b0ca4bf7dec79170e9f","creator":"admin","location":"http://127.0.0.1/download/linux/x86_64","is_ready":true,"is_release_version":true,"name":"bk-collector","source_app_code":"bk_monitorv3"}],"code":0,"message":"","request_id":"d6c2a25f66614344b3bccca0204fa841"}`
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
	var record []string
	patchB := gomonkey.ApplyFunc(CustomReportSubscriptionSvc.RefreshCollectorCustomConf, func(b CustomReportSubscriptionSvc, bkBizId *int, pluginName, opType string) error {
		var bkBizIdStr string
		if bkBizId == nil {
			bkBizIdStr = "nil"
		} else {
			bkBizIdStr = strconv.Itoa(*bkBizId)
		}
		record = append(record, fmt.Sprintf("%v_%s_%s", bkBizIdStr, pluginName, opType))
		return nil
	})
	defer patchB.Reset()
	svc := NewCustomReportSubscriptionSvc(nil)
	bizId := 2
	err := svc.RefreshCustomReport2Config(&bizId)
	assert.NoError(t, err)
	assert.ElementsMatch(t, record, []string{"2_bk-collector_add", "nil_bkmonitorproxy_add"})
}

func TestCustomReportSubscriptionSvc_RefreshCollectorCustomConf(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	cfg.GlobalCustomReportDefaultProxyIp = []string{"127.0.0.1"}
	patchA := gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "search_business") {
			data = `{"result":true,"code":0,"data":{"count":1,"info":[{"bk_biz_developer":"","bk_biz_id":60,"bk_biz_maintainer":"admin","bk_biz_name":"蓝鲸测试","bk_biz_productor":"","bk_biz_tester":"test8","bk_supplier_account":"0","create_time":"2023-05-23T23:19:57.356+08:00","db_app_abbr":"blueking_test","default":0,"language":"1","last_time":"2023-11-28T10:45:12.201+08:00","life_cycle":"2","operator":"","time_zone":"Asia/Shanghai"}]},"message":"success","permission":null,"request_id":"xx"}`
		}
		if strings.Contains(req.URL.Path, "api/host/biz_proxies/") {
			data = `{"result":true,"data":[{"bk_cloud_id":3,"bk_addressing":"static","inner_ip":"127.0.0.1","inner_ipv6":"","outer_ip":"127.0.0.1","outer_ipv6":"","login_ip":"127.0.0.1","data_ip":"","bk_biz_id":2}],"code":0,"message":"","request_id":"b9b316a32bd34135854a21931c1ffaef"}`
		}
		if strings.Contains(req.URL.Path, "list_biz_hosts_topo") {
			data = `{"result":true,"code":0,"data":{"count":1,"info":[{"host":{"bk_agent_id":"xxxxxxxxxxxx","bk_bak_operator":"admin","bk_cloud_id":3,"bk_comment":"","bk_host_id":8888,"bk_host_innerip":"127.0.0.2","bk_host_innerip_v6":"","bk_host_name":"VM-xxxx","bk_host_outerip":"127.0.0.1","bk_host_outerip_v6":"","bk_isp_name":null,"bk_os_name":"linux centos","bk_os_type":"1","bk_os_version":"7.9.2009","bk_province_name":null,"bk_state":null,"bk_state_name":null,"bk_supplier_account":"0","operator":"admin"},"topo":[{"bk_set_id":123,"bk_set_name":"BK_NET","module":[{"bk_module_id":456,"bk_module_name":"proxy"}]}]}]},"message":"success","permission":null,"request_id":"xx"}`
		}
		if strings.Contains(req.URL.Path, "list_hosts_without_biz") {
			data = `{"result":true,"code":0,"data":{"count":1,"info":[{"bk_addressing":"static","bk_agent_id":"xxxx98925385p","bk_asset_id":"","bk_bak_operator":"admin","bk_cloud_host_identifier":false,"bk_cloud_host_status":null,"bk_cloud_id":0,"bk_cloud_inst_id":"","bk_cloud_vendor":null,"bk_comment":"","bk_cpu":4,"bk_cpu_architecture":"x86","bk_cpu_module":"IntexxxxxxHz","bk_disk":1,"bk_host_id":9999,"bk_host_innerip":"127.0.0.1","bk_host_innerip_v6":"","bk_host_name":"VM-xxxxxxx","bk_host_outerip":"","bk_host_outerip_v6":"","bk_isp_name":null,"bk_mac":"52:54:00:xx:xx:xx","bk_mem":7599,"bk_os_bit":"64-bit","bk_os_name":"linux centos","bk_os_type":"1","bk_os_version":"7.9.2009","bk_outer_mac":"","bk_province_name":null,"bk_service_term":null,"bk_sla":null,"bk_sn":"","bk_state":null,"bk_state_name":null,"bk_supplier_account":"0","create_time":"2023-08-08T20:48:41.084+08:00","import_from":"3","last_time":"2023-10-31T15:26:11.442+08:00","operator":"admin"}]},"message":"success","permission":null,"request_id":"xx"}`
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
	tsConfig := map[string]interface{}{
		"bl_app_name": "prometheus_report",
		"bk_biz_id":   60,
		"bk_data_id":  uint(32332),
		"qps_config": map[string]interface{}{
			"name": "rate_limiter/token_bucket",
			"qps":  20,
			"type": "token_bucket",
		},
		"sub_config_name": "bk-collector-application.conf",
	}
	egConfig := map[string]interface{}{
		"bk_data_id":    uint(32331),
		"bk_data_token": "token_for_eg",
		"qps_config": map[string]interface{}{
			"name": "rate_limiter/token_bucket",
			"qps":  10,
			"type": "token_bucket",
		},
		"sub_config_name": "bk-collector-report-v2.conf",
		"token_config": map[string]interface{}{
			"name":         "token_checker/proxy",
			"proxy_dataid": 32331,
			"proxy_token":  "token_for_eg",
		},
		"validator_config": map[string]interface{}{
			"max_future_time_offset": 3600,
			"name":                   "proxy_validator/common",
			"type":                   "event",
			"version":                "v2",
		},
	}
	patchB := gomonkey.ApplyFunc(CustomReportSubscriptionSvc.GetCustomConfig, func(b CustomReportSubscriptionSvc, bkBizId *int, dataType, pluginName string) (map[int][]map[string]interface{}, error) {
		if dataType == "event" {
			return map[int][]map[string]interface{}{60: {egConfig}}, nil
		}
		if dataType == "time_series" {
			return map[int][]map[string]interface{}{60: {tsConfig}}, nil
		}
		return nil, nil
	})
	defer patchB.Reset()

	record := make(map[string][]map[string]interface{})
	patchC := gomonkey.ApplyFunc(CustomReportSubscriptionSvc.CreateSubscription, func(b CustomReportSubscriptionSvc, bkBizId int, items []map[string]interface{}, bkHostIds []int, pluginName string, opType string) error {
		record[fmt.Sprintf("%v_%s_%v", bkBizId, pluginName, bkHostIds[0])] = items
		return nil
	})
	defer patchC.Reset()

	svc := NewCustomReportSubscriptionSvc(nil)
	bizId := 60
	err := svc.RefreshCollectorCustomConf(&bizId, "bk-collector", "add")
	assert.NoError(t, err)
	fmt.Println(record)
	fmt.Println(record["60_bk-collector_8888"])
	equal, err := jsonx.CompareObjects(record["60_bk-collector_8888"], []map[string]interface{}{tsConfig, egConfig})
	assert.NoError(t, err)
	assert.True(t, equal)
	fmt.Println(record["0_bk-collector_9999"])

}

func TestCustomReportSubscriptionSvc_CreateSubscription(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	crs := customreport.CustomReportSubscription{
		BkBizId:        60,
		SubscriptionId: 987654,
		BkDataID:       32331,
		Config:         `{"scope":{}}`,
	}
	db.Delete(&customreport.CustomReportSubscription{}, "bk_data_id in (?)", []uint{32331, 32332})
	assert.NoError(t, crs.Create(db))

	tsConfig := map[string]interface{}{
		"bl_app_name": "prometheus_report",
		"bk_biz_id":   60,
		"bk_data_id":  uint(32332),
		"qps_config": map[string]interface{}{
			"name": "rate_limiter/token_bucket",
			"qps":  20,
			"type": "token_bucket",
		},
		"sub_config_name": "bk-collector-application.conf",
	}
	egConfig := map[string]interface{}{
		"bk_data_id":    uint(32331),
		"bk_data_token": "token_for_eg",
		"qps_config": map[string]interface{}{
			"name": "rate_limiter/token_bucket",
			"qps":  10,
			"type": "token_bucket",
		},
		"sub_config_name": "bk-collector-report-v2.conf",
		"token_config": map[string]interface{}{
			"name":         "token_checker/proxy",
			"proxy_dataid": 32331,
			"proxy_token":  "token_for_eg",
		},
		"validator_config": map[string]interface{}{
			"max_future_time_offset": 3600,
			"name":                   "proxy_validator/common",
			"type":                   "event",
			"version":                "v2",
		},
	}
	patchA := gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "subscription_create/") {
			data = `{"result":true,"code":0,"data":{"subscription_id": 987655},"message":"success","permission":null,"request_id":"xx"}`
		}
		if strings.Contains(req.URL.Path, "subscription_run/") {
			data = `{"result":true,"code":0,"data":{},"message":"success","permission":null,"request_id":"xx"}`
		}
		if strings.Contains(req.URL.Path, "subscription_update/") {
			data = `{"result":true,"code":0,"data":{},"message":"success","permission":null,"request_id":"xx"}`
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
	svc := NewCustomReportSubscriptionSvc(nil)
	err := svc.CreateSubscription(60, []map[string]interface{}{tsConfig, egConfig}, []int{8888}, "bk-collector", "add")
	assert.NoError(t, err)
	var crsTs, crsEg customreport.CustomReportSubscription
	assert.NoError(t, customreport.NewCustomReportSubscriptionQuerySet(db).BkDataIDEq(32331).One(&crsEg))
	assert.NoError(t, customreport.NewCustomReportSubscriptionQuerySet(db).BkDataIDEq(32332).One(&crsTs))
	equal, err := jsonx.CompareJson(crsEg.Config, `{"run_immediately":true,"scope":{"node_type":"INSTANCE","nodes":[{"bk_host_id":8888}],"object_type":"HOST"},"steps":[{"config":{"config_templates":[{"name":"bk-collector-report-v2.conf","version":"latest"}],"plugin_name":"bk-collector","plugin_version":"latest"},"id":"bk-collector","params":{"context":{"bk_biz_id":60,"bk_data_id":32331,"bk_data_token":"token_for_eg","qps_config":{"name":"rate_limiter/token_bucket","qps":10,"type":"token_bucket"},"token_config":{"name":"token_checker/proxy","proxy_dataid":32331,"proxy_token":"token_for_eg"},"validator_config":{"max_future_time_offset":3600,"name":"proxy_validator/common","type":"event","version":"v2"}}},"type":"PLUGIN"}],"subscription_id":987654}`)
	assert.NoError(t, err)
	assert.True(t, equal)
	equal, err = jsonx.CompareJson(crsTs.Config, `{"scope":{"node_type":"INSTANCE","nodes":[{"bk_host_id":8888}],"object_type":"HOST"},"steps":[{"config":{"config_templates":[{"name":"bk-collector-application.conf","version":"latest"}],"plugin_name":"bk-collector","plugin_version":"latest"},"id":"bk-collector","params":{"context":{"bk_biz_id":60,"bk_data_id":32332,"bl_app_name":"prometheus_report","qps_config":{"name":"rate_limiter/token_bucket","qps":20,"type":"token_bucket"}}},"type":"PLUGIN"}]}`)
	assert.NoError(t, err)
	assert.True(t, equal)
}
