// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v3"
)

const (
	pusherConfigName     = "bk_metrics_pusher"
	defaultPushBatchSize = 1024
	defaultPushPeriod    = time.Minute
	defaultTimeOffset    = 24 * time.Hour * 365 * 2 // 默认可容忍偏移时间为两年
)

type Pusher interface {
	Error() error
	Push() error
	GatherEvents() (<-chan Event, error)
	StartPeriodPush()
	Stop()
	Disabled() bool
	Gatherer(g prometheus.Gatherer) Pusher
	Collector(c prometheus.Collector) Pusher
	Client(c beat.Client) Pusher
	ConstLabels(lbs map[string]string) Pusher
}

type gsePusher struct {
	ctx        context.Context
	cancel     context.CancelFunc
	err        error
	gatherers  prometheus.Gatherers
	registerer prometheus.Registerer

	config          *PusherConfig
	constLabels     map[string]string
	remoteLabelsGot bool
	remoteLabels    map[string]string

	client beat.Client
}

func (p *gsePusher) ConstLabels(lbs map[string]string) Pusher {
	p.constLabels = lbs
	return p
}

type PusherConfig struct {
	Disabled                      bool                `config:"disabled"`
	DataID                        int                 `config:"dataid"`
	BatchSize                     int                 `config:"batch_size"`
	Period                        time.Duration       `config:"period"`
	TimeOffset                    time.Duration       `config:"time_offset"`
	Labels                        []map[string]string `config:"labels"`
	RemoteLabelsURL               string              `config:"remote_labels_url""`
	MetricRelabelConfigs          []*relabel.Config   `yaml:"metric_relabel_configs"`
	MetricRelabelConfigsInterface interface{}         `config:"metric_relabel_configs"`
}

var defaultPusherConfig = &PusherConfig{
	BatchSize:  defaultPushBatchSize,
	Period:     defaultPushPeriod,
	Labels:     []map[string]string{{}},
	TimeOffset: defaultTimeOffset,
}

func NewGsePusher(ctx context.Context, c *PusherConfig) Pusher {
	if c.BatchSize <= 0 {
		c.BatchSize = defaultPusherConfig.BatchSize
	}
	if c.Period <= 0 {
		c.Period = defaultPusherConfig.Period
	}
	if len(c.Labels) == 0 {
		c.Labels = defaultPusherConfig.Labels
	}
	if c.TimeOffset <= 0 {
		c.TimeOffset = defaultPusherConfig.TimeOffset
	}
	reg := prometheus.NewRegistry()
	ctx, cancel := context.WithCancel(ctx)
	return &gsePusher{
		ctx:        ctx,
		cancel:     cancel,
		gatherers:  prometheus.Gatherers{reg},
		registerer: reg,
		config:     c,
	}
}

// NewGsePusherWithConfig init with config
// sample config:
// bk_metrics_pusher:
//
//	dataid: 1
//	period: 60s
//	batch_size: 1024
//	labels: []
//	metric_relabel_configs:
//	  - source_labels:
//	      - __name__
//	    regex: ^(go_|process_).*
//	    action: keep
//	remote_labels_url: ""
//
// sample code:
// pusher, err := NewGsePusherWithConfig(ctx, config)
// counter := prometheus.NewCounterVec(prometheus.CounterVecOpts{}, []string{"label1"})
// // gather by prometheus.Collector
// pusher.Collector(counter)
//
// // gather by prometheus.Gatherer
// // when using prometheus.Register or prometheus.MustRegister
// pusher.Gatherer(prometheus.DefaultRegisterer)
//
// // for custom gatherer
// reg := prometheus.NewRegister()
// reg.Register(counter)
// pusher.Gatherer(reg)
//
// // push once
// pusher.Push()
// // push by period
// pusher.StartPeriodPush()
func NewGsePusherWithConfig(ctx context.Context, config *common.Config) (Pusher, error) {
	configContent, err := config.Child(pusherConfigName, -1)
	if err != nil {
		return nil, err
	}

	c := &PusherConfig{}
	err = configContent.Unpack(c)
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(c.MetricRelabelConfigsInterface)
	if err != nil {
		return nil, err
	}

	logp.Debug("pusher", "get metric relabel config:%s", data)
	err = yaml.Unmarshal(data, &c.MetricRelabelConfigs)
	if err != nil {
		return nil, err
	}
	p := NewGsePusher(ctx, c)
	return p, nil
}

