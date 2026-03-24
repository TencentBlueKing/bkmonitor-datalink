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

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var CMDB CMDBService

type CMDBService struct{}

type GetHostByIpParams struct {
	Ip        string
	BkCloudId int
}

func (CMDBService) processGetHostByIpParams(bkBizId int, ips []GetHostByIpParams) map[string]any {
	cloudDict := make(map[int][]string)
	for _, param := range ips {
		if ls, ok := cloudDict[param.BkCloudId]; ok {
			cloudDict[param.BkCloudId] = append(ls, param.Ip)
		} else {
			cloudDict[param.BkCloudId] = []string{param.Ip}
		}
	}
	conditions := []map[string]any{}
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
		ipv4Rules := []map[string]any{
			{"field": "bk_host_innerip", "operator": "in", "value": ipv4IPs},
		}

		ipv6Rules := []map[string]any{
			{"field": "bk_host_innerip_v6", "operator": "in", "value": ipv6IPs},
		}

		if cloudId != -1 {
			ipv4Rules = append(ipv4Rules, map[string]any{"field": "bk_cloud_id", "operator": "equal", "value": cloudId})
			ipv6Rules = append(ipv6Rules, map[string]any{"field": "bk_cloud_id", "operator": "equal", "value": cloudId})
		}

		ipv4Condition := map[string]any{
			"condition": "AND",
			"rules":     ipv4Rules,
		}
		ipv6Condition := map[string]any{
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

	var finalCondition any

	if len(conditions) == 1 {
		finalCondition = conditions[0]
	} else {
		finalCondition = map[string]any{
			"condition": "OR",
			"rules":     conditions,
		}
	}

	return map[string]any{
		"bk_biz_id":            bkBizId,
		"host_property_filter": finalCondition,
		"fields": []string{
			"bk_host_innerip",
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
			"svr_device_class",
		},
		"page": map[string]int{
			"limit": 500,
		},
	}
}

// GetHostByIp 通过IP查询主机信息
func (s CMDBService) GetHostByIp(ipList []GetHostByIpParams, bkBizId int) ([]cmdb.ListBizHostsTopoDataInfo, error) {
	tenantId, err := tenant.GetTenantIdByBkBizId(bkBizId)
	if err != nil {
		return nil, errors.Wrapf(err, "GetTenantIdByBkBizId failed, BkBizID: %d", bkBizId)
	}
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		return nil, errors.Wrapf(err, "GetCmdbApi failed, BkBizID: %d", bkBizId)
	}
	params := s.processGetHostByIpParams(bkBizId, ipList)
	var topoResp cmdb.ListBizHostsTopoResp
	if _, err = cmdbApi.ListBizHostsTopo().SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bkBizId)}).SetBody(params).SetResult(&topoResp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "ListBizHostsTopo with params [%s] failed", paramStr)
	}
	if err := topoResp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "ListBizHostsTopo with params [%s] failed", paramStr)
	}
	return topoResp.Data.Info, nil
}

// GetHostWithoutBiz 通过IP跨业务查询主机信息
func (s CMDBService) GetHostWithoutBiz(ips []string, bkCloudIds []int) ([]cmdb.ListHostsWithoutBizDataInfo, error) {
	// todo: tenant
	cmdbApi, err := api.GetCmdbApi(tenant.DefaultTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "GetCmdbApi failed")
	}
	var filterRules []map[string]any
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
		filterRules = append(filterRules, map[string]any{"field": "bk_host_innerip", "operator": "in", "value": ipv4List})
	}
	if len(ipv6List) != 0 {
		filterRules = append(filterRules, map[string]any{"field": "bk_host_innerip_v6", "operator": "in", "value": ipv6List})
	}
	if len(bkCloudIds) != 0 {
		filterRules = append(filterRules, map[string]any{"field": "bk_cloud_id", "operator": "in", "value": bkCloudIds})
	}
	params := map[string]any{"fields": nil}
	if len(filterRules) != 0 {
		params["host_property_filter"] = map[string]any{"condition": "AND", "rules": filterRules}
	}
	var resp cmdb.ListHostsWithoutBizResp
	if _, err = cmdbApi.ListHostsWithoutBiz().SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "ListHostsWithoutBizResp with body [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "ListHostsWithoutBizResp with params [%s] failed", paramStr)
	}
	return resp.Data.Info, nil
}

