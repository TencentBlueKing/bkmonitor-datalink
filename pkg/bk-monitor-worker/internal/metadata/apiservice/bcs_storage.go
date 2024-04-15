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
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

var BcsStorage BcsStorageService

type BcsStorageService struct{}

// Fetch 获取k8s资源信息
// resourceType [Node, ReplicaSet, Pod, Job]
func (BcsStorageService) Fetch(clusterId, resourceType string, fields []string) ([]map[string]interface{}, error) {
	fieldStr := strings.Join(fields, ",")
	bcsStorageApi, err := api.GetBcsStorageApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bcsStorageApi failed")
	}
	var result []map[string]interface{}
	offset := 0
	pathParam := map[string]string{
		"cluster_id": clusterId,
		"type":       resourceType,
	}
	queryParam := map[string]string{
		"limit":  BcsStoragePageSizeStr,
		"offset": strconv.Itoa(offset),
		"field":  fieldStr,
	}
	for {
		var resp define.APICommonListMapResp
		if _, err := bcsStorageApi.Fetch().SetPathParams(pathParam).SetQueryParams(queryParam).SetResult(&resp).Request(); err != nil {
			return nil, errors.Wrapf(err, "Fetch cluster [%s] resource [%s] fields [%s] offset [%d] failed", clusterId, resourceType, fieldStr, offset)
		}
		if err := resp.Err(); err != nil {
			return nil, errors.Wrapf(err, "Fetch cluster [%s] resource [%s] fields [%s] offset [%d] failed", clusterId, resourceType, fieldStr, offset)
		}
		if len(resp.Data) == 0 {
			break
		}
		result = append(result, resp.Data...)
		offset += len(resp.Data)
		queryParam["offset"] = strconv.Itoa(offset)
	}
	return result, nil
}