func (p *gsePusher) Disabled() bool {
	return p.config.Disabled
}

func (p *gsePusher) Error() error {
	return p.err
}

func (p *gsePusher) getClient() (beat.Client, error) {
	c := p.client
	if c == nil {
		if commonBKBeat.Client != nil {
			c = *commonBKBeat.Client
		}
	}
	if c == nil {
		return nil, errors.New("client not set")
	}
	return c, nil
}

func (p *gsePusher) Push() error {
	if p.Disabled() {
		return nil
	}
	if p.err != nil {
		return p.err
	}
	if p.config.RemoteLabelsURL != "" && !p.remoteLabelsGot {
		err := p.getRemoteLabels()
		if err != nil {
			return err
		}
	}
	events, err := p.GatherEvents()
	if err != nil {
		return err
	}
	return p.sendEvents(events)
}

func (p *gsePusher) sendEvents(events <-chan Event) error {
	c, err := p.getClient()
	if err != nil {
		return err
	}
	for event := range events {
		c.Publish(event)
	}
	return nil
}

func (p *gsePusher) Gatherer(g prometheus.Gatherer) Pusher {
	p.gatherers = append(p.gatherers, g)
	return p
}

func (p *gsePusher) Collector(c prometheus.Collector) Pusher {
	if p.err == nil {
		p.err = p.registerer.Register(c)
	}
	return p
}

func (p *gsePusher) Client(c beat.Client) Pusher {
	p.client = c
	return p
}

func (p *gsePusher) GatherEvents() (<-chan Event, error) {
	mfs, err := p.gatherers.Gather()
	if err != nil {
		return nil, err
	}
	logp.Debug("pusher", "got metric families :%d", len(mfs))
	events := make(chan Event)
	go func() {
		defer close(events)
		p.metricFamiliesToEvents(mfs, events)
	}()
	return events, nil
}

func (p *gsePusher) handleTimestampMs(ts, nowTs int64) int64 {
	offset := time.Since(time.Unix(0, ts*int64(time.Millisecond)))
	// 如果上报时间在过去且时间偏差超过两年，使用当前时间
	// 当上报时间在未来，保留原本时间
	if ts == 0 || offset > p.config.TimeOffset {
		ts = nowTs
	}
	return ts
}

func getValueFromMetric(metric *dto.Metric) (interface{}, error) {
	var v float64
	if metric.GetUntyped() != nil {
		v = metric.GetUntyped().GetValue()
	} else if metric.GetCounter() != nil {
		v = metric.GetCounter().GetValue()
	} else if metric.GetGauge() != nil {
		v = metric.GetGauge().GetValue()
	} else if metric.GetHistogram() != nil {
		return nil, nil
	}
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return nil, errors.New("value is NaN or Inf")
	}
	return v, nil
}

func (p *gsePusher) processLabels(name string, lps []*dto.LabelPair) labels.Labels {
	lbs := make(labels.Labels, 0, len(lps))
	for _, lp := range lps {
		lbs = append(lbs, labels.Label{Name: lp.GetName(), Value: lp.GetValue()})
	}
	lbs = append(lbs, labels.Label{Name: model.MetricNameLabel, Value: name})
	lbs, _ = relabel.Process(lbs, p.config.MetricRelabelConfigs...)
	return lbs
}

func (p *gsePusher) dataFromLabel(
	name string, value interface{}, timestamp int64, lbs labels.Labels, labelGroup map[string]string,
) (map[string]interface{}, error) {
	data := make(map[string]interface{})
	dimension := make(map[string]string)
	for _, lb := range lbs {
		if lb.Name == model.MetricNameLabel {
			continue
		}
		dimension[lb.Name] = lb.Value
	}
	for k, v := range labelGroup {
		dimension[k] = v
	}
	if len(dimension) == 0 {
		return nil, fmt.Errorf("no dimension found for metric %s=>%v", name, value)
	}
	data["dimension"] = dimension
	data["metrics"] = map[string]interface{}{
		name: value,
	}
	data["timestamp"] = timestamp
	return data, nil
}

