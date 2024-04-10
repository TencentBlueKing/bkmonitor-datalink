package window

import (
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
)

type MetricProcessor struct {
	dataId    string
	dpReqChan chan []prompb.TimeSeries
}

func (m *MetricProcessor) process(receiver chan<- storage.SaveRequest, event Event, fullTreeGraph *DiGraph) {
	systemFlowMetricCount := m.findSystemFlowMetric(receiver, event, fullTreeGraph)

	metrics.RecordApmRelationMetricFindCount(m.dataId, metrics.RelationMetricSystem, systemFlowMetricCount)
}

func (m *MetricProcessor) findSystemFlowMetric(receiver chan<- storage.SaveRequest, event Event, fullTreeGraph *DiGraph) int {
	requiredFields := []core.CommonField{core.NetHostIpField, core.HostIpField}
	filterTreeGraph := NewDiGraph()
	for index, span := range event.Spans {
		for _, field := range requiredFields {
			if field.Contain(span.Collections) {
				filterTreeGraph.AddNode(&Node{StandardSpan: span})
				logger.Debugf("[SystemFlowMetric] found field: %s in span[%d]", field.FullKey, index)
			}
		}
	}

	if filterTreeGraph.Empty() {
		logger.Debugf("[SystemFlowMetric] all span don't have IP field in traceId: %s", event.TraceId)
		return 0
	}
	filterTreeGraph.RefreshEdges()

	var series []prompb.TimeSeries
	for _, pair := range FindChildPairsBasedFullTree(fullTreeGraph, filterTreeGraph) {
		parentIp := pair[0].GetFieldValue(core.NetHostIpField, core.HostIpField)
		childIp := pair[1].GetFieldValue(core.NetHostIpField, core.HostIpField)

		point := prompb.TimeSeries{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "system_to_system_flow"},
				{Name: "from_ip", Value: parentIp},
				{Name: "to_ip", Value: childIp},
			},
			Samples: []prompb.Sample{
				{Value: 1, Timestamp: time.Now().UnixNano() / int64(time.Millisecond)},
			},
		}
		series = append(series, point)
	}

	receiver <- storage.SaveRequest{
		Target: storage.Prometheus,
		Data: storage.PrometheusStorageData{
			Value: series,
		},
	}

	return len(series)
}

func newMetricProcessor(dataId string) MetricProcessor {
	return MetricProcessor{dataId: dataId}
}
