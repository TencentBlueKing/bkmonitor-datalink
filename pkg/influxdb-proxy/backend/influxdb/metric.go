// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var (
	backendRequest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "influxdb_proxy_backend_request",
			Help: "backend handled request status",
		},
		[]string{"backend", "action", "status", "db", "type"},
	)
	backendHandledPoints = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "influxdb_proxy_backend_handled_points",
			Help: "backend handled handle point count status",
		},
		[]string{"backend", "action", "status", "db"},
	)

	backendBackupStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "influxdb_proxy_backend_backup_status",
			Help: "backend backup status",
		},
		[]string{"backend", "type", "action", "status", "db"},
	)

	alive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "influxdb_proxy_backend_alive_status",
			Help: "Number of the alive backend",
		},
		[]string{"backend", "type"},
	)
	kafkaStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "influxdb_proxy_kafka_alive_status",
			Help: "Number of the alive backend",
		},
		[]string{"backend"},
	)
)

// KafkaStatusUp 将kafka状态改为up
func KafkaStatusUp(name string, flowLog *logging.Entry) {
	metric, err := kafkaStatus.GetMetricWithLabelValues(name)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Set(1)
}

// KafkaStatusDown 将kafka状态改为down
func KafkaStatusDown(name string, flowLog *logging.Entry) {
	metric, err := kafkaStatus.GetMetricWithLabelValues(name)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Set(0)
}

type Metrics struct {
	backendName string

	alive prometheus.Gauge
}

// newBackendMetric :
func NewBackendMetric(backendName string) *Metrics {
	m := &Metrics{
		backendName: backendName,
	}
	return m
}

func (m Metrics) QueryFluxCountInc(db, status string, flowLog *logging.Entry) {
	metric, err := backendRequest.GetMetricWithLabelValues(m.backendName, "query", status, db, "flux")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
}

func (m Metrics) QueryCountInc(db string, status string, flowLog *logging.Entry) {
	metric, err := backendRequest.GetMetricWithLabelValues(m.backendName, "query", status, db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
	// m.queryFailRequest.Inc()
}

func (m Metrics) WriteCountInc(db string, status string, flowLog *logging.Entry) {
	metric, err := backendRequest.GetMetricWithLabelValues(m.backendName, "write", status, db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
	// m.writeFailRequest.Inc()
}

// BufferCountAdd 缓存计数
func (m Metrics) BufferCountAdd(db string, status string, count float64, flowLog *logging.Entry) {
	metric, err := backendHandledPoints.GetMetricWithLabelValues(m.backendName, "buffer", status, db)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Add(count)
}

// FlushCountAdd 缓存清理计数
func (m Metrics) FlushCountAdd(db string, status string, count float64, flowLog *logging.Entry) {
	// 记录写入行数
	metric, err := backendHandledPoints.GetMetricWithLabelValues(m.backendName, "flush", status, db)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Add(count)

	// 记录写入次数
	metric, err = backendRequest.GetMetricWithLabelValues(m.backendName, "flush", status, db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
}

func (m Metrics) CreateDBCountInc(db string, status string, flowLog *logging.Entry) {
	metric, err := backendRequest.GetMetricWithLabelValues(m.backendName, "createDB", status, db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
	// m.writeFailRequest.Inc()
}

func (m Metrics) BackupCountInc(db string, status string, flowLog *logging.Entry) {
	metric, err := backendBackupStatus.GetMetricWithLabelValues(m.backendName, "", "backup", status, db)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
	// m.backupStart.Inc()
}

func (m Metrics) SetBackupCount(count float64, flowLog *logging.Entry) {
	metric, err := backendBackupStatus.GetMetricWithLabelValues(m.backendName, "", "backup", "success", "_unknown")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Set(count)
	// m.backupStart.Inc()
}

func (m Metrics) RecoverCreateDBCountInc(db string, status string, flowLog *logging.Entry) {
	metric, err := backendBackupStatus.GetMetricWithLabelValues(m.backendName, "createDB", "recover", status, db)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
	// m.backupStart.Inc()
}

func (m Metrics) RecoverWriteCountInc(db string, status string, flowLog *logging.Entry) {
	metric, err := backendBackupStatus.GetMetricWithLabelValues(m.backendName, "write", "recover", status, db)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	metric.Inc()
	// m.backupStart.Inc()
}

func (m Metrics) SetAlive(backendType string, status bool, flowLog *logging.Entry) {
	metric, err := alive.GetMetricWithLabelValues(m.backendName, backendType)
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err)
		return
	}
	var v float64 = 0
	if status {
		v = 1
	}
	metric.Set(v)
}

func init() {
	// register the metrics
	prometheus.MustRegister(backendRequest, backendHandledPoints, backendBackupStatus, alive, kafkaStatus)
}
