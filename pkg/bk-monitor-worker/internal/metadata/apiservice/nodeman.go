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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
)

var Nodeman NodemanService

type NodemanService struct{}

// GetProxies 【节点管理2.0】查询云区域下的proxy列表
func (s NodemanService) GetProxies(bkCloudId int) ([]nodeman.ProxyData, error) {
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "nodemanApi failed")
	}
	var resp nodeman.GetProxiesResp
	if _, err := nodemanApi.GetProxies().SetQueryParams(map[string]string{"bk_cloud_id": strconv.Itoa(bkCloudId)}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrap(err, "GetProxiesResp failed")
	}
	return resp.Data, nil
}
