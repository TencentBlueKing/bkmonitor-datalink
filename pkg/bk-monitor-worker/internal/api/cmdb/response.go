// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

type SearchCloudAreaResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     SearchCloudAreaData `json:"data"`
}

type SearchCloudAreaData struct {
	Count int                       `json:"count"`
	Info  []SearchCloudAreaDataInfo `json:"info"`
}

type SearchCloudAreaDataInfo struct {
	BkAccountId       *int      `json:"bk_account_id"`
	BkCloudId         int       `json:"bk_cloud_id"`
	BkCloudName       string    `json:"bk_cloud_name"`
	BkCloudVendor     *string   `json:"bk_cloud_vendor"`
	BkCreator         string    `json:"bk_creator"`
	BkLastEditor      string    `json:"bk_last_editor"`
	BkRegion          string    `json:"bk_region"`
	BkStatus          string    `json:"bk_status"`
	BkStatusDetail    string    `json:"bk_status_detail"`
	BkSupplierAccount string    `json:"bk_supplier_account"`
	BkVpcId           string    `json:"bk_vpc_id"`
	BkVpcName         string    `json:"bk_vpc_name"`
	CreateTime        time.Time `json:"create_time"`
	LastTime          time.Time `json:"last_time"`
}

type ListBizHostsTopoResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     ListBizHostsTopoData `json:"data" mapstructure:"data"`
}

type ListBizHostsTopoData struct {
	Count int                        `json:"count" mapstructure:"count"`
	Info  []ListBizHostsTopoDataInfo `json:"info" mapstructure:"info"`
}

type ListBizHostsTopoDataInfoHost struct {
	BkBizId             int     `json:"bk_biz_id" mapstructure:"bk_biz_id"`
	BkSupplierAccount   string  `json:"bk_supplier_account" mapstructure:"bk_supplier_account"`
	BkAgentId           string  `json:"bk_agent_id" mapstructure:"bk_agent_id"`
	Operator            string  `json:"operator" mapstructure:"operator"`
	BkBakOperator       string  `json:"bk_bak_operator" mapstructure:"bk_bak_operator"`
	BkCloudId           int     `json:"bk_cloud_id" mapstructure:"bk_cloud_id"`
	BkComment           string  `json:"bk_comment" mapstructure:"bk_comment"`
	BkHostId            int     `json:"bk_host_id" mapstructure:"bk_host_id"`
	BkHostInnerip       string  `json:"bk_host_innerip" mapstructure:"bk_host_innerip"`
	BkHostInneripV6     string  `json:"bk_host_innerip_v6" mapstructure:"bk_host_innerip_v6"`
	BkHostName          string  `json:"bk_host_name" mapstructure:"bk_host_name"`
	BkHostOuterip       string  `json:"bk_host_outerip" mapstructure:"bk_host_outerip"`
	BkHostOuteripV6     string  `json:"bk_host_outerip_v6" mapstructure:"bk_host_outerip_v6"`
	BkOsName            string  `json:"bk_os_name" mapstructure:"bk_os_name"`
	BkOsType            string  `json:"bk_os_type" mapstructure:"bk_os_type"`
	BkOsVersion         string  `json:"bk_os_version" mapstructure:"bk_os_version"`
	BkOsBit             string  `json:"bk_os_bit" mapstructure:"bk_os_bit"`
	BkProvinceName      *string `json:"bk_province_name" mapstructure:"bk_province_name"`
	BkState             *string `json:"bk_state" mapstructure:"bk_state"`
	BkStateName         *string `json:"bk_state_name" mapstructure:"bk_state_name"`
	BkIspName           *string `json:"bk_isp_name" mapstructure:"bk_isp_name"`
	BkMem               *int    `json:"bk_mem" mapstructure:"bk_mem"`
	BkDisk              *int    `json:"bk_disk" mapstructure:"bk_disk"`
	BkCpu               *int    `json:"bk_cpu" mapstructure:"bk_cpu"`
	BkCpuModule         string  `json:"bk_cpu_module" mapstructure:"bk_cpu_module"`
	SrvStatus           *string `json:"srv_status" mapstructure:"srv_status"`
	IdcUnitName         string  `json:"idc_unit_name" mapstructure:"idc_unit_name"`
	NetDeviceId         string  `json:"net_device_id" mapstructure:"net_device_id"`
	RackId              string  `json:"rack_id" mapstructure:"rack_id"`
	BkSvrDeviceClsName  string  `json:"bk_svr_device_cls_name" mapstructure:"bk_svr_device_cls_name"`
	SvrDeviceClass      string  `json:"svr_device_class" mapstructure:"svr_device_class"`
	DockerClientVersion string  `json:"docker_client_version" mapstructure:"docker_client_version"`
	DockerServerVersion string  `json:"docker_server_version" mapstructure:"docker_server_version"`
	VersionMeta         string  `json:"version_meta"  mapstructure:"version_meta"`
}

