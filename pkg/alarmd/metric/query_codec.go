package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus"
)

type queryCodecMetrics struct{ responses *prometheus.CounterVec }

func newQueryCodecMetrics() queryCodecMetrics {
	return queryCodecMetrics{responses: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "access_response_codec_total",
		Help: "Completed physical HTTP responses by bounded wire codec, client negotiation and completeness. Opt-in legacy is same-response compatibility, not proof of the server fallback cause. " +
			"Reader: the shared-JSON reading of the codec mix, from /metrics after each deploy.",
	}, []string{"codec", "negotiation", "completion"})}
}

func (m queryCodecMetrics) observe(o observability.Observation) {
	for _, fact := range observability.NormalizeObservation(o).QueryCodec {
		m.responses.WithLabelValues(fact.Codec, fact.Negotiation, fact.Completion).Inc()
	}
}
