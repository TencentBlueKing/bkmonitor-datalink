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

var converterSpanKindTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "converter_span_kind_total",
		Help:      "Converter traces span kind total",
	},
	[]string{"id", "kind"},
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

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

func NewCommonConverter(conf Config) Converter {
	return commonConverter{
		tracesConverter:      TracesConverter,
		metricsConverter:     MetricsConverter,
		logsConverter:        LogsConverter,
		pushGatewayConverter: PushGatewayConverter,
		remoteWriteConverter: RemoteWriteConverter,
		proxyConverter:       ProxyConverter,
		pingserverConverter:  PingserverConverter,
		profilesConverter:    ProfilesConverter,
		ftaConverter:         FtaConverter,
		beatConverter:        BeatConverter,
		tarsConverter:        NewTarsConverter(conf.Tars),
	}
}

type commonConverter struct {
	tracesConverter      EventConverter
	metricsConverter     EventConverter
	logsConverter        EventConverter
	pushGatewayConverter EventConverter
	remoteWriteConverter EventConverter
	proxyConverter       EventConverter
	pingserverConverter  EventConverter
	profilesConverter    EventConverter
	ftaConverter         EventConverter
	beatConverter        EventConverter
	tarsConverter        EventConverter
}

func (c commonConverter) Convert(record *define.Record, f define.GatherFunc) {
	switch record.RecordType {
	case define.RecordTraces:
		c.tracesConverter.Convert(record, f)
	case define.RecordMetrics:
		c.metricsConverter.Convert(record, f)
	case define.RecordLogs:
		c.logsConverter.Convert(record, f)
	case define.RecordPushGateway:
		c.pushGatewayConverter.Convert(record, f)
	case define.RecordRemoteWrite:
		c.remoteWriteConverter.Convert(record, f)
	case define.RecordProxy:
		c.proxyConverter.Convert(record, f)
	case define.RecordPingserver:
		c.pingserverConverter.Convert(record, f)
	case define.RecordProfiles:
		c.profilesConverter.Convert(record, f)
	case define.RecordFta:
		c.ftaConverter.Convert(record, f)
	case define.RecordBeat:
		c.beatConverter.Convert(record, f)
	case define.RecordTars:
		c.tarsConverter.Convert(record, f)
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
