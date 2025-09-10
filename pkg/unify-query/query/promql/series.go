// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// Sample
type Sample struct {
	Value float64
	Time  int64
}

// InfluxdbSeriesSet
type InfluxdbSeriesSet struct {
	err   error
	warns storage.Warnings

	tables *influxdb.Tables
	index  int
}

// NewErrorSeriesSet
func NewErrorSeriesSet(err error) *InfluxdbSeriesSet {
	return &InfluxdbSeriesSet{
		err: err,
	}
}

// NewInfluxdbSeriesSet
func NewInfluxdbSeriesSet(t *influxdb.Tables) *InfluxdbSeriesSet {
	return &InfluxdbSeriesSet{
		tables: t, index: -1,
	}
}

// Next
func (i *InfluxdbSeriesSet) Next() bool {
	// 空数据的时候，直接返回
	if i.tables == nil {
		return false
	}
	i.index++
	log.Debugf(context.TODO(), "table index will go to->[%d] with tables count->[%d]", i.index, len(i.tables.Tables))
	return i.index < len(i.tables.Tables)
}

// At
func (i *InfluxdbSeriesSet) At() storage.Series {
	return NewInfluxdbSeries(i.tables.Tables[i.index])
}

// Err
func (i *InfluxdbSeriesSet) Err() error {
	return i.err
}

// Warnings
func (i *InfluxdbSeriesSet) Warnings() storage.Warnings {
	return i.warns
}

// InfluxdbSeries
type InfluxdbSeries struct {
	table *influxdb.Table

	labels []labels.Label
	isi    *InfluxdbSeriesIterator
}

// NewInfluxdbSeries
func NewInfluxdbSeries(t *influxdb.Table) *InfluxdbSeries {
	is := &InfluxdbSeries{
		table:  t,
		labels: make([]labels.Label, 0, len(t.GroupValues)), // 直接优先分配group长度
		isi:    NewInfluxdbSeriesIterator(t),
	}

	// 提前获取所有的label信息
	if len(t.GroupKeys) != len(t.GroupValues) {
		log.Errorf(context.TODO(), "WHAT? Got result group length->[%d|%d] is different", len(t.GroupKeys), len(t.GroupValues))
		return nil
	}

	for index, key := range t.GroupKeys {
		is.labels = append(is.labels, labels.Label{Name: key, Value: t.GroupValues[index]})
	}

	return is
}

// Labels
func (i InfluxdbSeries) Labels() labels.Labels {
	return i.labels
}

// Iterator
func (i InfluxdbSeries) Iterator(chunkenc.Iterator) chunkenc.Iterator {
	return i.isi
}

// InfluxdbSeriesIterator
type InfluxdbSeriesIterator struct {
	table *influxdb.Table

	readIndex         int // 读取的索引值
	timeColumnIndex   int // 时间索引值，可以方便直接从data中拿到【时间】那一列
	resultColumnIndex int // 结果索引值，可以方便直接从data中拿到【指标】那一列

	samples []*Sample // 结果转换缓存

	lastError error
}

// NewInfluxdbSeriesIterator
func NewInfluxdbSeriesIterator(t *influxdb.Table) *InfluxdbSeriesIterator {
	var (
		isi = &InfluxdbSeriesIterator{table: t, readIndex: -1}

		pointTime time.Time
		value     float64
		err       error
	)

	if len(t.Types) != len(t.Headers) {
		log.Errorf(context.TODO(), "WHAT? Got header length->[%d|%d] is different", len(t.Types), len(t.Headers))
		return nil
	}

	// 遍历找到这个结果表的时间和指标列
	for index, columnName := range isi.table.Headers {
		switch columnName {
		case influxdb.TimeColumnName:
			isi.timeColumnIndex = index
			log.Debugf(context.TODO(), "time column found index->[%d]", index)
		case influxdb.ResultColumnName:
			// 由于上面已经确认过header和type的长度，所以可以直接放心使用
			isi.resultColumnIndex = index
			log.Debugf(context.TODO(), "result column found index->[%d]", index)
		}
	}

	// 解析所有数据的内容
	// 优先解析的原因是：观察promQL的特性发现，对于同样的数据点，会存在重复读取的情况。如果每次临时转换，有重复的性能消耗情况
	for index := range isi.table.Data {
		if pointTime, value, err = isi.getRawPoint(index); err != nil {
			continue
		}

		isi.samples = append(isi.samples, &Sample{Time: pointTime.Unix() * 1000, Value: value})
	}

	return isi
}

// Next
func (i *InfluxdbSeriesIterator) Next() chunkenc.ValueType {
	i.readIndex++
	log.Debugf(context.TODO(), "row will go to->[%d] with row count->[%d]", i.readIndex, len(i.table.Data))
	// 由于上面getRawPoint的时候将nil和异常的跳过了，所以此时next是否有值，不能用原始数据对比，应该以isi.samples 的长度对比
	if i.readIndex < len(i.samples) {
		return chunkenc.ValFloat
	}
	return chunkenc.ValNone
}

