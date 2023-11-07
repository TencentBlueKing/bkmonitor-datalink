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
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/golang/protobuf/ptypes/timestamp"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	fakeTs     int64   = 1024
	fakeValue  float64 = 10001
	fakeString         = "fake_string"
	traceLabel         = "traceID"
	spanLabel          = "span_id"
)

type testCase struct {
	Family *dto.MetricFamily
	Event  []define.Event
}

func TestConvertPushGatewayData(t *testing.T) {
	pd := &define.PushGatewayData{
		MetricFamilies: &dto.MetricFamily{
			Name: proto.String("http_request_duration_microseconds"),
			Help: proto.String("foo"),
			Type: dto.MetricType_COUNTER.Enum(),
			Metric: []*dto.Metric{{
				Label: []*dto.LabelPair{
					{
						Name:  proto.String("handler"),
						Value: proto.String("query"),
					},
				},
				Counter: &dto.Counter{
					Value: proto.Float64(10),
				},
				TimestampMs: &fakeTs,
			}},
		},
	}

	events := make([]define.Event, 0)
	NewCommonConverter().Convert(&define.Record{
		RecordType: define.RecordPushGateway,
		Data:       pd,
	}, func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordPushGateway, evt.RecordType())
			assert.Equal(t, int32(0), evt.DataId())
			events = append(events, evt)
		}
	})

	assert.Len(t, events, 1)
}

