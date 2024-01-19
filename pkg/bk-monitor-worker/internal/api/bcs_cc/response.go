// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs_cc

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

type GetProjectsResp struct {
	define.ApiCommonRespMeta
	Data GetProjectsData `json:"data"`
}

type GetProjectsData struct {
	Count   int                     `json:"count"`
	Results []GetProjectsDataResult `json:"results"`
}

type GetProjectsDataResult struct {
	ApprovalStatus int       `json:"approval_status"`
	ApprovalTime   time.Time `json:"approval_time"`
	Approver       string    `json:"approver"`
	BgId           int       `json:"bg_id"`
	BgName         string    `json:"bg_name"`
	Bgid           int       `json:"bgid"`
	CcAppId        int       `json:"cc_app_id"`
	CenterId       int       `json:"center_id"`
	CenterName     string    `json:"center_name"`
	CreatedAt      time.Time `json:"created_at"`
	Creator        string    `json:"creator"`
	DataId         int       `json:"data_id"`
	DeployType     string    `json:"deploy_type"`
	DeptId         int       `json:"dept_id"`
	DeptName       string    `json:"dept_name"`
	Description    string    `json:"description"`
	EnglishName    string    `json:"english_name"`
	Id             int       `json:"id"`
	IsOfflined     bool      `json:"is_offlined"`
	IsSecrecy      bool      `json:"is_secrecy"`
	Kind           int       `json:"kind"`
	LogoAddr       string    `json:"logo_addr"`
	Name           string    `json:"name"`
	ProjectId      string    `json:"project_id"`
	ProjectName    string    `json:"project_name"`
	ProjectType    int       `json:"project_type"`
	Remark         string    `json:"remark"`
	UpdatedAt      time.Time `json:"updated_at"`
	Updator        string    `json:"updator"`
	UseBk          bool      `json:"use_bk"`
}
