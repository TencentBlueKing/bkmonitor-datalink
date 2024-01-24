// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcsproject

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

type FetchClustersResp struct {
	define.ApiCommonRespMeta
	Data FetchClustersRespData `json:"data"`
}

type FetchClustersRespData struct {
	Total   int                           `json:"total"`
	Results []FetchClustersRespDataResult `json:"results"`
}

type FetchClustersRespDataResult struct {
	CreateTime   time.Time `json:"createTime"`
	UpdateTime   time.Time `json:"updateTime"`
	Creator      string    `json:"creator"`
	Updater      string    `json:"updater"`
	Managers     string    `json:"managers"`
	ProjectID    string    `json:"projectID"`
	Name         string    `json:"name"`
	ProjectCode  string    `json:"projectCode"`
	UseBKRes     bool      `json:"useBKRes"`
	Description  string    `json:"description"`
	IsOffline    bool      `json:"isOffline"`
	Kind         string    `json:"kind"`
	BusinessID   string    `json:"businessID"`
	IsSecret     bool      `json:"isSecret"`
	ProjectType  int       `json:"projectType"`
	DeployType   int       `json:"deployType"`
	BGID         string    `json:"BGID"`
	BGName       string    `json:"BGName"`
	DeptID       string    `json:"deptID"`
	DeptName     string    `json:"deptName"`
	CenterID     string    `json:"centerID"`
	CenterName   string    `json:"centerName"`
	BusinessName string    `json:"businessName"`
}