type ListBizHostsTopoDataInfoTopo struct {
	BkSetId   int    `json:"bk_set_id" mapstructure:"bk_set_id"`
	BkSetName string `json:"bk_set_name" mapstructure:"bk_set_name"`
	Module    []struct {
		BkModuleId   int    `json:"bk_module_id" mapstructure:"bk_module_id"`
		BkModuleName string `json:"bk_module_name" mapstructure:"bk_module_name"`
	} `json:"module" mapstructure:"module"`
}

type ListBizHostsTopoDataInfo struct {
	Host ListBizHostsTopoDataInfoHost   `json:"host" mapstructure:"host"`
	Topo []ListBizHostsTopoDataInfoTopo `json:"topo" mapstructure:"topo"`
}

type SearchBusinessResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     BusinessData `json:"data" mapstructure:"data"`
}

type BusinessData struct {
	Count int                `json:"count" mapstructure:"count"`
	Info  []BusinessDataInfo `json:"info" mapstructure:"info"`
}

type BusinessDataInfo struct {
	BkTenantId        string    `json:"bk_tenant_id" mapstructure:"bk_tenant_id"`
	BkBizDeveloper    string    `json:"bk_biz_developer" mapstructure:"bk_biz_developer"`
	BkBizId           int       `json:"bk_biz_id" mapstructure:"bk_biz_id"`
	BkBizMaintainer   string    `json:"bk_biz_maintainer" mapstructure:"bk_biz_maintainer"`
	BkBizName         string    `json:"bk_biz_name" mapstructure:"bk_biz_name"`
	BkBizProductor    string    `json:"bk_biz_productor" mapstructure:"bk_biz_productor"`
	BkBizTester       string    `json:"bk_biz_tester" mapstructure:"bk_biz_tester"`
	BkSupplierAccount string    `json:"bk_supplier_account" mapstructure:"bk_supplier_account"`
	CreateTime        time.Time `json:"create_time" mapstructure:"create_time"`
	DbAppAbbr         string    `json:"db_app_abbr,omitempty" mapstructure:"db_app_abbr,omitempty"`
	Default           int       `json:"default" mapstructure:"default"`
	Language          string    `json:"language" mapstructure:"language"`
	LastTime          time.Time `json:"last_time" mapstructure:"last_time"`
	LifeCycle         string    `json:"life_cycle" mapstructure:"life_cycle"`
	Operator          string    `json:"operator" mapstructure:"operator"`
	TimeZone          string    `json:"time_zone" mapstructure:"time_zone"`
}

type ListHostsWithoutBizResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     ListHostsWithoutBizData `json:"data"`
}

type ListHostsWithoutBizData struct {
	Count int                           `json:"count"`
	Info  []ListHostsWithoutBizDataInfo `json:"info"`
}