func (s CMDBService) SearchCloudArea() ([]cmdb.SearchCloudAreaDataInfo, error) {
	// todo: tenant
	cmdbApi, err := api.GetCmdbApi(tenant.DefaultTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "GetCmdbApi failed")
	}
	var resp cmdb.SearchCloudAreaResp
	_, err = cmdbApi.SearchCloudArea().SetBody(map[string]any{"page": map[string]int{"start": 0, "limit": 1000}}).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrap(err, "SearchCloudArea failed")
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrap(err, "SearchCloudArea failed")
	}
	return resp.Data.Info, nil
}

// FindHostBizRelationMap 查询主机业务关系信息
func (s CMDBService) FindHostBizRelationMap(bkHostIds []int) (map[int]int, error) {
	// 获取到所有业务id
	// todo: tenant
	cmdbApi, err := api.GetCmdbApi(tenant.DefaultTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "GetCmdbApi failed")
	}
	var bizResp cmdb.FindHostBizRelationResp
	params := map[string]any{"bk_host_id": bkHostIds}
	if _, err := cmdbApi.FindHostBizRelation().SetBody(params).SetResult(&bizResp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "FindHostBizRelation with params [%s] failed", paramStr)
	}
	if err := bizResp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "FindHostBizRelation with params [%s] failed", paramStr)
	}
	result := make(map[int]int)
	for _, r := range bizResp.Data {
		result[r.BkHostId] = r.BkBizId
	}
	return result, nil
}

// GetAllHost 获取所有主机信息
func (s CMDBService) GetAllHost() ([]Host, error) {
	// 获取到所有业务id
	// todo: tenant
	cmdbApi, err := api.GetCmdbApi(tenant.DefaultTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "GetCmdbApi failed")
	}
	var bizResp cmdb.SearchBusinessResp
	if _, err := cmdbApi.SearchBusiness().SetPathParams(map[string]string{"bk_supplier_account": "0"}).SetResult(&bizResp).Request(); err != nil {
		return nil, errors.Wrap(err, "SearchBusinessResp failed")
	}
	if err := bizResp.Err(); err != nil {
		return nil, errors.Wrapf(err, "SearchBusinessResp failed")
	}

	fields := []string{
		"bk_host_innerip",
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
		"svr_device_class",
		"version_meta",
	}
	var hostInfoList []Host
	for _, info := range bizResp.Data.Info {
		params := map[string]any{
			"bk_biz_id": info.BkBizId,
			"fields":    fields,
			"page": map[string]int{
				"limit": 500,
			},
		}
		var topoResp cmdb.ListBizHostsTopoResp
		_, err = cmdbApi.ListBizHostsTopo().SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(info.BkBizId)}).SetBody(params).SetResult(&topoResp).Request()
		if err != nil {
			logger.Errorf("ListBizHostsTopo with bk_biz_id [%v] failed, %v", info.BkBizId, err)
			continue
		}
		if err := topoResp.Err(); err != nil {
			paramStr, _ := jsonx.MarshalString(topoResp)
			logger.Errorf("ListBizHostsTopo with params [%s] failed, %v", paramStr, err)
			continue
		}
		for _, topoInfo := range topoResp.Data.Info {
			hostInfoList = append(hostInfoList, Host{
				ListBizHostsTopoDataInfoHost: topoInfo.Host,
				BkBizId:                      info.BkBizId,
			})
		}

	}
	return hostInfoList, nil
}

type Host struct {
	cmdb.ListBizHostsTopoDataInfoHost
	BkBizId int `json:"bk_biz_id"`
}

// IgnoreMonitorByStatus 根据状态判断是否忽略监控
func (h Host) IgnoreMonitorByStatus() bool {
	return slicex.IsExistItem(cfg.GlobalHostDisableMonitorStates, *h.BkState)
}

// IsIPV6Biz 所属业务是否是ipv6业务
func (h Host) IsIPV6Biz() bool {
	return slicex.IsExistItem(cfg.GlobalIPV6SupportBizList, h.BkBizId)
}
