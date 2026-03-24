// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package clustermetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	redisStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Record struct {
	Instance ClusterInstance
	Metric   *ClusterMetric
	Data     []map[string]any
}

func (r *Record) Print() string {
	return fmt.Sprintf("metric_name: %v, instance: %v, data length: %v",
		r.Metric.MetricName, r.Instance.GetContext(), len(r.Data))
}

type KvShipper struct {
	RedisClient *redisStore.Instance
}

func renderKvKey(metric *ClusterMetric, instCtx map[string]string) (string, error) {
	pattern := config.ClusterMetricSubKeyPattern
	instCtx[config.ClusterMetricFieldName] = metric.MetricName
	keys := []string{config.ClusterMetricClusterFieldName, config.ClusterMetricFieldName}
	for _, k := range keys {
		val, ok := instCtx[k]
		if !ok {
			return "", errors.Errorf("Miss expected variable(%v) in instance context", k)
		}
		pattern = strings.ReplaceAll(pattern, fmt.Sprintf("{%s}", k), val)
	}
	return pattern, nil
}

func (kw *KvShipper) Write(ctx context.Context, record *Record) {
	key, err := renderKvKey(record.Metric, record.Instance.GetContext())
	if err != nil {
		logger.Errorf("Fail to render key, %+v", err)
		return
	}
	ttl := time.Duration(config.ClusterMetricStorageTTL)
	// 提取已经保存的指标数据
	savedData := make([]map[string]any, 0)
	savedContent := kw.RedisClient.HGet(config.ClusterMetricKey, key)
	if savedContent == "" {
		logger.Infof("No key(%s) contents", key)
	} else {
		err = json.Unmarshal([]byte(savedContent), &savedData)
		if err != nil {
			logger.Errorf("Fail to unmarshal key(%s) content, %v, $v", key, err, savedContent)
			return
		}
	}
	// 合并新旧指标数据
	var validData []map[string]any
	if len(savedData) > 0 {
		data := append(record.Data, savedData...)
		// 数据以秒为单位进行存储，移除过期数据
		expiredTime := float64(time.Now().Unix() - int64(ttl))
		for _, d := range data {
			if t, ok := d["time"].(float64); ok && t > expiredTime {
				validData = append(validData, d)
			}
		}
	} else {
		validData = record.Data
	}
	// 将合并数据写入存储
	if len(validData) == 0 {
		logger.Warnf("No valid data to write redis storage, %v", record)
		return
	}
	content, err := json.Marshal(validData)
	if err != nil {
		logger.Error("Fail to pack message to string for redis storage, %v, %v", record, err)
		return
	}
	err = kw.RedisClient.HSet(config.ClusterMetricKey, key, string(content))
	if err != nil {
		logger.Error("Fait to store message to redis, %v", err)
		return
	}
	// 更新指标
	kvMetricMeta := KvClusterMetricMeta{
		MetricName: record.Metric.MetricName,
		Tags:       record.Metric.Tags,
	}
	metricMetaContent, err := json.Marshal(&kvMetricMeta)
	if err != nil {
		logger.Error("Fail to pack metric metaInfo to string for redis storage, %v, %v", record, err)
		return
	}
	err = kw.RedisClient.HSet(config.ClusterMetricMetaKey, record.Metric.MetricName, string(metricMetaContent))
	if err != nil {
		logger.Error("Fait to store metric metaInfo to redis, %v", err)
		return
	}
}

type KvClusterMetricMeta struct {
	MetricName string   `json:"metric_name"`
	Tags       []string `json:"tags"`
}
