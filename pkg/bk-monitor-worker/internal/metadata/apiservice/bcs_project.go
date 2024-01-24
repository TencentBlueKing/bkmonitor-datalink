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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsproject"
)

var BcsProject BcsProjectService

type BcsProjectService struct{}

func (BcsProjectService) BatchGetProjects(kind string) ([]map[string]string, error) {
	if kind == "" {
		kind = "k8s"
	}
	bcsProjectApi, err := api.GetBcsProjectApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bcsCcApi failed")
	}
	var result []map[string]string
	params := map[string]string{"limit": ApiPageLimitStr, "kind": kind}
	var resp bcsproject.FetchClustersResp
	_, err = bcsProjectApi.GetProjects().SetQueryParams(params).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrap(err, "GetProjects failed")
	}
	for _, i := range resp.Data.Results {
		result = append(result, map[string]string{
			"projectId":   i.ProjectID,
			"name":        i.Name,
			"projectCode": i.ProjectCode,
			"bkBizId":     i.BusinessID,
		})
	}
	return result, nil
}
