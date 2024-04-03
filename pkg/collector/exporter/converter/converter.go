// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"strconv"
	"strings"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	converterFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "converter_failed_total",
			Help:      "Converter convert failed total",
		},
		[]string{"record_type", "id"},
	)

	converterSpanKindTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "converter_span_kind_total",
			Help:      "Converter traces span kind total",
		},
		[]string{"id", "kind"},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncConverterFailedCounter(rtype define.RecordType, dataId int32) {
	converterFailedTotal.WithLabelValues(rtype.S(), strconv.Itoa(int(dataId))).Inc()
}

func (m *metricMonitor) IncConverterSpanKindCounter(dataId int32, kind string) {
	converterSpanKindTotal.WithLabelValues(strconv.Itoa(int(dataId)), kind).Inc()
}

type Converter interface {
	Convert(record *define.Record, f define.GatherFunc)
}

type EventConverter interface {
	Converter
	ToEvent(define.Token, int32, common.MapStr) define.Event
	ToDataID(*define.Record) int32
}

func NewCommonConverter() Converter {
	return commonConverter{}
}

type commonConverter struct{}

func (c commonConverter) Convert(record *define.Record, f define.GatherFunc) {
	switch record.RecordType {
	case define.RecordTraces:
		TracesConverter.Convert(record, f)
	case define.RecordMetrics:
		MetricsConverter.Convert(record, f)
	case define.RecordLogs:
		LogsConverter.Convert(record, f)
	case define.RecordPushGateway:
		PushGatewayConverter.Convert(record, f)
	case define.RecordRemoteWrite:
		RemoteWriteConverter.Convert(record, f)
	case define.RecordProxy:
		ProxyConverter.Convert(record, f)
	case define.RecordPingserver:
		PingserverConverter.Convert(record, f)
	case define.RecordProfiles:
		ProfilesConverter.Convert(record, f)
	case define.RecordFta:
		FtaConverter.Convert(record, f)
	}
}

func CleanAttributesMap(attrs map[string]interface{}) map[string]interface{} {
	for k := range attrs {
		if strings.TrimSpace(k) == "" {
			delete(attrs, k)
		}
	}
	return attrs
}
