// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var metadataTaskNamespace = "bmw_metadata"

// metadata metrics
var (
	// consul数据操作统计
	consulCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metadataTaskNamespace,
			Name:      "consul_count",
			Help:      "consul execute count",
		},
		[]string{"key", "operation"},
	)

	// GSE变动统计
	gseCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metadataTaskNamespace,
			Name:      "gse_count",
			Help:      "gse change count",
		},
		[]string{"dataid", "operation"},
	)

	// ES变动统计
	esCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metadataTaskNamespace,
			Name:      "es_count",
			Help:      "es change count",
		},
		[]string{"table_id", "operation"},
	)

	// redis数据操作统计
	redisCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metadataTaskNamespace,
			Name:      "redis_count",
			Help:      "redis change count",
		},
		[]string{"key", "operation"},
	)

	// mysql数据操作统计
	mysqlCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metadataTaskNamespace,
			Name:      "mysql_count",
			Help:      "mysql change count",
		},
		[]string{"table", "operation"},
	)
	// rt metric 数量统计
	rtMetricNum = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metadataTaskNamespace,
			Name:      "rt_metric_num",
			Help:      "rt metric number",
		},
		[]string{"table_id"},
	)
)

// ConsulPutCount consul put count
func ConsulPutCount(key string) {
	metric, err := consulCount.GetMetricWithLabelValues(key, "PUT")
	if err != nil {
		logger.Errorf("prom get consul put count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// ConsulDeleteCount consul delete count
func ConsulDeleteCount(key string) {
	metric, err := consulCount.GetMetricWithLabelValues(key, "DELETE")
	if err != nil {
		logger.Errorf("prom get consul delete count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// GSEUpdateCount gse update count
func GSEUpdateCount(dataid uint) {
	metric, err := gseCount.GetMetricWithLabelValues(strconv.Itoa(int(dataid)), "UPDATE")
	if err != nil {
		logger.Errorf("prom get gse update count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// ESChangeCount es change count
func ESChangeCount(tableId, operation string) {
	metric, err := esCount.GetMetricWithLabelValues(tableId, operation)
	if err != nil {
		logger.Errorf("prom get es change count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RedisCount redis count
func RedisCount(key, operation string) {
	metric, err := redisCount.GetMetricWithLabelValues(key, operation)
	if err != nil {
		logger.Errorf("prom get redis count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// MysqlCount mysql count
func MysqlCount(tableName, operation string, count float64) {
	metric, err := mysqlCount.GetMetricWithLabelValues(tableName, operation)
	if err != nil {
		logger.Errorf("prom get mysql count metric failed: %s", err)
		return
	}
	metric.Add(count)
}

// RtMetricNum rt metric count
func RtMetricNum(tableId string, num float64) {
	metric, err := rtMetricNum.GetMetricWithLabelValues(tableId)
	if err != nil {
		logger.Errorf("prom get rt metric num metric failed: %s", err)
		return
	}
	metric.Set(num)
}

func init() {
	// register the metrics
	Registry.MustRegister(
		consulCount,
		gseCount,
		esCount,
		redisCount,
		mysqlCount,
		rtMetricNum,
	)
}
