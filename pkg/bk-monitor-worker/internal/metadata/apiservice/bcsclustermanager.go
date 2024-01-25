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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsclustermanager"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
)

var BcsClusterManager BcsClusterManagerService

type BcsClusterManagerService struct{}

func (BcsClusterManagerService) GetProjectClusters(projectId string, excludeSharedCluster bool) ([]map[string]interface{}, error) {
	api, err := api.GetBcsClusterManagerApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bcs cluster manager api failed")
	}
	var resp bcsclustermanager.FetchClustersResp
	_, err = api.FetchClusters().SetQueryParams(map[string]string{"projectID": projectId}).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "FetchClusters with projectID [%s] failed", projectId)
	}
	var result []map[string]interface{}
	for _, c := range resp.Data {
		opt := optionx.NewOptions(c)
		isShard, _ := opt.GetBool("is_shared")
		// 如果需要，则过滤掉共享集群
		if excludeSharedCluster && isShard {
			continue
		}
		result = append(result, map[string]interface{}{
			"projectId": c["projectID"],
			"clusterId": c["clusterID"],
			"bkBizId":   c["businessID"],
			"isShared":  false,
		})
	}
	return result, nil
}
