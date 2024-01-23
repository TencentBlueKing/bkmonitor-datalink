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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcs_cc"
)

var BcsCc BcsCcService

type BcsCcService struct{}

func (BcsCcService) BatchGetProjects(limit int, desireAllData, filterK8sKind bool) ([]map[string]string, error) {
	if limit == 0 {
		limit = 2000
	}
	params := make(map[string]string)
	params["limit"] = strconv.Itoa(limit)
	if desireAllData {
		params["desire_all_data"] = "1"
	} else {
		params["desire_all_data"] = "0"
	}
	bcsCcApi, err := api.GetBcsCcApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bcsCcApi failed")
	}
	var resp bcs_cc.GetProjectsResp
	_, err = bcsCcApi.GetProjects().SetQueryParams(params).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrap(err, "GetProjects failed")
	}
	var result []map[string]string
	for _, i := range resp.Data.Results {
		// 是否过滤掉非 k8s 类型数据
		if filterK8sKind && i.Kind != 1 {
			continue
		}
		result = append(result, map[string]string{
			"projectId":   i.ProjectId,
			"name":        i.ProjectName,
			"projectCode": i.EnglishName,
			"bkBizId":     strconv.Itoa(i.CcAppId),
		})
	}

	return result, nil
}
