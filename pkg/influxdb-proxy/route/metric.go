// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var (
	handledCluster = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "influxdb_proxy_http_handled_cluster",
			Help: "http to cluster call record",
		},
		[]string{"cluster", "action", "status", "db"},
	)
)

// QueryClusterFailedCountInc Query Metric
func QueryClusterFailedCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "query", "fail", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// QueryClusterSuccessCountInc :
func QueryClusterSuccessCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "query", "success", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// QueryClusterSendCountInc :
func QueryClusterSendCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "query", "send", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// WriteClusterFailedCountInc Write Metric
func WriteClusterFailedCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "write", "fail", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// WriteClusterSuccessCountInc :
func WriteClusterSuccessCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "write", "success", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// WriteClusterSendCountInc :
func WriteClusterSendCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "write", "send", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// CreateDBClusterFailedCountInc create db Metric
func CreateDBClusterFailedCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "createDB", "fail", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// CreateDBClusterSuccessCountInc :
func CreateDBClusterSuccessCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "createDB", "success", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// CreateDBClusterSendCountInc :
func CreateDBClusterSendCountInc(cluster string, db string) error {
	metric, err := handledCluster.GetMetricWithLabelValues(cluster, "createDB", "send", db)
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// metricError :
func metricError(name string, err error, flowLog *logging.Entry) {
	if err != nil {
		flowLog.Errorf("handle metric failed,module:%s,error:%s", name, err)
	}
}

func init() {
	// register the metrics
	prometheus.MustRegister(handledCluster)
}