var nowFunc = time.Now

type metricGroup struct {
	suffix  string
	metrics []*dto.Metric
}

func (p *gsePusher) getConstLabelGroups() []map[string]string {
	constLabelGroups := make([]map[string]string, 0, len(p.config.Labels))
	for _, labelGroup := range p.config.Labels {
		constLabelGroup := make(map[string]string)
		for k, v := range labelGroup {
			constLabelGroup[k] = v
		}
		for k, v := range p.constLabels {
			constLabelGroup[k] = v
		}
		for k, v := range p.remoteLabels {
			constLabelGroup[k] = v
		}
		constLabelGroups = append(constLabelGroups, constLabelGroup)
	}
	return constLabelGroups
}

func (p *gsePusher) metricFamiliesToEvents(mfs []*dto.MetricFamily, events chan<- Event) {
	datas := make([]map[string]interface{}, 0, p.config.BatchSize)
	now := nowFunc()
	nowTimeStamp := now.UnixNano() / 1e6
	constLabelGroups := p.getConstLabelGroups()
	c := 0
	for _, mf := range mfs {
		var metricGroups []metricGroup
		if mf.GetType() == dto.MetricType_HISTOGRAM || mf.GetType() == dto.MetricType_SUMMARY {
			metricGroups = getHistogramOrSummaryMetricGroups(mf.Metric)
		} else {
			metricGroups = []metricGroup{
				{
					suffix:  "",
					metrics: mf.Metric,
				},
			}
		}
		logp.Debug("pusher", "metric: %s got metricGroups: %d", mf.GetName(), len(metricGroups))
		for _, mg := range metricGroups {
			name := mf.GetName() + mg.suffix
			for _, metric := range mg.metrics {
				value, err := getValueFromMetric(metric)
				if err != nil {
					logp.Warn("[pusher] getValueFromMetric failed: %s", name)
					continue
				}
				lbs := p.processLabels(name, metric.Label)
				if lbs == nil {
					logp.Warn("[pusher] metric: %s has not labels", name)
					continue
				}
				timestamp := p.handleTimestampMs(metric.GetTimestampMs(), nowTimeStamp)
				// 根据配置注入label
				for _, labelGroup := range constLabelGroups {
					data, err := p.dataFromLabel(name, value, timestamp, lbs, labelGroup)
					if err != nil {
						logp.Err("[pusher] metric: %s dataFromLabel failed: %v", name, err)
						continue
					}
					datas = append(datas, data)
					c++
					if len(datas) >= p.config.BatchSize {
						event := p.wrapEvent(datas, now)
						events <- event
						datas = make([]map[string]interface{}, 0, p.config.BatchSize)
					}
				}
			}
		}
	}
	if len(datas) >= 0 {
		event := p.wrapEvent(datas, now)
		events <- event
	}
	logp.Debug("pusher", "pusher sent metrics: %d", c)
}

func getFloatString(v float64) string {
	if math.IsInf(v, +1) {
		return "+Inf"
	}
	return fmt.Sprint(v)
}

