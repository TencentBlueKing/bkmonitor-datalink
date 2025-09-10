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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var Bkdata BkdataService

type BkdataService struct{}

// QueryMetricAndDimension 查询指标和维度数据
func (s BkdataService) QueryMetricAndDimension(bkTenantId string, storage string, rt string) ([]map[string]any, error) {
	bkdataApi, err := api.GetBkdataApi(bkTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}
	var resp bkdata.CommonMapResp
	// NOTE: 设置no_value=true，不需要返回维度对应的 value
	params := map[string]string{"storage": storage, "result_table_id": rt, "no_value": "true"}
	if _, err = bkdataApi.QueryMetricAndDimension().SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "query metrics and dimension error by bkdata: %s, table_id: %s", storage, rt)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "query metrics and dimension error by bkdata: %s, table_id: %s", storage, rt)
	}

	metrics := resp.Data["metrics"]
	metricInfo, ok := metrics.([]any)
	if !ok || len(metricInfo) == 0 {
		logger.Errorf("query bkdata metrics error, params: %v, metrics: %v", params, metricInfo)
		return nil, errors.New("query metrics error, no data")
	}

	// parse metrics and dimensions
	var MetricsDimension []map[string]any
	for _, dataInfo := range metricInfo {
		data, ok := dataInfo.(map[string]any)
		if !ok {
			logger.Errorf("metric data not map[string]interface{}, data: %v", params, metricInfo)
			continue
		}
		lastModifyTime := data["update_time"].(float64)
		dimensions := data["dimensions"].([]any)
		tagValueList := make(map[string]any)
		for _, dimInfo := range dimensions {
			dim, ok := dimInfo.(map[string]any)
			if !ok {
				logger.Errorf("dimension data not map[string]interface{}, dimInfo: %v", dimInfo)
				continue
			}
			// 判断值为 string
			tag_name, ok := dim["name"].(string)
			if !ok {
				logger.Errorf("dimension: %s is not string", dim["name"])
				continue
			}
			// 判断值为 float64
			tagUpdateTime, ok := dim["update_time"].(float64)
			if !ok {
				logger.Errorf("dimension: %s is not string", dim["name"])
				continue
			}
			tagValueList[tag_name] = map[string]any{"last_update_time": tagUpdateTime / 1000}
		}

		item := map[string]any{
			"field_name":       data["name"],
			"last_modify_time": lastModifyTime / 1000,
			"tag_value_list":   tagValueList,
		}
		MetricsDimension = append(MetricsDimension, item)
	}
	return MetricsDimension, nil
}
