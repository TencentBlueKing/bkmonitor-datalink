// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var (
	httpRequest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "influxdb_proxy_http_request",
			Help: "http request received",
		},
		[]string{"action", "status", "db", "code", "type"},
	)
	// 记录一下proxy启动和重载时间
	upRecord = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "influxdb_proxy_up_info",
			Help: "influxdb proxy start/restart timestamp",
		},
		[]string{"action"},
	)

	aliveConsul = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "influxdb_proxy_consul_alive_status",
			Help: "whether consul connection alive",
		},
		[]string{},
	)
)

// Query Metric
func QueryFailedCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("query", "fail", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func QuerySuccessCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("query", "success", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func QueryReceivedCountInc(db string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("query", "received", db, "0", "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// Query Metric
func RawQueryFailedCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("query", "fail", db, code, "flux")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func RawQuerySuccessCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("query", "success", db, code, "flux")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func RawQueryReceivedCountInc(db string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("query", "received", db, "0", "flux")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// Write Metric
func WriteFailedCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("write", "fail", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func WritePartSuccCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("write", "part_success", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func WriteSuccessCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("write", "success", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func WriteReceivedCountInc(db string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("write", "received", db, "0", "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

// create db Metric
func CreateDBFailedCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("createDB", "fail", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func CreateDBPartSuccCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("createDB", "part_success", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func CreateDBSuccessCountInc(db string, code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("createDB", "success", db, code, "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func CreateDBReceivedCountInc(db string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("createDB", "received", db, "0", "influxql")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func ReloadReceivedCountInc() error {
	metric, err := httpRequest.GetMetricWithLabelValues("reload", "received", "", "0", "")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func ReloadSuccessCountInc(code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("reload", "success", "", code, "")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func ReloadFailedCountInc(code string) error {
	metric, err := httpRequest.GetMetricWithLabelValues("reload", "fail", "", code, "")
	if err != nil {
		return err
	}
	metric.Inc()
	return nil
}

func ConsulAliveUp() error {
	metric, err := aliveConsul.GetMetricWithLabelValues()
	if err != nil {
		return err
	}
	metric.Set(1)
	return nil
}

func ConsulAliveDown() error {
	metric, err := aliveConsul.GetMetricWithLabelValues()
	if err != nil {
		return err
	}
	metric.Set(0)
	return nil
}

func ProxyStartRecord(timestamp int64) error {
	metric, err := upRecord.GetMetricWithLabelValues("start")
	if err != nil {
		return err
	}
	metric.Set(float64(timestamp))
	return nil
}

func ProxyReloadRecord(timestamp int64) error {
	metric, err := upRecord.GetMetricWithLabelValues("reload")
	if err != nil {
		return err
	}
	metric.Set(float64(timestamp))
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
	prometheus.MustRegister(httpRequest, upRecord, aliveConsul)
}
