// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apiservice

import (
	"strconv"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var Nodeman NodemanService

type NodemanService struct{}

// GetProxies 【节点管理2.0】查询云区域下的proxy列表
func (s NodemanService) GetProxies(bkCloudId int) ([]nodeman.ProxyData, error) {
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "nodemanApi failed")
	}
	params := map[string]string{"bk_cloud_id": strconv.Itoa(bkCloudId)}
	var resp nodeman.GetProxiesResp
	if _, err := nodemanApi.GetProxies().SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "GetProxiesResp with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "GetProxiesResp with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// PluginInfo 查询插件列表
func (s NodemanService) PluginInfo(pluginName, version string) ([]nodeman.PluginInfoData, error) {
	if pluginName == "" {
		return nil, nil
	}
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "nodemanApi failed")
	}
	var resp nodeman.PluginInfoResp
	params := map[string]string{"name": pluginName}
	if version != "" {
		params["version"] = version
	}
	if _, err := nodemanApi.PluginInfo().SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "get PluginInfo with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "get PluginInfo params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// PluginSearch 【节点管理2.0】插件查询接口
func (s NodemanService) PluginSearch(bkBizIds, bkHostIds, excludeHosts []int, conditions []interface{}) ([]nodeman.PluginSearchDataItem, error) {
	if bkBizIds == nil {
		bkBizIds = []int{}
	}
	if excludeHosts == nil {
		excludeHosts = []int{}
	}
	if bkHostIds == nil {
		bkHostIds = []int{}
	}

	if conditions == nil {
		conditions = []interface{}{}
	}
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "GetNodemanApi failed")
	}
	var resp nodeman.PluginSearchResp
	_, err = nodemanApi.PluginSearch().SetBody(map[string]interface{}{
		"page":          1,
		"pagesize":      len(bkHostIds),
		"conditions":    conditions,
		"bk_host_id":    bkHostIds,
		"bk_biz_id":     bkBizIds,
		"exclude_hosts": excludeHosts,
	}).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "PluginSearch with bk_host_id [%v] bk_biz_id [%v] exclude_hosts [%v] conditions [%v] failed", bkHostIds, bkBizIds, excludeHosts, conditions)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "PluginSearch with bk_host_id [%v] bk_biz_id [%v] exclude_hosts [%v] conditions [%v] failed", bkHostIds, bkBizIds, excludeHosts, conditions)
	}
	return resp.Data.List, nil
}

// PluginOperate 【节点管理2.0】插件管理接口
func (s NodemanService) PluginOperate(params map[string]interface{}) (interface{}, error) {
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "GetNodemanApi failed")
	}
	var resp define.APICommonResp
	_, err = nodemanApi.PluginOperate().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "PluginSearch with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "PluginSearch with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// UpdateSubscription 更新订阅
func (s NodemanService) UpdateSubscription(params map[string]interface{}) (*define.APICommonResp, error) {
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "nodemanApi failed")
	}
	var resp define.APICommonResp
	_, err = nodemanApi.UpdateSubscription().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "UpdateSubscription with body [%v] failed", params)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "UpdateSubscription with body [%v] failed", params)
	}
	return &resp, nil
}

// CreateSubscription 创建订阅
func (s NodemanService) CreateSubscription(params map[string]interface{}) (*define.APICommonMapResp, error) {
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "nodemanApi failed")
	}
	var resp define.APICommonMapResp
	_, err = nodemanApi.CreateSubscription().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "CreateSubscription with body [%v] failed", params)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "CreateSubscription with body [%v] failed", params)
	}
	return &resp, nil
}

// RunSubscription 运行订阅
func (s NodemanService) RunSubscription(params map[string]interface{}) (*define.APICommonMapResp, error) {
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "nodemanApi failed")
	}
	var resp define.APICommonMapResp
	_, err = nodemanApi.RunSubscription().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "RunSubscription with body [%v] failed", params)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "RunSubscription with body [%v] failed", params)
	}
	return &resp, nil
}
