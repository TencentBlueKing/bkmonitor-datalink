package aegisv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestDecodeOTLPHistogramMetrics(t *testing.T) {
	// 标准 OTLP Histogram metrics payload
	buf := []byte(`{
		"resourceMetrics": [{
			"resource": {
				"attributes": [
					{"key": "deployment.environment.name", "value": {"stringValue": "production"}},
					{"key": "rum.provider", "value": {"stringValue": "blueking"}},
					{"key": "service.name", "value": {"stringValue": "demo-app"}},
					{"key": "service.version", "value": {"stringValue": "1.0.0"}},
					{"key": "telemetry.sdk.language", "value": {"stringValue": "webjs"}}
				]
			},
			"scopeMetrics": [{
				"scope": {"name": "bk-rum", "version": ""},
				"metrics": [{
					"name": "browser.web_vital.duration",
					"description": "Web Vitals duration metrics, including FCP, INP, LCP and TTFB",
					"unit": "ms",
					"histogram": {
						"aggregationTemporality": 2,
						"dataPoints": [
							{
								"attributes": [
									{"key": "rum.page.host", "value": {"stringValue": "apps.paas3-dev.bktencent.com"}},
									{"key": "rum.page.path", "value": {"stringValue": "/otelfrontenddemo/"}},
									{"key": "rum.navigation.type", "value": {"stringValue": "reload"}},
									{"key": "vital.metric", "value": {"stringValue": "fcp"}},
									{"key": "vital.rating", "value": {"stringValue": "good"}}
								],
								"bucketCounts": [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0],
								"explicitBounds": [0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000],
								"count": 1,
								"sum": 1536,
								"min": 1536,
								"max": 1536,
								"startTimeUnixNano": "1782215103034000000",
								"timeUnixNano": "1782215162938000000"
							},
							{
								"attributes": [
									{"key": "rum.page.host", "value": {"stringValue": "apps.paas3-dev.bktencent.com"}},
									{"key": "rum.page.path", "value": {"stringValue": "/otelfrontenddemo/"}},
									{"key": "rum.navigation.type", "value": {"stringValue": "reload"}},
									{"key": "vital.metric", "value": {"stringValue": "ttfb"}},
									{"key": "vital.rating", "value": {"stringValue": "good"}}
								],
								"bucketCounts": [0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
								"explicitBounds": [0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000],
								"count": 1,
								"sum": 46,
								"min": 46,
								"max": 46,
								"startTimeUnixNano": "1782215103125000000",
								"timeUnixNano": "1782215162938000000"
							}
						]
					}
				}]
			}]
		}]
	}`)

	encoder := JsonEncoder()
	metrics, err := encoder.UnmarshalMetrics(buf)
	require.NoError(t, err)

	// Verify structure
	require.Equal(t, 1, metrics.ResourceMetrics().Len())
	rm := metrics.ResourceMetrics().At(0)

	// Resource attributes
	rAttrs := rm.Resource().Attributes()
	svc, _ := rAttrs.Get("service.name")
	assert.Equal(t, "demo-app", svc.AsString())

	// Scope metrics
	require.Equal(t, 1, rm.ScopeMetrics().Len())
	sm := rm.ScopeMetrics().At(0)
	assert.Equal(t, "bk-rum", sm.Scope().Name())

	// Metrics
	require.Equal(t, 1, sm.Metrics().Len())
	m := sm.Metrics().At(0)
	assert.Equal(t, "browser.web_vital.duration", m.Name())
	assert.Equal(t, "ms", m.Unit())
	assert.Equal(t, pmetric.MetricDataTypeHistogram, m.DataType())

	// Histogram data points
	hist := m.Histogram()
	assert.Equal(t, 2, hist.DataPoints().Len())

	// FCP data point
	dp0 := hist.DataPoints().At(0)
	fcpMetric, _ := dp0.Attributes().Get("vital.metric")
	assert.Equal(t, "fcp", fcpMetric.AsString())
	assert.Equal(t, uint64(1), dp0.Count())
	assert.Equal(t, float64(1536), dp0.Sum())

	// TTFB data point
	dp1 := hist.DataPoints().At(1)
	ttfbMetric, _ := dp1.Attributes().Get("vital.metric")
	assert.Equal(t, "ttfb", ttfbMetric.AsString())
	assert.Equal(t, uint64(1), dp1.Count())
	assert.Equal(t, float64(46), dp1.Sum())
}

func TestDecodeAegisV2Metrics_IsNotOTLP(t *testing.T) {
	// AegisV2 format should return handled=true with isAegisV2 check
	aegisV2Buf := []byte(`{
		"topic": "SDK-test",
		"scheme": "v2",
		"bean": {"version": "1.0.0"},
		"d2": [{
			"fields": {"type": "web_vitals", "level": "info"},
			"message": [{"msg": "web_vitals", "FCP": 100, "timestamp": 1000}]
		}]
	}`)

	encoder := JsonEncoder()
	metrics, err := encoder.UnmarshalMetrics(aegisV2Buf)
	require.NoError(t, err)

	// Should get Histogram format from aegisV2 splitMetrics
	require.Equal(t, 1, metrics.ResourceMetrics().Len())
	sm := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0)
	require.Equal(t, 1, sm.Metrics().Len())
	m := sm.Metrics().At(0)
	assert.Equal(t, "browser.web_vital.duration", m.Name())
	assert.Equal(t, pmetric.MetricDataTypeHistogram, m.DataType())
}
