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

type GetHostByIpParams struct {
	Ip        string
	BkCloudId int
}

func (CMDBService) processGetHostByIpParams(bkBizId int, ips []GetHostByIpParams) map[string]interface{} {
	var cloudDict = make(map[int][]string)
	for _, param := range ips {
		if ls, ok := cloudDict[param.BkCloudId]; ok {
			cloudDict[param.BkCloudId] = append(ls, param.Ip)
		} else {
			cloudDict[param.BkCloudId] = []string{param.Ip}
		}
	}
	conditions := []map[string]interface{}{}
	for cloudId, ipList := range cloudDict {
		ipv6IPs := []string{}
		ipv4IPs := []string{}
		for _, ip := range ipList {
			if IsIPv6(ip) {
				ipv6IPs = append(ipv6IPs, ip)
			} else {
				ipv4IPs = append(ipv4IPs, ip)
			}
		}
		ipv4Rules := []map[string]interface{}{
			{"field": "bk_host_innerip", "operator": "in", "value": ipv4IPs},
		}

		ipv6Rules := []map[string]interface{}{
			{"field": "bk_host_innerip_v6", "operator": "in", "value": ipv6IPs},
		}

		if cloudId != -1 {
			ipv4Rules = append(ipv4Rules, map[string]interface{}{"field": "bk_cloud_id", "operator": "equal", "value": cloudId})
			ipv6Rules = append(ipv6Rules, map[string]interface{}{"field": "bk_cloud_id", "operator": "equal", "value": cloudId})
		}

		ipv4Condition := map[string]interface{}{
			"condition": "AND",
			"rules":     ipv4Rules,
		}
		ipv6Condition := map[string]interface{}{
			"condition": "AND",
			"rules":     ipv6Rules,
		}

		if len(ipv4IPs) > 0 {
			conditions = append(conditions, ipv4Condition)
		}
		if len(ipv6IPs) > 0 {
			conditions = append(conditions, ipv6Condition)
		}
	}

	var finalCondition interface{}

	if len(conditions) == 1 {
		finalCondition = conditions[0]
	} else {
		finalCondition = map[string]interface{}{
			"condition": "OR",
			"rules":     conditions,
		}
	}

	return map[string]interface{}{
		"bk_biz_id":            bkBizId,
		"host_property_filter": finalCondition,
		"fields": []string{"bk_host_innerip",
			"bk_host_innerip_v6",
			"bk_cloud_id",
			"bk_host_id",
			"bk_biz_id",
			"bk_agent_id",
			"bk_host_outerip",
			"bk_host_outerip_v6",
			"bk_host_name",
			"bk_os_name",
			"bk_os_type",
			"operator",
			"bk_bak_operator",
			"bk_state_name",
			"bk_isp_name",
			"bk_province_name",
			"bk_supplier_account",
			"bk_state",
			"bk_os_version",
			"service_template_id",
			"srv_status",
			"bk_comment",
			"idc_unit_name",
			"net_device_id",
			"rack_id",
			"bk_svr_device_cls_name",
			"svr_device_class"},
		"page": map[string]int{
			"limit": 500,
		},
	}
}

// GetHostByIp 通过IP查询主机信息
func (s CMDBService) GetHostByIp(ipList []GetHostByIpParams, BkBizId int) ([]cmdb.ListBizHostsTopoDataInfo, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, err
	}
	params := s.processGetHostByIpParams(BkBizId, ipList)
	var topoResp cmdb.ListBizHostsTopoResp
	_, err = cmdbApi.ListBizHostsTopo().SetBody(params).SetResult(&topoResp).Request()
	if err != nil {
		return nil, err
	}
	return topoResp.Data.Info, nil
}

// GetHostWithoutBiz 通过IP跨业务查询主机信息
func (s CMDBService) GetHostWithoutBiz(ips []string) ([]cmdb.ListHostsWithoutBizDataInfo, error) {
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
		if len(ipv4List) != 0 {
			filterRules = append(filterRules, map[string]interface{}{"field": "bk_host_innerip", "operator": "in", "value": ipv4List})
		}
		if len(ipv6List) != 0 {
			filterRules = append(filterRules, map[string]interface{}{"field": "bk_host_innerip_v6", "operator": "in", "value": ipv6List})
		}
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
