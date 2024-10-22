// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package routecluster

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// Metrics :
type Metrics interface {
	// Query Metric
	QueryFailedCountInc(db string, flowLog *logging.Entry)
	QuerySuccessCountInc(db string, flowLog *logging.Entry)
	QueryReceivedCountInc(db string, flowLog *logging.Entry)

	RawQueryFailedCountInc(db string, flowLog *logging.Entry)
	RawQuerySuccessCountInc(db string, flowLog *logging.Entry)
	RawQueryReceivedCountInc(db string, flowLog *logging.Entry)

	// Write Metric
	WriteFailedCountInc(db string, flowLog *logging.Entry)
	WritePartSuccCountInc(db string, flowLog *logging.Entry)
	WriteSuccessCountInc(db string, flowLog *logging.Entry)
	WriteReceivedCountInc(db string, flowLog *logging.Entry)

	// create db Metric
	CreateDBFailedCountInc(db string, flowLog *logging.Entry)
	CreateDBPartSuccCountInc(db string, flowLog *logging.Entry)
	CreateDBSuccessCountInc(db string, flowLog *logging.Entry)
	CreateDBReceivedCountInc(db string, flowLog *logging.Entry)

	// Query Metric
	QueryBackendFailedCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)
	QueryBackendSuccessCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)
	QueryBackendSendCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)

	RawQueryBackendFailedCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)
	RawQueryBackendSuccessCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)
	RawQueryBackendSendCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)

	// Write Metric
	WriteBackendFailedCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)
	WriteBackendSuccessCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)
	WriteBackendSendCountInc(backend string, db string, tagKey string, flowLog *logging.Entry)

	// create db Metric
	CreateDBBackendFailedCountInc(backend string, db string, flowLog *logging.Entry)
	CreateDBBackendSuccessCountInc(backend string, db string, flowLog *logging.Entry)
	CreateDBBackendSendCountInc(backend string, db string, flowLog *logging.Entry)
}

var (
	clusterRequest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "influxdb_proxy_cluster_request",
			Help: "cluster request",
		},
		[]string{"cluster", "action", "status", "db", "type"},
	)

	handledBackend = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "influxdb_proxy_cluster_handled_backend",
			Help: "cluster to backend call record",
		},
		[]string{"cluster", "backend", "action", "status", "db", "tag_key", "type"},
	)
)

type metric struct {
	clusterName string
}

// NewClusterMetric :
func NewClusterMetric(clusterName string) Metrics {
	return newClusterMetric(clusterName)
}

func newClusterMetric(clusterName string) Metrics {
	m := &metric{
		clusterName: clusterName,
	}
	return m
}

// QueryFailedCountInc : query failed count
func (m *metric) QueryFailedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "query", "fail", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// QuerySuccessCountInc : query success count
func (m *metric) QuerySuccessCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "query", "success", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// QueryReceivedCountInc : query received count
func (m *metric) QueryReceivedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "query", "received", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// RawQueryFailedCountInc: raw query failed count
func (m *metric) RawQueryFailedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "query", "fail", db, "flux")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// RawQuerySuccessCountInc : raw query success count
func (m *metric) RawQuerySuccessCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "query", "success", db, "flux")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// RawQueryReceivedCountInc : raw query received count
func (m *metric) RawQueryReceivedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "query", "received", db, "flux")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// WriteFailedCountInc : write failed count
func (m *metric) WriteFailedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "write", "fail", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// WritePartSuccCountInc : write part success count
func (m *metric) WritePartSuccCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "write", "part_success", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// WriteSuccessCountInc : write success count
func (m *metric) WriteSuccessCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "write", "success", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// WriteReceivedCountInc : write received count
func (m *metric) WriteReceivedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "write", "received", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// CreateDBFailedCountInc : create db count
func (m *metric) CreateDBFailedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "createDB", "fail", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// CreateDBPartSuccCountInc : create db part success count
func (m *metric) CreateDBPartSuccCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "createDB", "part_success", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// CreateDBSuccessCountInc : create db success count
func (m *metric) CreateDBSuccessCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "createDB", "success", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// CreateDBReceivedCountInc : create db received count
func (m *metric) CreateDBReceivedCountInc(db string, flowLog *logging.Entry) {
	metric, err := clusterRequest.GetMetricWithLabelValues(m.clusterName, "createDB", "received", db, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// QueryBackendFailedCountInc : query backend failed count
func (m *metric) QueryBackendFailedCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "query", "fail", db, tagKey, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// QueryBackendSuccessCountInc : query backend success count
func (m *metric) QueryBackendSuccessCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "query", "success", db, tagKey, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// QueryBackendSendCountInc : query backend send count
func (m *metric) QueryBackendSendCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "query", "send", db, tagKey, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// RawQueryBackendFailedCountInc : raw query backend failed count
func (m *metric) RawQueryBackendFailedCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "query", "fail", db, tagKey, "flux")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// RawQueryBackendSuccessCountInc : raw query backend success count
func (m *metric) RawQueryBackendSuccessCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "query", "success", db, tagKey, "flux")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// RawQueryBackendSendCountInc : raw query backend send count
func (m *metric) RawQueryBackendSendCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "query", "send", db, tagKey, "flux")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// WriteBackendFailedCountInc : write backend failed count
func (m *metric) WriteBackendFailedCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "write", "fail", db, tagKey, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// WriteBackendSuccessCountInc : write backend success count
func (m *metric) WriteBackendSuccessCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "write", "success", db, tagKey, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// WriteBackendSendCountInc : write backend send count
func (m *metric) WriteBackendSendCountInc(backend string, db string, tagKey string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "write", "send", db, tagKey, "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// cCreateDBBackendFailedCountInc : reate db Metric
func (m *metric) CreateDBBackendFailedCountInc(backend string, db string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "createDB", "fail", db, "", "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// CreateDBBackendSuccessCountInc : create db backend success count
func (m *metric) CreateDBBackendSuccessCountInc(backend string, db string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "createDB", "success", db, "", "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

// CreateDBBackendSendCountInc : create db backend send count
func (m *metric) CreateDBBackendSendCountInc(backend string, db string, flowLog *logging.Entry) {
	metric, err := handledBackend.GetMetricWithLabelValues(m.clusterName, backend, "createDB", "send", db, "", "influxql")
	if err != nil {
		flowLog.Errorf("get metric failed,error:%s", err.Error())
		return
	}
	metric.Inc()
}

func init() {
	// register the metrics
	prometheus.MustRegister(clusterRequest, handledBackend)
}
