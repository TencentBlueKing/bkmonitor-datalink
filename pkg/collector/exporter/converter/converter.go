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
	Clean()
}

type EventConverter interface {
	Converter
	ToEvent(define.Token, int32, common.MapStr) define.Event
	ToDataID(*define.Record) int32
}

func NewCommonConverter(conf *Config) Converter {
	return commonConverter{
		traces:      tracesConverter{},
		metrics:     metricsConverter{},
		logs:        logsConverter{},
		pushGateway: pushGatewayConverter{},
		remoteWrite: remoteWriteConverter{},
		proxy:       proxyConverter{},
		pingserver:  pingserverConverter{},
		profiles:    profilesConverter{},
		fta:         ftaConverter{},
		beat:        beatConverter{},
		logPush:     logPushConverter{},
		tars:        newTarsConverter(conf.Tars),
	}
}

type commonConverter struct {
	traces      EventConverter
	metrics     EventConverter
	logs        EventConverter
	pushGateway EventConverter
	remoteWrite EventConverter
	proxy       EventConverter
	pingserver  EventConverter
	profiles    EventConverter
	fta         EventConverter
	beat        EventConverter
	logPush     EventConverter
	tars        EventConverter
}

func (c commonConverter) Clean() {
	c.tars.Clean()
}

func (c commonConverter) Convert(record *define.Record, f define.GatherFunc) {
	switch record.RecordType {
	case define.RecordTraces:
		c.traces.Convert(record, f)
	case define.RecordMetrics:
		c.metrics.Convert(record, f)
	case define.RecordLogs:
		c.logs.Convert(record, f)
	case define.RecordPushGateway:
		c.pushGateway.Convert(record, f)
	case define.RecordRemoteWrite:
		c.remoteWrite.Convert(record, f)
	case define.RecordProxy:
		c.proxy.Convert(record, f)
	case define.RecordPingserver:
		c.pingserver.Convert(record, f)
	case define.RecordProfiles:
		c.profiles.Convert(record, f)
	case define.RecordFta:
		c.fta.Convert(record, f)
	case define.RecordBeat:
		c.beat.Convert(record, f)
	case define.RecordTars:
		c.tars.Convert(record, f)
	case define.RecordLogPush:
		c.logPush.Convert(record, f)
	}
}

func CleanAttributesMap(attrs map[string]any) map[string]any {
	for k := range attrs {
		if strings.TrimSpace(k) == "" {
			delete(attrs, k)
		}
	}
	return attrs
}
