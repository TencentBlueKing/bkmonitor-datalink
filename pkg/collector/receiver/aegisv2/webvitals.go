package aegisv2

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// OTel 标准直方图桶边界（毫秒级，用于时间指标：FCP/LCP/FID/INP）
var histogramBoundsMS = []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}

// OTel 标准直方图桶边界（比例级，用于无量纲指标：CLS）
var histogramBoundsRatio = []float64{0, 0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 1.0}

var webVitalsDefs = []struct{ metric, key string }{
	{"fcp", "FCP"},
	{"lcp", "LCP"},
	{"fid", "FID"},
	{"inp", "INP"},
	{"ttfb", "TTFB"},
	{"cls", "CLS"},
}

// webVitalsDurationDefs 仅包含时长类指标（单位 ms）。
var webVitalsDurationDefs = []struct{ metric, key string }{
	{"fcp", "FCP"},
	{"lcp", "LCP"},
	{"fid", "FID"},
	{"inp", "INP"},
	{"ttfb", "TTFB"},
}

// vitalThresholds 定义 Web Vital 指标的质量阈值
type vitalThresholds struct {
	good             float64
	needsImprovement float64
}

var vitalRatingConfig = map[string]vitalThresholds{
	"fcp":  {1800, 3000}, // FCP: Good ≤ 1.8s, Needs Improvement ≤ 3s
	"lcp":  {2500, 4000}, // LCP: Good ≤ 2.5s, Needs Improvement ≤ 4s
	"fid":  {100, 300},   // FID: Good ≤ 100ms, Needs Improvement ≤ 300ms
	"inp":  {200, 500},   // INP: Good ≤ 200ms, Needs Improvement ≤ 500ms
	"ttfb": {800, 1800},  // TTFB: Good ≤ 0.8s, Needs Improvement ≤ 1.8s
	"cls":  {0.1, 0.25},  // CLS: Good ≤ 0.1, Needs Improvement ≤ 0.25
}

// appendWebVitalsSpans 将一条 web_vitals 消息拆解为每个指标各自独立的 Span。
// 值为 -1 表示客户端未采集，跳过不写。
func appendWebVitalsSpans(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, now pcommon.Timestamp, payload collectPayload, record d2Record, msg d2Message) {
	timestamp := millisToTimestamp(msg.Timestamp, now)
	gotoID := firstNonEmptyString(msg.raw, "aegisv2_goto")
	pageURL := recordPageURL(record)

	for _, v := range webVitalsDefs {
		value, ok := extractFloat64(msg.raw, v.key)
		if !ok || value < 0 {
			continue
		}

		span := appendSpan(scopeSpans, traceID, spanNameBrowserVital, ptrace.SpanKindInternal, timestamp, timestamp)
		putWebVitalSpanAttrs(span.Attributes(), payload, record, msg.Timestamp, v.metric, value, gotoID, pageURL)
	}
}

func putWebVitalSpanAttrs(attrs pcommon.Map, payload collectPayload, record d2Record, timestamp int64, metric string, value any, gotoID, pageURL string) {
	putCommonSpanAttrs(attrs, payload, record)
	upsertNonZeroInt(attrs, attrEventTimestamp, timestamp)
	upsertString(attrs, "span_type", "vital")
	upsertString(attrs, "span_subtype", metric)
	upsertString(attrs, "event_label", "Web 指标")
	putResultAttrs(attrs, "success", "none")
	upsertString(attrs, "target_label", metric)
	upsertAny(attrs, "target_value", value)
	upsertString(attrs, "vital.metric", metric)
	upsertAny(attrs, "vital.value", value)
	if gotoID != "" {
		upsertString(attrs, "vital.id", gotoID+"."+metric)
	}
	putFullPageAttrs(attrs, pageURL)
	putBrowserContextAttrs(attrs, payload)
}

type webVitalsCollector struct {
	data []webVitalDataPoint
}

func newWebVitalsCollector() *webVitalsCollector {
	return &webVitalsCollector{data: make([]webVitalDataPoint, 0, 32)}
}

