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
	"encoding/json"
	"regexp"
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var Bkdata BkdataService

type BkdataService struct{}

const (
	defaultDataScopeName = "default"
	metricNamePattern    = `^[a-zA-Z0-9_]+$`
)

// getScopeNameFromGroupKey 与 Python TimeSeriesGroup.get_scope_name_from_group_key 一致：从 group_key 转换为 field_scope
func getScopeNameFromGroupKey(groupKey string, metricGroupDimensions string) string {
	if groupKey == "" || metricGroupDimensions == "" || metricGroupDimensions == "[]" {
		return defaultDataScopeName
	}
	var dims []map[string]any
	if err := json.Unmarshal([]byte(metricGroupDimensions), &dims); err != nil || len(dims) == 0 {
		return defaultDataScopeName
	}
	keyValueMap := make(map[string]string)
	for _, part := range strings.Split(groupKey, "||") {
		if idx := strings.Index(part, ":"); idx >= 0 {
			key := strings.TrimSpace(part[:idx])
			val := strings.TrimSpace(part[idx+1:])
			keyValueMap[key] = val
		}
	}
	var levels []string
	for _, dimConfig := range dims {
		dimName, _ := dimConfig["key"].(string)
		value := keyValueMap[dimName]
		if value == "" {
			if v, ok := dimConfig["default_value"].(string); ok {
				value = v
			}
		}
		levels = append(levels, value)
	}
	allEmpty := true
	for _, level := range levels {
		if level != "" {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		return defaultDataScopeName
	}
	result := strings.Join(levels, "||")
	if result == "" {
		return defaultDataScopeName
	}
	return result
}

// QueryMetricAndDimensionOpts 可选参数，与 Python get_metric_from_bkdata 的请求与解析逻辑对齐
type QueryMetricAndDimensionOpts struct {
	// MetricGroupDimensions 分组维度配置 JSON，非空时使用 v2 接口并解析 group_dimensions
	MetricGroupDimensions string
}

// QueryMetricAndDimension 通过 bkdata 获取指标和维度数据，与 Python get_metric_from_bkdata 完全一致
func (s BkdataService) QueryMetricAndDimension(bkTenantId string, storage string, rt string, metricGroupDimensions string) ([]map[string]any, error) {
	bkdataApi, err := api.GetBkdataApi(bkTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	params := map[string]string{
		"bk_tenant_id":    bkTenantId,
		"storage":         storage,
		"result_table_id": rt,
		"no_value":        "true",
	}
	if metricGroupDimensions != "" && metricGroupDimensions != "[]" {
		params["version"] = "v2"
	}

	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.QueryMetricAndDimension().SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "query metrics and dimension error by bkdata: storage=%s, table_id=%s", storage, rt)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "query metrics and dimension error by bkdata: storage=%s, table_id=%s", storage, rt)
	}

	metrics := resp.Data["metrics"]
	metricInfo, ok := metrics.([]any)
	if !ok || len(metricInfo) == 0 {
		logger.Errorf("query bkdata metrics error, params: %v, metrics: %v", params, metricInfo)
		return []map[string]any{}, errors.New("query metrics error, no data")
	}
	logger.Infof("query bkdata metrics success for rt(%v), params: %v, metrics: %d", rt, params, len(metricInfo))

	useV2 := metricGroupDimensions != "" && metricGroupDimensions != "[]"

	validNameRegex := regexp.MustCompile(metricNamePattern)
	var retData []map[string]any

	for _, dataInfo := range metricInfo {
		md, ok := dataInfo.(map[string]any)
		if !ok {
			continue
		}
		name, _ := md["name"].(string)
		if name == "" {
			continue
		}
		if !validNameRegex.MatchString(name) {
			logger.Warnf("invalid metric name: %s", name)
			continue
		}

		if useV2 {
			groupDimensions, _ := md["group_dimensions"].(map[string]any)
			if groupDimensions == nil {
				continue
			}
			latestUpdateTime := 0.0
			for _, groupInfoRaw := range groupDimensions {
				groupInfo, ok := groupInfoRaw.(map[string]any)
				if !ok {
					continue
				}
				ut, _ := groupInfo["update_time"].(float64)
				if ut > latestUpdateTime {
					latestUpdateTime = ut
				}
			}
			for groupKey, groupInfoRaw := range groupDimensions {
				groupInfo, ok := groupInfoRaw.(map[string]any)
				if !ok {
					continue
				}
				updateTime, _ := groupInfo["update_time"].(float64)
				tagValueList := make(map[string]any)
				if dims, ok := groupInfo["dimensions"].([]any); ok {
					for _, d := range dims {
						if dimName, ok := d.(string); ok && dimName != "" {
							tagValueList[dimName] = map[string]any{
								"last_update_time": updateTime / 1000,
								"values":           []any{},
							}
						}
					}
				}
				item := map[string]any{
					"field_name":       name,
					"field_scope":      getScopeNameFromGroupKey(groupKey, metricGroupDimensions),
					"last_modify_time": int64(latestUpdateTime) / 1000,
					"tag_value_list":   tagValueList,
					"is_active":        true,
				}
				retData = append(retData, item)
			}
		} else {
			updateTime, _ := md["update_time"].(float64)
			tagValueList := make(map[string]any)
			if dimensions, ok := md["dimensions"].([]any); ok {
				for _, d := range dimensions {
					dim, ok := d.(map[string]any)
					if !ok {
						logger.Errorf("dimension data not map[string]interface{}, dimInfo: %v", d)
						continue
					}
					dimName, ok := dim["name"].(string)
					if dimName == "" || !ok {
						logger.Errorf("dimension: %s is not string", dim["name"])
						continue
					}
					dimUpdateTime, _ := dim["update_time"].(float64)
					var values []any
					if vals, ok := dim["values"].([]any); ok {
						for _, v := range vals {
							if vm, ok := v.(map[string]any); ok {
								if val, has := vm["value"]; has {
									values = append(values, val)
								}
							}
						}
					}
					tagValueList[dimName] = map[string]any{
						"last_update_time": dimUpdateTime / 1000,
						"values":           values,
					}
				}
			}
			item := map[string]any{
				"field_name":       name,
				"field_scope":      defaultDataScopeName,
				"last_modify_time": int64(updateTime) / 1000,
				"tag_value_list":   tagValueList,
				"is_active":        true, // 只要从Bkdata 指标发现服务中能够获取到，即为活跃指标
			}
			retData = append(retData, item)
		}
	}

	return retData, nil
}
