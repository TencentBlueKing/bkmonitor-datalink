// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/elastic/beats/libbeat/common"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/textparse"
)

// PromEvent store the lines prometheus data
type PromEvent struct {
	Key             string
	Value           float64
	Labels          common.MapStr
	AggreValue      common.MapStr
	DimensionString string // ordered dimension string
	HashKey         string
	TS              int64
	Exemplar        *exemplar.Exemplar
}

// exemplarString returns exemplar string in a fixed order
func (pe *PromEvent) exemplarString() string {
	if pe.Exemplar == nil {
		return ""
	}
	return HashLabels(pe.Exemplar.Labels)
}

// GetAggreValue get same timestamp and same dimension metrics
func (pe *PromEvent) GetAggreValue() common.MapStr {
	return pe.AggreValue
}

// GetLabels get dimensions
func (pe *PromEvent) GetLabels() common.MapStr {
	return pe.Labels
}

// ProduceHashKey the hash of dimension map string
func (pe *PromEvent) ProduceHashKey() {
	h := xxhash.Sum64([]byte(pe.DimensionString + pe.exemplarString()))
	pe.HashKey = strconv.FormatUint(h, 10)
}

// GetTimestamp get the timestamp of the metric or local time
func (pe *PromEvent) GetTimestamp() int64 {
	return pe.TS
}

func HashLabels(lbs labels.Labels) string {
	return strconv.FormatUint(hashLabels(lbs), 10)
}

var bytesPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, 1024)
	},
}

var seps = []byte{'\xff'}

func hashLabels(lbs labels.Labels) uint64 {
	sort.Sort(lbs) // 排序保证 hash key 稳定性
	b := bytesPool.Get().([]byte)
	b = b[:0]
	for _, v := range lbs {
		b = append(b, v.Name...)
		b = append(b, seps[0])
		b = append(b, v.Value...)
		b = append(b, seps[0])
	}
	h := xxhash.Sum64(b)
	b = b[:0]
	bytesPool.Put(b) // nolint:staticcheck
	return h
}

// NewPromEvent 优先使用 V2 作为解析方式 出错再回退到 V1 还出错的话就抛出异常
func NewPromEvent(line string, ts int64, offsetTime time.Duration, handler TimestampHandler) (PromEvent, error) {
	pe, err := NewPromEventV2(line, ts, offsetTime, handler)
	if err == nil {
		return pe, nil
	}

	return NewPromEventV1(line, ts, offsetTime, handler)
}

func NewPromEventV2(line string, ts int64, offsetTime time.Duration, handler TimestampHandler) (PromEvent, error) {
	// \n 为解析分隔符
	if !strings.HasSuffix(line, "\n") {
		line = line + "\n"
	}

	var pe PromEvent
	parser := textparse.NewOpenMetricsParser([]byte(line))
	entry, err := parser.Next()
	if err != nil {
		return pe, err
	}

	switch entry {
	case textparse.EntrySeries:
		_, timestamp, val := parser.Series()
		if math.IsInf(val, 0) || math.IsNaN(val) {
			return pe, errors.New("value is NaN or Inf")
		}

		var lbs labels.Labels
		var epr exemplar.Exemplar
		parser.Metric(&lbs)
		if found := parser.Exemplar(&epr); found {
			pe.Exemplar = &epr
			pe.Exemplar.Ts = handler(ts, pe.Exemplar.Ts, offsetTime)
		}

		var peTs int64
		if timestamp == nil {
			peTs = handler(ts, ts, offsetTime)
		} else {
			peTs = handler(ts, *timestamp, offsetTime)
		}

		labelsMap := make(common.MapStr, len(lbs))
		newLbs := make(labels.Labels, 0)
		for _, lb := range lbs {
			if lb.Name == "__name__" {
				continue
			}
			labelsMap[lb.Name] = lb.Value
			newLbs = append(newLbs, lb)
		}
		pe.Labels = labelsMap
		pe.Key = lbs.Get("__name__")
		pe.Value = val
		pe.TS = peTs
		pe.AggreValue = common.MapStr{}

		// 排序 dimensions
		pe.DimensionString = HashLabels(newLbs)
	}

	pe.ProduceHashKey()
	return pe, nil
}

func NewPromEventV1(line string, ts int64, offsetTime time.Duration, handler TimestampHandler) (PromEvent, error) {
	if !strings.HasSuffix(line, "\n") {
		line = line + "\n"
	}

	var pe PromEvent
	decoder := expfmt.NewDecoder(strings.NewReader(line), expfmt.FmtText)
	family := &clientmodel.MetricFamily{}
	err := decoder.Decode(family)
	if err != nil {
		return pe, err
	}

	familyName := family.GetName()
	metrics := family.GetMetric()
	if metrics == nil || len(metrics) != 1 {
		return pe, errors.New("not single metric")
	}
	metric := metrics[0]

	// 时间戳处理
	timestamp := metric.GetTimestampMs()
	timestamp = handler(ts, timestamp, offsetTime)

	lbs := metric.GetLabel()
	newLbs := make(labels.Labels, 0)
	labelMap := make(common.MapStr)
	for _, label := range lbs {
		labelMap[label.GetName()] = label.GetValue()
		newLbs = append(newLbs, labels.Label{Name: label.GetName(), Value: label.GetValue()})
	}

	value, err := extractValueFromMetric(*metric)
	if err != nil {
		return pe, err
	}

	if math.IsInf(value, 0) || math.IsNaN(value) {
		return pe, errors.New("value is NaN or Inf")
	}

	pe = PromEvent{
		Key:        familyName,
		Value:      value,
		Labels:     labelMap,
		AggreValue: common.MapStr{},
		TS:         timestamp,
	}
	pe.DimensionString = HashLabels(newLbs)
	pe.ProduceHashKey()

	return pe, nil
}

func extractValueFromMetric(metric clientmodel.Metric) (float64, error) {
	if metric.GetUntyped() != nil {
		return metric.GetUntyped().GetValue(), nil
	}
	if metric.GetCounter() != nil {
		return metric.GetCounter().GetValue(), nil
	}
	if metric.GetGauge() != nil {
		return metric.GetGauge().GetValue(), nil
	}

	return 0, errors.New("no metric found")
}