// Seek 直接跳转到给定的时间及之后的数据，即使这个迭代器已经到头了，也需要重新搜索
// 此处的代码是直接挪用prometheus的remote storage逻辑
func (i *InfluxdbSeriesIterator) Seek(t int64) chunkenc.ValueType { // nolint:golint,govet
	if i.readIndex == -1 {
		i.readIndex = 0
	}
	if i.readIndex >= len(i.samples) {
		return chunkenc.ValNone
	}

	if s := i.samples[i.readIndex]; s.Time >= t {
		return chunkenc.ValFloat
	}

	i.readIndex += sort.Search(len(i.samples)-i.readIndex, func(n int) bool {
		return i.samples[n+i.readIndex].Time >= t
	})
	if i.readIndex < len(i.samples) {
		return chunkenc.ValFloat
	}
	return chunkenc.ValNone
}

func (i *InfluxdbSeriesIterator) AtHistogram() (int64, *histogram.Histogram) {
	panic("promql series implement me AtHistogram")
}

func (i *InfluxdbSeriesIterator) AtFloatHistogram() (int64, *histogram.FloatHistogram) {
	panic("promql series implement me AtFloatHistogram")
}

func (i *InfluxdbSeriesIterator) AtT() int64 {
	s := i.getCurrentPoint()
	log.Debugf(context.TODO(), "got sample at index->[%d] with time->[%d] value->[%f]", i.readIndex, s.Time, s.Value)
	return s.Time
}

// At
func (i *InfluxdbSeriesIterator) At() (int64, float64) {
	s := i.getCurrentPoint()
	log.Debugf(context.TODO(), "got sample at index->[%d] with time->[%d] value->[%f]", i.readIndex, s.Time, s.Value)
	return s.Time, s.Value
}

// Err
func (i *InfluxdbSeriesIterator) Err() error {
	return i.lastError
}

// getCurrentPoint
func (i *InfluxdbSeriesIterator) getCurrentPoint() *Sample {
	return i.samples[i.readIndex]
}

// getRawPoint: 传入索引值，返回原始数据的时间和值
func (i *InfluxdbSeriesIterator) getRawPoint(index int) (time.Time, float64, error) {
	var (
		data     = i.table.Data[index]
		t        time.Time
		err      error
		ok       bool
		value    float64
		timeItem string
	)

	// 基于不同的通信协议(json/x-msgpack)会有不同的时间解析结果
	switch data[i.timeColumnIndex].(type) {
	case string:
		timeItem, ok = data[i.timeColumnIndex].(string)
		if !ok {
			log.Errorf(context.TODO(), "parse time type failed,data: %#v", data[i.resultColumnIndex])
			return t, 0, nil
		}
		if t, err = time.Parse(time.RFC3339Nano, timeItem); err != nil {
			log.Errorf(context.TODO(),
				"failed to transfer datetime->[%s] for err->[%s], will return empty data", data[i.timeColumnIndex], err,
			)
			i.lastError = ErrDatetimeParseFailed
			return t, 0, i.lastError
		}
	case time.Time:
		t, ok = data[i.timeColumnIndex].(time.Time)
		if !ok {
			log.Errorf(context.TODO(), "parse time type failed,data: %#v", data[i.timeColumnIndex])
			return t, 0, nil
		}
	}

	switch v := data[i.resultColumnIndex].(type) {
	case float64:
		return t, v, nil
	case int:
		return t, float64(v), nil
	case int64:
		return t, float64(v), nil
	case json.Number:
		result, err1 := v.Float64()
		if err1 != nil {
			log.Errorf(context.TODO(), "parse value from string failed,data:%#v", data[i.resultColumnIndex])
			i.lastError = err1
			return t, 0, err1
		}
		return t, result, nil
	case string:
		result, err1 := strconv.ParseFloat(v, 64)
		if err1 != nil {
			log.Errorf(context.TODO(), "parse value from string failed,data:%#v", data[i.resultColumnIndex])
			i.lastError = err1
			return t, 0, err1
		}
		return t, result, nil
	case nil:
		log.Debugf(context.TODO(), "value data is nil, skip this")
		return t, 0, ErrInvalidValue
	default:
		log.Errorf(context.TODO(),
			"get value type failed, type: %T, data: %+v, resultColumnIndex: %d",
			data[i.resultColumnIndex], data, i.resultColumnIndex,
		)
	}
	log.Debugf(context.TODO(),
		"parser data->[%v] to time->[%s] result->[%f] success", data, t.String(), value,
	)
	return t, value, nil
}
