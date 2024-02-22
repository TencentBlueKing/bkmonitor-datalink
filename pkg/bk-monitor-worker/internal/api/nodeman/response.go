// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodeman

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

type PluginInfoResp struct {
	define.ApiCommonRespMeta
	Data []PluginInfoData `json:"data"`
}

// PluginInfoData 插件信息结构体
type PluginInfoData struct {
	CpuArch          string `json:"cpu_arch"`
	Creator          string `json:"creator"`
	Id               int    `json:"id"`
	IsReady          bool   `json:"is_ready"`
	IsReleaseVersion bool   `json:"is_release_version"`
	Location         string `json:"location"`
	Md5              string `json:"md5"`
	Module           string `json:"module"`
	Name             string `json:"name"`
	Os               string `json:"os"`
	PkgMtime         string `json:"pkg_mtime"`
	PkgName          string `json:"pkg_name"`
	PkgSize          int64  `json:"pkg_size"`
	Project          string `json:"project"`
	SourceAppCode    string `json:"source_app_code"`
	Version          string `json:"version"`
}

type GetProxiesResp struct {
	define.ApiCommonRespMeta
	Data []ProxyData `json:"data"`
}

// ProxyData GetProxiesData proxy信息结构体
type ProxyData struct {
	BkCloudId       int    `json:"bk_cloud_id"`
	BkHostId        int    `json:"bk_host_id"`
	InnerIp         string `json:"inner_ip"`
	InnerIpv6       string `json:"inner_ipv6"`
	OuterIp         string `json:"outer_ip"`
	OuterIpv6       string `json:"outer_ipv6"`
	LoginIp         string `json:"login_ip"`
	DataIp          string `json:"data_ip"`
	BkBizId         int    `json:"bk_biz_id"`
	IsManual        bool   `json:"is_manual"`
	BkBizName       string `json:"bk_biz_name"`
	ApId            int    `json:"ap_id"`
	ApName          string `json:"ap_name"`
	Status          string `json:"status"`
	StatusDisplay   string `json:"status_display"`
	Version         string `json:"version"`
	Account         string `json:"account"`
	AuthType        string `json:"auth_type"`
	Port            int    `json:"port"`
	ReCertification bool   `json:"re_certification"`
}

type PluginSearchResp struct {
	define.ApiCommonRespMeta
	Data PluginSearchData `json:"data"`
}

type PluginSearchData struct {
	Total int                    `json:"total"`
	List  []PluginSearchDataItem `json:"list"`
}

type PluginSearchDataItem struct {
	Status            string                             `json:"status"`
	InnerIp           string                             `json:"inner_ip"`
	BkAddressing      string                             `json:"bk_addressing"`
	BkHostName        string                             `json:"bk_host_name"`
	BkBizId           int                                `json:"bk_biz_id"`
	BkAgentId         string                             `json:"bk_agent_id"`
	CpuArch           string                             `json:"cpu_arch"`
	OsType            string                             `json:"os_type"`
	InnerIpv6         string                             `json:"inner_ipv6"`
	BkCloudId         int                                `json:"bk_cloud_id"`
	NodeType          string                             `json:"node_type"`
	NodeFrom          string                             `json:"node_from"`
	ApId              int                                `json:"ap_id"`
	BkHostId          int                                `json:"bk_host_id"`
	Version           string                             `json:"version"`
	StatusDisplay     string                             `json:"status_display"`
	BkCloudName       string                             `json:"bk_cloud_name"`
	BkBizName         string                             `json:"bk_biz_name"`
	JobResult         interface{}                        `json:"job_result"`
	PluginStatus      []PluginSearchDataItemPluginStatus `json:"plugin_status"`
	OperatePermission bool                               `json:"operate_permission"`
	SetupPath         string                             `json:"setup_path"`
}

type PluginSearchDataItemPluginStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version"`
	HostId  int    `json:"host_id"`
}
