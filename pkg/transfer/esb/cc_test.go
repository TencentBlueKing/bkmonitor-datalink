// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb_test

import (
	"encoding/json"
	"testing"

	"github.com/cstockton/go-conv"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/esb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// CCApiClientSuite :
type CCApiClientSuite struct {
	ClientSuite
	apiClient *esb.CCApiClient
}

// SetupTest :
func (s *CCApiClientSuite) SetupTest() {
	s.ClientSuite.SetupTest()
	s.apiClient = esb.NewCCApiClient(s.client)
}

// TestAgent :
func (s *CCApiClientSuite) TestAgent() {
	req, err := s.apiClient.Agent().Request()

	s.NoError(err)
	s.Equal("http://paas.service.consul/api/c/compapi/v2/cc/", req.URL.String())

	// test apigw
	s.conf.Set(esb.ConfESBUseAPIGateway, true)

	req, err = s.apiClient.Agent().Request()
	s.NoError(err)
	s.Equal("http://paas.service.consul/api/bk-cmdb/prod/", req.URL.String())

	s.conf.Set(esb.ConfESBCmdbApiAddress, "http://bk-cmdb.api.com/")
	req, err = s.apiClient.Agent().Request()
	s.NoError(err)
	s.Equal("http://bk-cmdb.api.com/", req.URL.String())
}

// TestGetHostsByRange :
func (s *CCApiClientSuite) TestGetHostsByRange() {
	s.doer.EXPECT().Do(gomock.Any()).Return(
		newJSONResponse(200, `{"result":true,"code":0,"message":"success","data":{"count":1,"info":[{"host":{"bk_asset_id":"DKUXHBUH189","bk_bak_operator":"admin","bk_cloud_id":0,"bk_comment":"","bk_cpu":8,"bk_cpu_mhz":2609,"bk_cpu_module":"E5-2620","bk_disk":300000,"bk_host_id":17,"bk_host_innerip":"127.0.0.1","bk_host_name":"nginx-1","bk_host_outerip":"","bk_isp_name":"1","bk_mac":"","bk_mem":32000,"bk_os_bit":"","create_time":"2019-07-22T01:52:21.737Z","last_time":"2019-07-22T01:52:21.737Z","bk_os_version":"","bk_os_type":"1","bk_service_term":5,"bk_sla":"1","import_from":"1","bk_province_name":"广东","bk_supplier_account":"0","bk_state_name":"CN","bk_outer_mac":"","operator":"admin","bk_sn":""},"topo":[{"bk_set_id":3,"bk_set_name":"job","module":[{"bk_module_id":54,"bk_module_name":"job"}]}]}]}}`),
		nil,
	)
	res, err := s.apiClient.GetHostsByRange(1, 0, 0)
	s.NoError(err)
	s.Equal(res.Info[0].Topo[0].BKSetID, 3)
	s.Equal(res.Info[0].Topo[0].Module[0].BKModuleID, 54)
	s.Equal(res.Info[0].Host.BKCloudID, 0)
	s.Equal(res.Info[0].Host.BKHostInnerIP, "127.0.0.1")
	s.Equal(res.Info[0].Host.BKOuterIP, "")
}

// TestGetHostsByRange :
func (s *CCApiClientSuite) TestGetSearchBusiness() {
	s.doer.EXPECT().Do(gomock.Any()).Return(
		newJSONResponse(200, `{"message":"success","code":0,"data":{"count":10,"info":[{"bk_biz_id":2,"bk_biz_name":"蓝鲸"},{"bk_biz_id":3,"bk_biz_name":"多业务测试"},{"bk_biz_id":4,"bk_biz_name":"日志检索-测试1"},{"bk_biz_id":5,"bk_biz_name":"日志检索-测试2"},{"bk_biz_id":6,"bk_biz_name":"日志检索-测试3"},{"bk_biz_id":7,"bk_biz_name":"bence测试业务"},{"bk_biz_id":8,"bk_biz_name":"bence测试业务2"},{"bk_biz_id":9,"bk_biz_name":"欢乐游戏(demo)"},{"bk_biz_id":10,"bk_biz_name":"shengjie测试业务"},{"bk_biz_id":11,"bk_biz_name":"layman"}]},"result":true,"request_id":"f13f103bce56443493e4dda687a9ea43"}`),
		nil,
	)

	response, err := s.apiClient.GetSearchBusiness()
	s.NoError(err)
	s.NotNil(response)
	for _, value := range response {
		s.Equal(true, utils.IsIntInSlice(conv.Int(value.BKBizID), []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11}))
	}
}