type ListHostsWithoutBizDataInfo struct {
	BkAddressing          string      `json:"bk_addressing"`
	BkAgentId             string      `json:"bk_agent_id"`
	BkAssetId             string      `json:"bk_asset_id"`
	BkBakOperator         string      `json:"bk_bak_operator"`
	BkCloudHostIdentifier bool        `json:"bk_cloud_host_identifier"`
	BkCloudHostStatus     interface{} `json:"bk_cloud_host_status"`
	BkCloudId             int         `json:"bk_cloud_id"`
	BkCloudInstId         string      `json:"bk_cloud_inst_id"`
	BkCloudVendor         interface{} `json:"bk_cloud_vendor"`
	BkComment             string      `json:"bk_comment"`
	BkCpu                 *int        `json:"bk_cpu"`
	BkCpuArchitecture     string      `json:"bk_cpu_architecture"`
	BkCpuModule           string      `json:"bk_cpu_module"`
	BkDisk                *int        `json:"bk_disk"`
	BkHostId              int         `json:"bk_host_id"`
	BkHostInnerip         string      `json:"bk_host_innerip"`
	BkHostInneripV6       string      `json:"bk_host_innerip_v6"`
	BkHostName            string      `json:"bk_host_name"`
	BkHostOuterip         string      `json:"bk_host_outerip"`
	BkHostOuteripV6       string      `json:"bk_host_outerip_v6"`
	BkIspName             interface{} `json:"bk_isp_name"`
	BkMac                 string      `json:"bk_mac"`
	BkMem                 *int        `json:"bk_mem"`
	BkOsBit               string      `json:"bk_os_bit"`
	BkOsName              string      `json:"bk_os_name"`
	BkOsType              *string     `json:"bk_os_type"`
	BkOsVersion           string      `json:"bk_os_version"`
	BkOuterMac            string      `json:"bk_outer_mac"`
	BkProvinceName        interface{} `json:"bk_province_name"`
	BkServiceTerm         interface{} `json:"bk_service_term"`
	BkSla                 interface{} `json:"bk_sla"`
	BkSn                  string      `json:"bk_sn"`
	BkState               interface{} `json:"bk_state"`
	BkStateName           *string     `json:"bk_state_name"`
	BkSupplierAccount     string      `json:"bk_supplier_account"`
	BkUpdatedAt           time.Time   `json:"bk_updated_at,omitempty"`
	BkUpdatedBy           string      `json:"bk_updated_by,omitempty"`
	CreateTime            time.Time   `json:"create_time"`
	ImportFrom            *string     `json:"import_from"`
	LastTime              time.Time   `json:"last_time"`
	Operator              string      `json:"operator"`
	DbmMeta               string      `json:"dbm_meta,omitempty"`
	BkCreatedAt           time.Time   `json:"bk_created_at,omitempty"`
	BkCreatedBy           string      `json:"bk_created_by,omitempty"`
}

type FindHostBizRelationResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     []FindHostBizRelationData `json:"data"`
}

type FindHostBizRelationData struct {
	BkBizId           int    `json:"bk_biz_id"`
	BkHostId          int    `json:"bk_host_id"`
	BkModuleId        int    `json:"bk_module_id"`
	BkSetId           int    `json:"bk_set_id"`
	BkSupplierAccount string `json:"bk_supplier_account"`
}

// SearchBizInstTopoResp 查询业务拓扑返回
type SearchBizInstTopoResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     []SearchBizInstTopoData `json:"data"`
}

// SearchBizInstTopoData 查询业务拓扑数据
type SearchBizInstTopoData struct {
	BkInstId   int                     `json:"bk_inst_id"`
	BkInstName string                  `json:"bk_inst_name"`
	BkObjId    string                  `json:"bk_obj_id"`
	BkObjName  string                  `json:"bk_obj_name"`
	Child      []SearchBizInstTopoData `json:"child"`
}

// Traverse 递归遍历
func (s *SearchBizInstTopoData) Traverse(fn func(*SearchBizInstTopoData)) {
	fn(s)
	for _, child := range s.Child {
		child.Traverse(fn)
	}
}

// ToTopoLinks 递归获取模块ID到拓扑链路的映射
func (s *SearchBizInstTopoData) ToTopoLinks(result *map[int][]map[string]interface{}, parents []map[string]interface{}) {
	parents = append(parents, map[string]interface{}{
		"bk_inst_id":   s.BkInstId,
		"bk_inst_name": s.BkInstName,
		"bk_obj_id":    s.BkObjId,
		"bk_obj_name":  s.BkObjName,
	})

	// 如果是模块，记录链路
	if s.BkObjId == "module" {
		reverseParents := make([]map[string]interface{}, len(parents))
		for i, p := range parents {
			reverseParents[len(parents)-i-1] = p
		}
		(*result)[s.BkInstId] = reverseParents
		return
	}

	// 递归子节点
	for _, child := range s.Child {
		child.ToTopoLinks(result, parents)
	}
}

// GetId 获取唯一标识
func (s *SearchBizInstTopoData) GetId() string {
	topoId := fmt.Sprintf("%s|%d", s.BkObjId, s.BkInstId)
	return topoId
}

// GetBizInternalModuleResp 查询业务内部模块返回
type GetBizInternalModuleResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     GetBizInternalModuleData `json:"data"`
}