func getHistogramOrSummaryMetricGroups(metrics []*dto.Metric) []metricGroup {
	result := make([]metricGroup, 0)
	for _, metric := range metrics {
		if metric.GetHistogram() == nil && metric.GetSummary() == nil {
			continue
		}
		h := metric.GetHistogram()
		s := metric.GetSummary()
		if h != nil {
			buckets := make([]*dto.Metric, 0, len(h.GetBucket()))
			infSeen := false
			for _, bucket := range h.GetBucket() {
				bucketValue := float64(bucket.GetCumulativeCount())

				bucketLabel := append(metric.Label, &dto.LabelPair{
					Name:  proto.String(model.BucketLabel),
					Value: proto.String(getFloatString(bucket.GetUpperBound())),
				})
				buckets = append(buckets, &dto.Metric{
					Label: bucketLabel,
					Untyped: &dto.Untyped{
						Value: &bucketValue,
					},
					TimestampMs: metric.TimestampMs,
				})
				if math.IsInf(bucket.GetUpperBound(), +1) {
					infSeen = true
				}
			}
			if !infSeen {
				bucketValue := float64(h.GetSampleCount())
				bucketLabel := append(metric.Label, &dto.LabelPair{
					Name:  proto.String(model.BucketLabel),
					Value: proto.String(getFloatString(math.Inf(+1))),
				})
				buckets = append(buckets, &dto.Metric{
					Label: bucketLabel,
					Untyped: &dto.Untyped{
						Value: &bucketValue,
					},
					TimestampMs: metric.TimestampMs,
				})
			}
			result = append(result, metricGroup{
				suffix:  "_bucket",
				metrics: buckets,
			})
		}
		if s != nil {
			quantiles := make([]*dto.Metric, 0, len(s.GetQuantile()))
			for _, quantile := range s.GetQuantile() {
				quantileLabel := append(metric.Label, &dto.LabelPair{
					Name:  proto.String(model.QuantileLabel),
					Value: proto.String(getFloatString(quantile.GetQuantile())),
				})
				quantiles = append(quantiles, &dto.Metric{
					Label: quantileLabel,
					Untyped: &dto.Untyped{
						Value: quantile.Value,
					},
					TimestampMs: metric.TimestampMs,
				})
			}
			result = append(result, metricGroup{
				suffix:  "",
				metrics: quantiles,
			})
		}
		var countValue float64
		if h != nil {
			countValue = float64(h.GetSampleCount())
		}
		if s != nil {
			countValue = float64(s.GetSampleCount())
		}
		result = append(result, metricGroup{
			suffix: "_count",
			metrics: []*dto.Metric{
				{
					Label: metric.Label,
					Untyped: &dto.Untyped{
						Value: &countValue,
					},
					TimestampMs: metric.TimestampMs,
				},
			},
		})
		var sumValue float64
		if h != nil {
			sumValue = h.GetSampleSum()
		}
		if s != nil {
			sumValue = s.GetSampleSum()
		}
		result = append(result, metricGroup{
			suffix: "_sum",
			metrics: []*dto.Metric{
				{
					Label: metric.Label,
					Untyped: &dto.Untyped{
						Value: &sumValue,
					},
					TimestampMs: metric.TimestampMs,
				},
			},
		})
	}
	return result
}

func (p *gsePusher) wrapEvent(datas []map[string]interface{}, t time.Time) Event {
	return beat.Event{
		Fields: MapStr{
			"dataid":    p.config.DataID,
			"time":      t.Unix(),
			"data":      datas,
			"timestamp": t.Unix(),
		},
		Timestamp: t,
	}
}

func (p *gsePusher) StartPeriodPush() {
	if p.Disabled() {
		return
	}
	ticker := time.NewTicker(p.config.Period)
	err := p.Push()
	if err != nil {
		logp.Err("[pusher] push failed: %v", err)
	}
	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				err := p.Push()
				if err != nil {
					logp.Err("[pusher] push failed: %v", err)
				}
			case <-p.ctx.Done():
				return
			}
		}
	}()
}

func (p *gsePusher) Stop() {
	if p.Disabled() {
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *gsePusher) getRemoteLabels() error {
	if p.config.RemoteLabelsURL == "" {
		return nil
	}
	rsp, err := http.Get(p.config.RemoteLabelsURL)
	if err != nil {
		return err
	}
	bs, err := io.ReadAll(rsp.Body)
	if err != nil {
		return err
	}
	remoteLabels := make(map[string]string)
	err = json.Unmarshal(bs, &remoteLabels)
	if err != nil {
		return err
	}
	p.remoteLabels = remoteLabels
	p.remoteLabelsGot = true
	logp.Debug("pusher", "got remote lables: %v", remoteLabels)
	return nil
}