// TestGetHostsByRange :
func (s *CCApiClientSuite) TestGetServiceInstance() {
	s.doer.EXPECT().Do(gomock.Any()).Return(
		newJSONResponse(200, `{"result":true,"bk_error_code":0,"bk_error_msg":"success","permission":null,"data":{"count":1,"info":[{"metadata":{"label":{"bk_biz_id":"2"}},"id":1,"name":"mysql","service_template_id":3,"bk_host_id":1,"bk_module_id":18,"creator":"cc_system","modifier":"cc_system","create_time":"2019-07-09T13:06:54.013+08:00","last_time":"2019-07-09T13:06:54.013+08:00","bk_supplier_account":"0","service_category_id":10,"process_instances":[{"process":{"metadata":{"label":{"bk_biz_id":"2"}},"bk_process_id":110,"bk_func_name":"mysqld","work_path":"/data/bkee","bind_ip":"","bk_process_name":"mysqld","port":"3306","last_time":"2019-07-09T13:06:54.019+08:00","create_time":"2019-07-09T13:06:54.019+08:00","bk_biz_id":2,"protocol":"1","bk_supplier_account":"0"},"relation":{"metadata":{"label":{"bk_biz_id":"2"}},"bk_process_id":110,"service_instance_id":1,"process_template_id":11,"bk_host_id":1,"bk_supplier_account":"0"}},{"process":{"metadata":{"label":{"bk_biz_id":"2"}},"bk_process_id":111,"bk_func_name":"bash","work_path":"","bind_ip":"","bk_process_name":"mysqld_safe","port":"","last_time":"2019-07-09T13:06:54.024+08:00","create_time":"2019-07-09T13:06:54.024+08:00","bk_biz_id":2,"protocol":"","bk_supplier_account":"0","bk_start_param_regex":"mysqld_safe"},"relation":{"metadata":{"label":{"bk_biz_id":"2"}},"bk_process_id":111,"service_instance_id":1,"process_template_id":12,"bk_host_id":1,"bk_supplier_account":"0"}}]}]}}`),
		nil,
	)
	response, err := s.apiClient.GetServiceInstance(1, 1, 1, []int{1})
	s.NoError(err)
	s.NotNil(response)
	s.Equal(18, response.Info[0].BKModuleID)
}

func (s *CCApiClientSuite) TestTopoDataToCmdbLevelV3() {
	s.doer.EXPECT().Do(gomock.Any()).Return(
		newJSONResponse(200, `{"result":true,"code":0,"message":"success","data":[{"bk_inst_id":2,"bk_inst_name":"blueking","bk_obj_id":"biz","bk_obj_name":"business","child":[{"bk_inst_id":3,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"set","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]},{"bk_inst_id":13,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"set","child":[{"bk_inst_id":35,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]},{"bk_inst_id":4,"bk_inst_name":"consumer","bk_obj_id":"consumer","bk_obj_name":"consumer","child":[{"bk_inst_id":13,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"set","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]}]}]},{"bk_inst_id":3,"bk_inst_name":"blueking","bk_obj_id":"biz","bk_obj_name":"business","child":[{"bk_inst_id":3,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"set","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]},{"bk_inst_id":13,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"set","child":[{"bk_inst_id":35,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]},{"bk_inst_id":1,"bk_inst_name":"consumer1","bk_obj_id":"consumer1","bk_obj_name":"consumer1","child":[{"bk_inst_id":2,"bk_inst_name":"consumer2","bk_obj_id":"consumer2","bk_obj_name":"consumer2","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"module","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]}]},{"bk_inst_id":13,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"set","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]}]},{"bk_inst_id":9,"bk_inst_name":"test9","bk_obj_id":"test9","bk_obj_name":"test9","child":[{"bk_inst_id":91,"bk_inst_name":"test91","bk_obj_id":"test91","bk_obj_name":"test91","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"module","child":[{"bk_inst_id":5,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]}]},{"bk_inst_id":19,"bk_inst_name":"job","bk_obj_id":"set","bk_obj_name":"set","child":[{"bk_inst_id":191,"bk_inst_name":"job","bk_obj_id":"module","bk_obj_name":"module","child":[]}]}]}]}]}`),
		nil)
	response, err := s.apiClient.GetSearchBizInstTopo(0, 2, 0, -1)
	s.NoError(err)
	res := make([][]map[string]string, 0)
	for _, value := range response {
		res = append(res, esb.TopoDataToCmdbLevelV3(&value))
	}
	mapHelper := utils.NewMapStringHelper(res[1][0])
	if biz, ok := mapHelper.Get(define.RecordBizIDFieldName); ok {
		s.Equal(biz, 3)
	}
}

