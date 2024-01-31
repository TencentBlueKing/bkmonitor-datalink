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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

var CMDB CMDBService

type CMDBService struct{}

// GetHostWithoutBiz 通过IP跨业务查询主机信息
func (s CMDBService) GetHostWithoutBiz(ips []string, bkCloudIds []int) ([]cmdb.ListHostsWithoutBizDataInfo, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, err
	}
	var filterRules []map[string]interface{}
	var ipv4List []string
	var ipv6List []string
	for _, ip := range ips {
		if IsIPv6(ip) {
			ipv6List = append(ipv6List, ip)
		} else {
			ipv4List = append(ipv4List, ip)
		}
	}
	if len(ipv4List) != 0 {
		filterRules = append(filterRules, map[string]interface{}{"field": "bk_host_innerip", "operator": "in", "value": ipv4List})
	}
	if len(ipv6List) != 0 {
		filterRules = append(filterRules, map[string]interface{}{"field": "bk_host_innerip_v6", "operator": "in", "value": ipv6List})
	}
	if len(bkCloudIds) != 0 {
		filterRules = append(filterRules, map[string]interface{}{"field": "bk_cloud_id", "operator": "in", "value": bkCloudIds})
	}
	var params = map[string]interface{}{"fields": nil}
	if len(filterRules) != 0 {
		params["host_property_filter"] = map[string]interface{}{"condition": "AND", "rules": filterRules}
	}
	var resp cmdb.ListHostsWithoutBizResp
	_, err = cmdbApi.ListHostsWithoutBiz().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "ListHostsWithoutBizResp with body [%v] failed", params)
	}
	return resp.Data.Info, nil
}

func (s CMDBService) SearchCloudArea() ([]cmdb.SearchCloudAreaDataInfo, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, errors.Wrap(err, "GetCmdbApi failed")
	}
	var resp cmdb.SearchCloudAreaResp
	_, err = cmdbApi.SearchCloudArea().SetBody(map[string]interface{}{"page": map[string]int{"start": 0, "limit": 1000}}).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrap(err, "SearchCloudArea failed")
	}
	return resp.Data.Info, nil
}
