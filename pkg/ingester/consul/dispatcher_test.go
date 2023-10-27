// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"encoding/json"
	"fmt"
	"testing"

	consulApi "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

func TestDispatcher(t *testing.T) {
	dataSources := []*define.DataSource{
		{
			Option: define.Plugin{
				PluginID:   "1001-test",
				PluginType: "http_pull",
			},
			DataID: 1001,
		},
		{
			Option: define.Plugin{
				PluginID:   "1002-test",
				PluginType: "http_pull",
			},
			DataID: 1002,
		},
		{
			Option: define.Plugin{
				PluginID:   "1003-test",
				PluginType: "http_pull",
			},
			DataID: 1003,
		},
		{
			Option: define.Plugin{
				PluginID:   "1004-test",
				PluginType: "http_push",
			},
			DataID: 1004,
		},
		{
			Option: define.Plugin{
				PluginID:   "1005-test",
				PluginType: "http_push",
				BusinessID: "2",
			},
			DataID: 1005,
		},
	}

	dispatcher := &consul.Dispatcher{}

	dispatcher.Services = []*define.ServiceInfo{
		{
			ID: "service-0",
		},
		{
			ID: "service-1",
		},
	}

	for _, index := range []int{0, 1, 2, 3} {
		ds := dataSources[index]
		value, _ := json.Marshal(ds)
		dispatcher.DataSources = append(dispatcher.DataSources, &define.DataSourceKVPair{
			Pair: &consulApi.KVPair{
				Key:         fmt.Sprintf("bkmonitor/metadata/v1/default/data_id/%d", dataSources[index].DataID),
				Value:       value,
				ModifyIndex: 2,
			},
			DataSource: ds,
		})
	}

	for i, index := range []int{0, 2, 4} {
		ds := dataSources[index]
		value, _ := json.Marshal(ds)
		dispatcher.DispatchedDataSources = append(dispatcher.DispatchedDataSources, &define.DataSourceKVPair{
			Pair: &consulApi.KVPair{
				Key:         fmt.Sprintf("bkmonitor/ingester/data_id/service-%d/%d", i, dataSources[index].DataID),
				Value:       value,
				ModifyIndex: 2,
			},
			DataSource: ds,
		})
	}

	plan := dispatcher.GetPlan()

	assert.Equal(t, 2, len(plan))
	assert.NotNil(t, plan["service-0"])
	assert.NotNil(t, plan["service-1"])
	assert.Equal(t, 2, len(plan["service-0"]))
	assert.Equal(t, 3, len(plan["service-1"]))
	assert.Equal(t, 1002, plan["service-0"][0].DataSource.DataID)
	assert.Equal(t, 1004, plan["service-0"][1].DataSource.DataID)
	assert.Equal(t, 1001, plan["service-1"][0].DataSource.DataID)
	assert.Equal(t, 1003, plan["service-1"][1].DataSource.DataID)
	assert.Equal(t, 1004, plan["service-1"][2].DataSource.DataID)

	oldPlan := dispatcher.GetOldPlan()

	assert.Equal(t, 3, len(oldPlan))
	assert.Equal(t, 1, len(oldPlan["service-0"]))
	assert.Equal(t, 1, len(oldPlan["service-1"]))
	assert.Equal(t, 1, len(oldPlan["service-2"]))

	planToAdd, planToDelete := dispatcher.DiffPlan()

	assert.Equal(t, 2, len(planToAdd))
	assert.Equal(t, 2, len(planToAdd["service-0"]))
	assert.Equal(t, 2, len(planToAdd["service-1"]))
	assert.Equal(t, 0, len(planToAdd["service-2"]))

	assert.Equal(t, 2, len(planToDelete))
	assert.Equal(t, 1, len(planToDelete["service-0"]))
	assert.Equal(t, 1, len(planToDelete["service-2"]))

	//assert.Equal(t, consul.ServiceDispatchPlan{
	//	"service-0": []*define.DataSourceKVPair{dispatcher.DataSources[0], dispatcher.DataSources[1]},
	//	"service-1": []*define.DataSourceKVPair{dispatcher.DataSources[2]},
	//}, plan)
}

func TestParseDataSource(t *testing.T) {
	plugin := &define.Plugin{}
	plugin_option := map[string]interface{}{
		"plugin_id":   "test_pull",
		"plugin_type": "http_pull",
		"bk_biz_id":   123,
	}
	err := utils.ConvertByJSON(plugin_option, plugin)
	assert.EqualValues(t, nil, err)
	assert.EqualValues(t, 123, plugin.BusinessID)

	plugin_option["bk_biz_id"] = "123"
	err = utils.ConvertByJSON(plugin_option, plugin)
	assert.EqualValues(t, nil, err)
	assert.EqualValues(t, "123", plugin.BusinessID)

	global_plugin_option := map[string]interface{}{
		"plugin_id":   "test_pull",
		"plugin_type": "http_pull",
	}
	fmt.Printf("global_plugin_option %v", global_plugin_option)

	global_plugin := &define.Plugin{}
	utils.ConvertByJSON(global_plugin_option, global_plugin)
	assert.EqualValues(t, nil, global_plugin.BusinessID)
	fmt.Printf("global_plugin %v \n", global_plugin)
}
