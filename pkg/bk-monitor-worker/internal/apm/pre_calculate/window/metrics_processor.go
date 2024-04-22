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

func (m *MetricProcessor) process(receiver chan<- storage.SaveRequest, fullTreeGraph *DiGraph) {
	parentChildMetricCount := m.findParentChildMetric(receiver, fullTreeGraph)

	metrics.RecordApmRelationMetricFindCount(m.dataId, metrics.RelationMetricSystem, parentChildMetricCount)
}

// findParentChildMetric find the metrics which contains c-s relation
// include: system <-> system / system <-> service / service <-> service
func (m *MetricProcessor) findParentChildMetric(receiver chan<- storage.SaveRequest, fullTreeGraph *DiGraph) int {

	count := 0
	ts := time.Now().UnixNano() / int64(time.Millisecond)
	for _, pair := range fullTreeGraph.FindParentChildPairs() {
		var series []prompb.TimeSeries

		cService := pair[0].GetFieldValue(core.ServiceNameField)
		sService := pair[1].GetFieldValue(core.ServiceNameField)
		parentIp := pair[0].GetFieldValue(core.NetHostIpField, core.HostIpField)
		childIp := pair[1].GetFieldValue(core.NetHostIpField, core.HostIpField)

		if cService != "" && sService != "" {
			// --> Find service -> service relation
			series = append(series, prompb.TimeSeries{
				Labels: []prompb.Label{
					{
						Name:  "__name__",
						Value: "service_to_service_flow",
					},
					{
						Name:  "from_service_name",
						Value: cService,
					},
					{
						Name:  "to_service_name",
						Value: sService,
					},
				},
				Samples: []prompb.Sample{{Value: 1, Timestamp: ts}},
			})
		}
		if parentIp != "" {
			// ----> Find system -> service relation
			series = append(series, prompb.TimeSeries{
				Labels: []prompb.Label{
					{
						Name:  "__name__",
						Value: "system_to_service_flow",
					},
					{
						Name:  "from_bk_target_ip",
						Value: parentIp,
					},
					{
						Name:  "to_service_name",
						Value: sService,
					},
				},
				Samples: []prompb.Sample{{Value: 1, Timestamp: ts}},
			})
		}
		if childIp != "" {
			// ----> Find service -> system relation
			series = append(series, prompb.TimeSeries{
				Labels: []prompb.Label{
					{
						Name:  "__name__",
						Value: "service_to_system_flow",
					},
					{
						Name:  "from_service_name",
						Value: cService,
					},
					{
						Name:  "to_bk_target_ip",
						Value: childIp,
					},
				},
				Samples: []prompb.Sample{{Value: 1, Timestamp: ts}},
			})
		}
		if parentIp != "" && childIp != "" {
			// ----> find system -> system relation
			series = append(series, prompb.TimeSeries{
				Labels: []prompb.Label{
					{Name: "__name__", Value: "system_to_system_flow"},
					{Name: "from_bk_target_ip", Value: parentIp},
					{Name: "to_ip", Value: childIp},
				},
				Samples: []prompb.Sample{{Value: 1, Timestamp: ts}},
			})
		}

		if len(series) > 0 {
			receiver <- storage.SaveRequest{
				Target: storage.Prometheus,
				Data:   storage.PrometheusStorageData{Value: series},
			}
		}
		count += len(series)
	}

	return count
}

func newMetricProcessor(dataId string) MetricProcessor {
	return MetricProcessor{dataId: dataId}
}
