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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcs"
)

var Bcs BcsService

type BcsService struct{}

// FetchSharedClusterNamespaces 拉取集群下的命名空间数据
func (BcsService) FetchSharedClusterNamespaces(clusterId string, projectCode string) ([]map[string]string, error) {
	if projectCode == "" {
		// 当为`-`时，为拉取集群下所有的命名空间数据
		projectCode = "-"
	}

	api, err := api.GetBcsApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bcs api failed")
	}
	var resp bcs.FetchSharedClusterNamespacesResp
	_, err = api.FetchSharedClusterNamespaces().SetPathParams(map[string]string{
		"project_code": projectCode,
		"cluster_id":   clusterId,
	}).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "FetchSharedClusterNamespaces with cluster_id [%s] project_code [%s] failed", clusterId, projectCode)
	}
	var result []map[string]string
	for _, ns := range resp.Data {
		proId, _ := ns["projectID"].(string)
		proCode, _ := ns["projectCode"].(string)
		namespace, _ := ns["name"].(string)

		result = append(result, map[string]string{
			"projectId":   proId,
			"projectCode": proCode,
			"clusterId":   clusterId,
			"namespace":   namespace,
		})
	}
	return result, nil
}
