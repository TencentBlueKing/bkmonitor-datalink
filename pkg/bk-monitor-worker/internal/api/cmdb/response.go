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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

type SearchCloudAreaResp struct {
	define.ApiCommonRespMeta
	Data SearchCloudAreaData `json:"data"`
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
	define.ApiCommonRespMeta
	Data ListBizHostsTopoData `json:"data"`
}

type ListBizHostsTopoData struct {
	Count int                        `json:"count"`
	Info  []ListBizHostsTopoDataInfo `json:"info"`
}

type ListBizHostsTopoDataInfoHost struct {
	BkAgentId         string      `json:"bk_agent_id"`
	BkBakOperator     string      `json:"bk_bak_operator"`
	BkCloudId         int         `json:"bk_cloud_id"`
	BkComment         string      `json:"bk_comment"`
	BkHostId          int         `json:"bk_host_id"`
	BkHostInnerip     string      `json:"bk_host_innerip"`
	BkHostInneripV6   string      `json:"bk_host_innerip_v6"`
	BkHostName        string      `json:"bk_host_name"`
	BkHostOuterip     string      `json:"bk_host_outerip"`
	BkHostOuteripV6   string      `json:"bk_host_outerip_v6"`
	BkIspName         interface{} `json:"bk_isp_name"`
	BkOsName          string      `json:"bk_os_name"`
	BkOsType          string      `json:"bk_os_type"`
	BkOsVersion       string      `json:"bk_os_version"`
	BkProvinceName    interface{} `json:"bk_province_name"`
	BkState           interface{} `json:"bk_state"`
	BkStateName       interface{} `json:"bk_state_name"`
	BkSupplierAccount string      `json:"bk_supplier_account"`
	Operator          string      `json:"operator"`
}

type ListBizHostsTopoDataInfoTopo struct {
	BkSetId   int    `json:"bk_set_id"`
	BkSetName string `json:"bk_set_name"`
	Module    []struct {
		BkModuleId   int    `json:"bk_module_id"`
		BkModuleName string `json:"bk_module_name"`
	} `json:"module"`
}

type ListBizHostsTopoDataInfo struct {
	Host ListBizHostsTopoDataInfoHost   `json:"host"`
	Topo []ListBizHostsTopoDataInfoTopo `json:"topo"`
}

type SearchBusinessResp struct {
	define.ApiCommonRespMeta
	Data BusinessData `json:"data"`
}

type BusinessData struct {
	Count int                `json:"count"`
	Info  []BusinessDataInfo `json:"info"`
}

type BusinessDataInfo struct {
	BkBizDeveloper    string    `json:"bk_biz_developer"`
	BkBizId           int       `json:"bk_biz_id"`
	BkBizMaintainer   string    `json:"bk_biz_maintainer"`
	BkBizName         string    `json:"bk_biz_name"`
	BkBizProductor    string    `json:"bk_biz_productor"`
	BkBizTester       string    `json:"bk_biz_tester"`
	BkSupplierAccount string    `json:"bk_supplier_account"`
	CreateTime        time.Time `json:"create_time"`
	DbAppAbbr         string    `json:"db_app_abbr,omitempty"`
	Default           int       `json:"default"`
	Language          string    `json:"language"`
	LastTime          time.Time `json:"last_time"`
	LifeCycle         string    `json:"life_cycle"`
	Operator          string    `json:"operator"`
	TimeZone          string    `json:"time_zone"`
}

type ListHostsWithoutBizResp struct {
	define.ApiCommonRespMeta
	Data ListHostsWithoutBizData `json:"data"`
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
	define.ApiCommonRespMeta
	Data []FindHostBizRelationData `json:"data"`
}

type FindHostBizRelationData struct {
	BkBizId           int    `json:"bk_biz_id"`
	BkHostId          int    `json:"bk_host_id"`
	BkModuleId        int    `json:"bk_module_id"`
	BkSetId           int    `json:"bk_set_id"`
	BkSupplierAccount string `json:"bk_supplier_account"`
}