func (s *CCApiClientSuite) TestOpenHostResInMonitorAdapter() {
	var hostInfo *esb.CCSearchHostResponseData
	s.NoError(json.Unmarshal([]byte(`{"count":1,"info":[{"host":{"bk_asset_id":"DKUXHBUH189","bk_bak_operator":"admin","bk_cloud_id":0,"bk_comment":"","bk_cpu":8,"bk_cpu_mhz":2609,"bk_cpu_module":"E5-2620","bk_disk":300000,"bk_host_id":17,"bk_host_innerip":"127.0.0.1","bk_host_name":"nginx-1","bk_host_outerip":"","bk_isp_name":"1","bk_mac":"","bk_mem":32000,"bk_os_bit":"","create_time":"2019-07-22T01:52:21.737Z","last_time":"2019-07-22T01:52:21.737Z","bk_os_version":"","bk_os_type":"1","bk_service_term":5,"bk_sla":"1","import_from":"1","bk_province_name":"广东","bk_supplier_account":"0","bk_state_name":"CN","bk_outer_mac":"","operator":"admin","bk_sn":""},"topo":[{"bk_set_id":3,"bk_set_name":"job","module":[{"bk_module_id":54,"bk_module_name":"job"}]}]}]}`), &hostInfo))
	res, _ := esb.OpenHostResInMonitorAdapter(hostInfo, 2)
	s.Equal(2, res.Info[0].BizID)
	s.Equal("54", res.Info[0].Topo[0][define.RecordBkModuleID])
}

func (s *CCApiClientSuite) TestMergeTopoHost() {
	cases := []map[string]string{
		{
			"bk_module_id": "18",
			"bk_set_id":    "5",
			"test":         "2",
		},
		{
			"bk_module_id": "15",
			"bk_set_id":    "5",
			"test":         "2",
		},
		{
			"bk_module_id": "54",
			"bk_set_id":    "5",
			"test":         "1",
			"test2":        "2",
		},
		{
			"test":         "2",
			"bk_module_id": "19",
			"bk_set_id":    "5",
		},
		{
			"bk_module_id": "21",
			"bk_set_id":    "5",
			"test":         "2",
		},
		{
			"test":         "2",
			"bk_module_id": "12",
			"bk_set_id":    "5",
		},
		{
			"bk_module_id": "9",
			"bk_set_id":    "5",
			"test":         "2",
		},
	}

	hostInfo := esb.CCSearchHostResponseData{}
	s.NoError(json.Unmarshal([]byte(`{"count":1,"info":[{"host":{"bk_asset_id":"DKUXHBUH189","bk_bak_operator":"admin","bk_cloud_id":0,"bk_comment":"","bk_cpu":8,"bk_cpu_mhz":2609,"bk_cpu_module":"E5-2620","bk_disk":300000,"bk_host_id":17,"bk_host_innerip":"127.0.0.1","bk_host_name":"nginx-1","bk_host_outerip":"","bk_isp_name":"1","bk_mac":"","bk_mem":32000,"bk_os_bit":"","create_time":"2019-07-22T01:52:21.737Z","last_time":"2019-07-22T01:52:21.737Z","bk_os_version":"","bk_os_type":"1","bk_service_term":5,"bk_sla":"1","import_from":"1","bk_province_name":"广东","bk_supplier_account":"0","bk_state_name":"CN","bk_outer_mac":"","operator":"admin","bk_sn":""},"topo":[{"bk_set_id":3,"bk_set_name":"job","module":[{"bk_module_id":54,"bk_module_name":"job"}]}]}]}`), &hostInfo))
	res, _ := esb.OpenHostResInMonitorAdapter(&hostInfo, 2)
	esb.MergeTopoHost(res, cases)
	s.Equal(res.Info[0].Topo[0]["test"], "1")
	s.Equal(res.Info[0].Topo[0]["test2"], "2")
}

// TestCCApiClientSuite :
func TestCCApiClientSuite(t *testing.T) {
	suite.Run(t, new(CCApiClientSuite))
}