func TestGetPushGatewayEventsFromMetricFamily(t *testing.T) {
	t.Run("convertCounter1", func(t *testing.T) {
		c := &pushGatewayConverter{}
		labels := map[string]string{
			"handler": "query",
		}
		input := testCase{
			Family: &dto.MetricFamily{
				Name: proto.String("http_request_duration_microseconds"),
				Help: proto.String("foo"),
				Type: dto.MetricType_COUNTER.Enum(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("handler"),
							Value: proto.String("query"),
						},
					},
					Counter: &dto.Counter{
						Value: proto.Float64(10),
					},
					TimestampMs: &fakeTs,
				}},
			},
			Event: []define.Event{
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds": float64(10),
					},
					"target":    "unknown",
					"dimension": labels,
					"timestamp": fakeTs,
				}),
			},
		}

		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}
		c.publishEventsFromMetricFamily(define.Token{}, &define.PushGatewayData{MetricFamilies: input.Family}, 0, fakeTs, gather)
		assert.Equal(t, input.Event, events)

		id := c.ToDataID(&define.Record{
			Token: define.Token{MetricsDataId: 10011},
		})
		assert.Equal(t, int32(10011), id)
	})

	t.Run("convertCounter2", func(t *testing.T) {
		c := &pushGatewayConverter{}
		labels := map[string]string{
			"handler": "query",
		}
		input := testCase{
			Family: &dto.MetricFamily{
				Name: proto.String("http_request_duration_microseconds_with_exemplar"),
				Help: proto.String("foo"),
				Type: dto.MetricType_COUNTER.Enum(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("handler"),
							Value: proto.String("query"),
						},
					},
					Counter: &dto.Counter{
						Value: proto.Float64(10),
						Exemplar: &dto.Exemplar{
							Label: []*dto.LabelPair{
								{
									Name: &traceLabel, Value: &fakeString,
								},
								{
									Name: &spanLabel, Value: &fakeString,
								},
							},
							Value:     &fakeValue,
							Timestamp: &timestamp.Timestamp{},
						},
					},
					TimestampMs: &fakeTs,
				}},
			},
			Event: []define.Event{
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_with_exemplar": float64(10),
					},
					"target":    "unknown",
					"dimension": labels,
					"timestamp": fakeTs,
					"exemplar": common.MapStr{
						"bk_span_id":         "fake_string",
						"bk_trace_id":        "fake_string",
						"bk_trace_timestamp": int64(0),
						"bk_trace_value":     float64(10001),
					},
				}),
			},
		}

		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}
		c.publishEventsFromMetricFamily(define.Token{}, &define.PushGatewayData{MetricFamilies: input.Family}, 0, fakeTs, gather)
		assert.Equal(t, input.Event, events)
	})

	t.Run("convertGauge1", func(t *testing.T) {
		c := &pushGatewayConverter{}
		labels := map[string]string{
			"handler": "query",
		}
		input := testCase{
			Family: &dto.MetricFamily{
				Name: proto.String("http_request_duration_microseconds"),
				Help: proto.String("foo"),
				Type: dto.MetricType_GAUGE.Enum(),
				Metric: []*dto.Metric{{
					Gauge: &dto.Gauge{
						Value: proto.Float64(10),
					},
					TimestampMs: &fakeTs,
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("handler"),
							Value: proto.String("query"),
						},
					},
				}},
			},
			Event: []define.Event{
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds": float64(10),
					},
					"target":    "unknown",
					"timestamp": fakeTs,
					"dimension": labels,
				}),
			},
		}

		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}
		c.publishEventsFromMetricFamily(define.Token{}, &define.PushGatewayData{MetricFamilies: input.Family}, 0, fakeTs, gather)
		assert.Equal(t, input.Event, events)
	})

	t.Run("convertHistogram1", func(t *testing.T) {
		c := &pushGatewayConverter{}
		input := testCase{
			Family: &dto.MetricFamily{
				Name: proto.String("http_request_duration_microseconds"),
				Help: proto.String("foo"),
				Type: dto.MetricType_HISTOGRAM.Enum(),
				Metric: []*dto.Metric{{
					Histogram: &dto.Histogram{
						SampleCount: proto.Uint64(10),
						SampleSum:   proto.Float64(10),
						Bucket: []*dto.Bucket{
							{
								UpperBound:      proto.Float64(0.99),
								CumulativeCount: proto.Uint64(10),
								Exemplar: &dto.Exemplar{
									Label: []*dto.LabelPair{
										{
											Name: &traceLabel, Value: &fakeString,
										},
										{
											Name: &spanLabel, Value: &fakeString,
										},
									},
									Value:     &fakeValue,
									Timestamp: &timestamp.Timestamp{},
								},
							},
						},
					},
				}},
			},
			Event: []define.Event{
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_count": uint64(10),
						"http_request_duration_microseconds_sum":   float64(10),
					},
					"timestamp": fakeTs,
					"target":    "unknown",
					"dimension": map[string]string{},
				}),
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_bucket": uint64(10),
					},
					"dimension": map[string]string{
						"le": "0.99",
					},
					"target":    "unknown",
					"timestamp": fakeTs,
					"exemplar": common.MapStr{
						"bk_span_id":         "fake_string",
						"bk_trace_id":        "fake_string",
						"bk_trace_timestamp": int64(0),
						"bk_trace_value":     float64(10001),
					},
				}),
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_bucket": uint64(10),
					},
					"dimension": map[string]string{
						"le": "+Inf",
					},
					"target":    "unknown",
					"timestamp": fakeTs,
				}),
			},
		}

		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}
		c.publishEventsFromMetricFamily(define.Token{}, &define.PushGatewayData{MetricFamilies: input.Family}, 0, fakeTs, gather)
		assert.Equal(t, input.Event, events)
	})

	t.Run("convertHistogram2", func(t *testing.T) {
		c := &pushGatewayConverter{}
		input := testCase{
			Family: &dto.MetricFamily{
				Name: proto.String("http_request_duration_microseconds_with_exemplar"),
				Help: proto.String("foo"),
				Type: dto.MetricType_HISTOGRAM.Enum(),
				Metric: []*dto.Metric{{
					Histogram: &dto.Histogram{
						SampleCount: proto.Uint64(10),
						SampleSum:   proto.Float64(10),
						Bucket: []*dto.Bucket{
							{
								UpperBound:      proto.Float64(0.99),
								CumulativeCount: proto.Uint64(10),
							},
						},
					},
				}},
			},
			Event: []define.Event{
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_with_exemplar_count": uint64(10),
						"http_request_duration_microseconds_with_exemplar_sum":   float64(10),
					},
					"timestamp": fakeTs,
					"target":    "unknown",
					"dimension": map[string]string{},
				}),
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_with_exemplar_bucket": uint64(10),
					},
					"dimension": map[string]string{
						"le": "0.99",
					},
					"target":    "unknown",
					"timestamp": fakeTs,
				}),
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_with_exemplar_bucket": uint64(10),
					},
					"dimension": map[string]string{
						"le": "+Inf",
					},
					"target":    "unknown",
					"timestamp": fakeTs,
				}),
			},
		}

		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}
		c.publishEventsFromMetricFamily(define.Token{}, &define.PushGatewayData{MetricFamilies: input.Family}, 0, fakeTs, gather)
		assert.Equal(t, input.Event, events)
	})

	t.Run("convertUntyped1", func(t *testing.T) {
		labels := map[string]string{
			"handler": "query",
		}
		c := &pushGatewayConverter{}
		input := testCase{
			Family: &dto.MetricFamily{
				Name: proto.String("http_request_duration_microseconds"),
				Help: proto.String("foo"),
				Type: dto.MetricType_UNTYPED.Enum(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("handler"),
							Value: proto.String("query"),
						},
					},
					Untyped: &dto.Untyped{
						Value: proto.Float64(10),
					},
				}},
			},
			Event: []define.Event{
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds": float64(10),
					},
					"target":    "unknown",
					"dimension": labels,
					"timestamp": fakeTs,
				}),
			},
		}

		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}
		c.publishEventsFromMetricFamily(define.Token{}, &define.PushGatewayData{MetricFamilies: input.Family}, 0, fakeTs, gather)
		assert.Equal(t, input.Event, events)
	})

	t.Run("convertSummary1", func(t *testing.T) {
		labels := map[string]string{
			"handler": "query",
		}
		c := &pushGatewayConverter{}
		input := testCase{
			Family: &dto.MetricFamily{
				Name: proto.String("http_request_duration_microseconds"),
				Help: proto.String("foo"),
				Type: dto.MetricType_SUMMARY.Enum(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("handler"),
							Value: proto.String("query"),
						},
					},
					Summary: &dto.Summary{
						SampleCount: proto.Uint64(10),
						SampleSum:   proto.Float64(10),
						Quantile: []*dto.Quantile{
							{
								Quantile: proto.Float64(10),
								Value:    proto.Float64(10),
							},
						},
					},
				}},
			},
			Event: []define.Event{
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds_count": uint64(10),
						"http_request_duration_microseconds_sum":   float64(10),
					},
					"target":    "unknown",
					"dimension": labels,
					"timestamp": fakeTs,
				}),
				c.ToEvent(define.Token{}, 0, common.MapStr{
					"metrics": common.MapStr{
						"http_request_duration_microseconds": float64(10),
					},
					"dimension": map[string]string{
						"handler":  "query",
						"quantile": "10",
					},
					"target":    "unknown",
					"timestamp": fakeTs,
				}),
			},
		}

		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}
		c.publishEventsFromMetricFamily(define.Token{}, &define.PushGatewayData{MetricFamilies: input.Family}, 0, fakeTs, gather)
		assert.Equal(t, input.Event, events)
	})
}