// GetBizInternalModuleData 查询业务内部模块数据
type GetBizInternalModuleData struct {
	BkSetId   int    `json:"bk_set_id"`
	BkSetName string `json:"bk_set_name"`
	Module    []struct {
		BkModuleId   int    `json:"bk_module_id"`
		BkModuleName string `json:"bk_module_name"`
	}
}

// SearchObjectAttributeResp 查询对象属性返回
type SearchObjectAttributeResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     []SearchObjectAttributeData `json:"data"`
}

// SearchObjectAttributeData 查询对象属性数据
type SearchObjectAttributeData struct {
	BkObjId        string `json:"bk_obj_id"`
	BkPropertyId   string `json:"bk_property_id"`
	BkPropertyName string `json:"bk_property_name"`
	BkPropertyType string `json:"bk_property_type"`
	Creator        string `json:"creator"`
}

// ResourceWatchResp 监听资源变化返回
type ResourceWatchResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     ResourceWatchData `json:"data"`
}

// ResourceWatchData 监听资源变化数据
type ResourceWatchData struct {
	BkWatched bool                 `json:"bk_watched"`
	BkEvents  []ResourceWatchEvent `json:"bk_events"`
}

type ResourceWatchEvent struct {
	BkCursor    string                 `json:"bk_cursor"`
	BkResource  string                 `json:"bk_resource"`
	BkEventType string                 `json:"bk_event_type"`
	BkDetail    map[string]interface{} `json:"bk_detail"`
}

// SearchModuleResp 查询模块返回
type SearchModuleResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     struct {
		Count int                      `json:"count" mapstructure:"count"`
		Info  []map[string]interface{} `json:"info" mapstructure:"info"`
	} `json:"data" mapstructure:"data"`
}

// SearchSetResp 查询集群返回
type SearchSetResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     struct {
		Count int                      `json:"count" mapstructure:"count"`
		Info  []map[string]interface{} `json:"info" mapstructure:"info"`
	} `json:"data" mapstructure:"data"`
}

// ListServiceInstanceDetailResp 查询服务实例详情返回
type ListServiceInstanceDetailResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     ListServiceInstanceDetailData `json:"data" mapstructure:"data"`
}

// ListServiceInstanceDetailData 查询服务实例详情数据
type ListServiceInstanceDetailData struct {
	Count int                             `json:"count" mapstructure:"count"`
	Info  []ListServiceInstanceDetailInfo `json:"info" mapstructure:"info"`
}

// ListServiceInstanceDetailInfo 查询服务实例详情信息
type ListServiceInstanceDetailInfo struct {
	BkBizId           int         `json:"bk_biz_id" mapstructure:"bk_biz_id"`
	ID                int         `json:"id" mapstructure:"id"`
	Name              string      `json:"name" mapstructure:"name"`
	BkModuleId        int         `json:"bk_module_id" mapstructure:"bk_module_id"`
	BkHostId          int         `json:"bk_host_id" mapstructure:"bk_host_id"`
	ServiceTemplateId int         `json:"service_template_id" mapstructure:"service_template_id"`
	ProcessInstances  interface{} `json:"process_instances" mapstructure:"process_instances"`
}

// SearchDynamicGroupResp 查询动态分组返回
type SearchDynamicGroupResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     SearchDynamicGroupData `json:"data" mapstructure:"data"`
}

// SearchDynamicGroupData 查询动态分组数据
type SearchDynamicGroupData struct {
	Count int                      `json:"count" mapstructure:"count"`
	Info  []SearchDynamicGroupInfo `json:"info" mapstructure:"info"`
}

// SearchDynamicGroupInfo 查询动态分组信息
type SearchDynamicGroupInfo struct {
	BkBizId int    `json:"bk_biz_id" mapstructure:"bk_biz_id"`
	ID      string `json:"id" mapstructure:"id"`
	Name    string `json:"name" mapstructure:"name"`
	BkObjId string `json:"bk_obj_id" mapstructure:"bk_obj_id"`
}

// ExecuteDynamicGroupResp 执行动态分组返回
type ExecuteDynamicGroupResp struct {
	define.ApiCommonRespMeta `mapstructure:",squash"`
	Data                     ExecuteDynamicGroupData `json:"data" mapstructure:"data"`
}

// ExecuteDynamicGroupData 执行动态分组数据
type ExecuteDynamicGroupData struct {
	Count int                      `json:"count" mapstructure:"count"`
	Info  []map[string]interface{} `json:"info" mapstructure:"info"`
}