func (c *webVitalsCollector) collect(event aegisEvent, timestamp pcommon.Timestamp, pageURL, netType string) {
	for _, def := range webVitalsDurationDefs {
		value, ok := extractFloat64(event.msg.raw, def.key)
		if !ok || value < 0 {
			continue
		}
		c.data = append(c.data, webVitalDataPoint{
			timestamp:   timestamp,
			metricName:  def.metric,
			value:       value,
			sessionID:   event.record.Fields.Session.ID,
			viewID:      event.record.Fields.View.ID,
			viewName:    event.record.Fields.View.ViewName,
			loadingType: event.record.Fields.View.LoadingType,
			viewURL:     event.record.Fields.View.ViewURL,
			pageURL:     pageURL,
			netType:     netType,
		})
	}
}

func (c *webVitalsCollector) export(scopeMetrics pmetric.ScopeMetrics) error {
	if len(c.data) == 0 {
		return nil
	}

	histogram := newWebVitalsHistogram(scopeMetrics)

	for _, data := range c.data {
		data.appendTo(histogram)
	}

	return nil
}

func newWebVitalsHistogram(scopeMetrics pmetric.ScopeMetrics) pmetric.Histogram {
	histogramMetric := scopeMetrics.Metrics().AppendEmpty()
	histogramMetric.SetName("browser.web_vital.duration")
	histogramMetric.SetDescription("Web Vitals duration metrics from aegisv2")
	histogramMetric.SetUnit("ms")
	histogramMetric.SetDataType(pmetric.MetricDataTypeHistogram)
	return histogramMetric.Histogram()
}

type webVitalDataPoint struct {
	timestamp   pcommon.Timestamp
	metricName  string
	value       float64
	sessionID   string
	viewID      string
	viewName    string
	loadingType string
	viewURL     string
	pageURL     string
	netType     string
}

func (d webVitalDataPoint) appendTo(histogram pmetric.Histogram) {
	dataPoint := histogram.DataPoints().AppendEmpty()
	dataPoint.SetStartTimestamp(d.timestamp)
	dataPoint.SetTimestamp(d.timestamp)
	dataPoint.SetCount(1)
	dataPoint.SetSum(d.value)
	dataPoint.SetMin(d.value)
	dataPoint.SetMax(d.value)

	d.fillAttrs(dataPoint.Attributes())
	bounds := d.bounds()
	dataPoint.SetMExplicitBounds(bounds)
	dataPoint.SetMBucketCounts(bucketCountsForValue(d.value, bounds))
}

func (d webVitalDataPoint) fillAttrs(attrs pcommon.Map) {
	upsertString(attrs, "session.id", d.sessionID)
	upsertString(attrs, "view.id", d.viewID)
	upsertString(attrs, "view.name", d.viewName)
	upsertString(attrs, "view.url", d.viewURL)
	upsertString(attrs, "vital.metric", d.metricName)
	upsertString(attrs, "vital.rating", webVitalRating(d.metricName, d.value))
	if d.pageURL != "" {
		upsertString(attrs, "url.full", d.pageURL)
	}
	if d.netType != "" {
		upsertString(attrs, "network.effective_type", strings.ToLower(d.netType))
	}
}

func (d webVitalDataPoint) bounds() []float64 {
	if d.metricName == "cls" {
		return histogramBoundsRatio
	}
	return histogramBoundsMS
}

func bucketCountsForValue(value float64, bounds []float64) []uint64 {
	bucketCounts := make([]uint64, len(bounds)+1)
	bucketCounts[findBucketIndex(value, bounds)] = 1
	return bucketCounts
}

// findBucketIndex 根据值找出在直方图中的桶索引
func findBucketIndex(value float64, bounds []float64) int {
	for i := 0; i < len(bounds); i++ {
		if value < bounds[i] {
			return i
		}
	}
	return len(bounds)
}

// webVitalRating 根据指标类型和值返回评级（good/needs improvement/poor）
func webVitalRating(metric string, value float64) string {
	thresholds, ok := vitalRatingConfig[metric]
	if !ok {
		return "unknown"
	}
	if value <= thresholds.good {
		return "good"
	}
	if value <= thresholds.needsImprovement {
		return "needs improvement"
	}
	return "poor"
}
