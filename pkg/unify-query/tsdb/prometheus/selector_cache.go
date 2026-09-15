// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

type SelectorCacheState string

const (
	SelectorCacheSuccess SelectorCacheState = "SUCCESS"
	SelectorCachePartial SelectorCacheState = "PARTIAL"
	SelectorCacheError   SelectorCacheState = "ERROR"
)

type SelectorCacheLimits struct {
	MaxSeries int
	MaxPoints int
	MaxBytes  int64
}

type SelectorCacheLimitError struct {
	Name string
	Max  int64
}

func (e *SelectorCacheLimitError) Error() string {
	return fmt.Sprintf("selector cache exceeds %s: %d", e.Name, e.Max)
}

type selectorCacheEntry struct {
	ready  chan struct{}
	data   *materializedSelectorData
	state  SelectorCacheState
	status *metadata.Status
	err    error
}

type SelectorCache struct {
	mu       sync.Mutex
	statusMu sync.Mutex
	entries  map[string]*selectorCacheEntry
	limits   SelectorCacheLimits
	series   int
	points   int
	bytes    int64
	hits     int64
	misses   int64
	waits    int64
}

type SelectorCacheStats struct {
	Hits   int64
	Misses int64
	Waits  int64
}

func NewSelectorCache(limits SelectorCacheLimits) *SelectorCache {
	return &SelectorCache{
		entries: make(map[string]*selectorCacheEntry),
		limits:  limits,
	}
}

func (c *SelectorCache) GetOrLoad(
	ctx context.Context,
	key string,
	load func(context.Context) storage.SeriesSet,
) storage.SeriesSet {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok {
		c.misses++
		metric.NamedOutputsSelectorCacheEventInc(ctx, metric.NamedOutputsSelectorCacheMiss)
		entry = &selectorCacheEntry{ready: make(chan struct{})}
		c.entries[key] = entry
		c.mu.Unlock()

		selectorCtx := metadata.WithSelectorStatusScope(ctx)
		statusBefore := cloneSelectorStatus(metadata.GetStatus(selectorCtx))
		usage := selectorCacheUsage{}
		entry.data, entry.err = materializeSelectorSet(selectorCtx, load(selectorCtx), func(series, points int, bytes int64) error {
			return c.reserveIncremental(&usage, series, points, bytes)
		})
		entry.state = SelectorCacheSuccess
		if entry.err != nil {
			entry.state = SelectorCacheError
			entry.data = nil
			c.release(usage)
		} else if status := selectorStatusDelta(statusBefore, metadata.GetStatus(selectorCtx)); status != nil {
			entry.status = status
			entry.state = SelectorCachePartial
		}
		metric.NamedOutputsSelectorCacheStateInc(ctx, selectorCacheMetricState(entry.state))
		close(entry.ready)
		return c.entryView(ctx, entry)
	}
	select {
	case <-entry.ready:
		c.hits++
		metric.NamedOutputsSelectorCacheEventInc(ctx, metric.NamedOutputsSelectorCacheHit)
		c.mu.Unlock()
		return c.entryView(ctx, entry)
	default:
		c.waits++
		metric.NamedOutputsSelectorCacheEventInc(ctx, metric.NamedOutputsSelectorCacheWait)
	}
	c.mu.Unlock()

	select {
	case <-entry.ready:
		return c.entryView(ctx, entry)
	case <-ctx.Done():
		return storage.ErrSeriesSet(ctx.Err())
	}
}

func selectorCacheMetricState(state SelectorCacheState) string {
	switch state {
	case SelectorCacheSuccess:
		return metric.NamedOutputsSelectorCacheSuccess
	case SelectorCachePartial:
		return metric.NamedOutputsSelectorCachePartial
	case SelectorCacheError:
		return metric.NamedOutputsSelectorCacheError
	default:
		return ""
	}
}

func (c *SelectorCache) Stats() SelectorCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return SelectorCacheStats{Hits: c.hits, Misses: c.misses, Waits: c.waits}
}

type selectorCacheUsage struct {
	series int
	points int
	bytes  int64
}

func (c *SelectorCache) reserveIncremental(usage *selectorCacheUsage, deltaSeries, deltaPoints int, deltaBytes int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	series := c.series + deltaSeries
	points := c.points + deltaPoints
	bytes := c.bytes + deltaBytes
	if c.limits.MaxSeries > 0 && series > c.limits.MaxSeries {
		return &SelectorCacheLimitError{Name: "max_series", Max: int64(c.limits.MaxSeries)}
	}
	if c.limits.MaxPoints > 0 && points > c.limits.MaxPoints {
		return &SelectorCacheLimitError{Name: "max_points", Max: int64(c.limits.MaxPoints)}
	}
	if c.limits.MaxBytes > 0 && bytes > c.limits.MaxBytes {
		return &SelectorCacheLimitError{Name: "max_cache_bytes", Max: c.limits.MaxBytes}
	}
	c.series = series
	c.points = points
	c.bytes = bytes
	usage.series += deltaSeries
	usage.points += deltaPoints
	usage.bytes += deltaBytes
	return nil
}

func (c *SelectorCache) release(usage selectorCacheUsage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.series -= usage.series
	c.points -= usage.points
	c.bytes -= usage.bytes
}

func (c *SelectorCache) entryView(ctx context.Context, entry *selectorCacheEntry) storage.SeriesSet {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	if entry.status != nil {
		mergeSelectorStatus(ctx, entry.status)
	}
	if entry.err != nil {
		return storage.ErrSeriesSet(entry.err)
	}
	return entry.data.view()
}

func cloneSelectorStatus(status *metadata.Status) *metadata.Status {
	if status == nil {
		return nil
	}
	copyOfStatus := *status
	return &copyOfStatus
}

func selectorStatusDelta(before, after *metadata.Status) *metadata.Status {
	if after == nil {
		return nil
	}
	if before == nil {
		return cloneSelectorStatus(after)
	}
	if before.Code == after.Code && before.Message == after.Message {
		return nil
	}
	delta := cloneSelectorStatus(after)
	prefix := before.Message + "; "
	if before.Message != "" && strings.HasPrefix(after.Message, prefix) {
		delta.Message = strings.TrimPrefix(after.Message, prefix)
	}
	return delta
}

func mergeSelectorStatus(ctx context.Context, delta *metadata.Status) {
	if delta == nil {
		return
	}
	current := metadata.GetStatus(ctx)
	if current == nil {
		metadata.SetStatus(ctx, delta.Code, delta.Message)
		return
	}
	if current.Code == delta.Code && current.Message == delta.Message {
		return
	}
	message := delta.Message
	if current.Message != "" {
		message = current.Message
		if delta.Message != "" && !strings.Contains(current.Message, delta.Message) {
			message += "; " + delta.Message
		}
	}
	metadata.SetStatus(ctx, delta.Code, message)
}

type materializedSelectorData struct {
	series   []materializedSelectorSeries
	warnings storage.Warnings
	points   int
	bytes    int64
}

type materializedSelectorSeries struct {
	labels  labels.Labels
	samples []materializedSelectorSample
}

type materializedSelectorSample struct {
	t         int64
	v         float64
	h         *histogram.Histogram
	fh        *histogram.FloatHistogram
	valueType chunkenc.ValueType
}

func materializeSelectorSet(
	ctx context.Context,
	set storage.SeriesSet,
	reserve func(series, points int, bytes int64) error,
) (*materializedSelectorData, error) {
	data := &materializedSelectorData{}
	for set.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		series := set.At()
		seriesLabels := series.Labels()
		var labelBytes int64
		for _, label := range seriesLabels {
			labelBytes += int64(len(label.Name) + len(label.Value))
		}
		if err := reserve(1, 0, labelBytes); err != nil {
			return nil, err
		}
		data.bytes += labelBytes
		materialized := materializedSelectorSeries{labels: append(labels.Labels(nil), seriesLabels...)}
		iterator := series.Iterator(nil)
		for valueType := iterator.Next(); valueType != chunkenc.ValNone; valueType = iterator.Next() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			sample := materializedSelectorSample{valueType: valueType}
			var sampleBytes int64
			switch valueType {
			case chunkenc.ValFloat:
				sampleBytes = 24
				if err := reserve(0, 1, sampleBytes); err != nil {
					return nil, err
				}
				sample.t, sample.v = iterator.At()
			case chunkenc.ValHistogram:
				sampleBytes = 128
				if err := reserve(0, 1, sampleBytes); err != nil {
					return nil, err
				}
				sample.t, sample.h = iterator.AtHistogram()
				if sample.h != nil {
					sample.h = sample.h.Copy()
				}
			case chunkenc.ValFloatHistogram:
				sampleBytes = 128
				if err := reserve(0, 1, sampleBytes); err != nil {
					return nil, err
				}
				sample.t, sample.fh = iterator.AtFloatHistogram()
				if sample.fh != nil {
					sample.fh = sample.fh.Copy()
				}
			default:
				continue
			}
			materialized.samples = append(materialized.samples, sample)
			data.points++
			data.bytes += sampleBytes
		}
		if err := iterator.Err(); err != nil {
			return nil, err
		}
		data.series = append(data.series, materialized)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := set.Err(); err != nil {
		return nil, err
	}
	data.warnings = append(storage.Warnings(nil), set.Warnings()...)
	return data, nil
}

func (d *materializedSelectorData) view() storage.SeriesSet {
	series := make([]storage.Series, 0, len(d.series))
	for _, item := range d.series {
		itemCopy := item
		series = append(series, &storage.SeriesEntry{
			Lset: append(labels.Labels(nil), itemCopy.labels...),
			SampleIteratorFn: func(chunkenc.Iterator) chunkenc.Iterator {
				return &materializedSelectorIterator{samples: itemCopy.samples, index: -1}
			},
		})
	}
	return &materializedSelectorSeriesSet{series: series, warnings: append(storage.Warnings(nil), d.warnings...), index: -1}
}

type materializedSelectorSeriesSet struct {
	series   []storage.Series
	warnings storage.Warnings
	index    int
}

func (s *materializedSelectorSeriesSet) Next() bool {
	s.index++
	return s.index < len(s.series)
}

func (s *materializedSelectorSeriesSet) At() storage.Series { return s.series[s.index] }
func (s *materializedSelectorSeriesSet) Err() error         { return nil }
func (s *materializedSelectorSeriesSet) Warnings() storage.Warnings {
	return s.warnings
}

type materializedSelectorIterator struct {
	samples []materializedSelectorSample
	index   int
}

func (i *materializedSelectorIterator) Next() chunkenc.ValueType {
	i.index++
	if i.index >= len(i.samples) {
		return chunkenc.ValNone
	}
	return i.samples[i.index].valueType
}

func (i *materializedSelectorIterator) Seek(t int64) chunkenc.ValueType {
	if i.index >= 0 && i.index < len(i.samples) && i.samples[i.index].t >= t {
		return i.samples[i.index].valueType
	}
	for i.Next() != chunkenc.ValNone {
		if i.samples[i.index].t >= t {
			return i.samples[i.index].valueType
		}
	}
	return chunkenc.ValNone
}

func (i *materializedSelectorIterator) At() (int64, float64) {
	sample := i.samples[i.index]
	return sample.t, sample.v
}

func (i *materializedSelectorIterator) AtHistogram() (int64, *histogram.Histogram) {
	sample := i.samples[i.index]
	if sample.h == nil {
		return sample.t, nil
	}
	return sample.t, sample.h.Copy()
}

func (i *materializedSelectorIterator) AtFloatHistogram() (int64, *histogram.FloatHistogram) {
	sample := i.samples[i.index]
	if sample.fh != nil {
		return sample.t, sample.fh.Copy()
	}
	if sample.h != nil {
		return sample.t, sample.h.ToFloat()
	}
	return sample.t, nil
}

func (i *materializedSelectorIterator) AtT() int64 { return i.samples[i.index].t }
func (i *materializedSelectorIterator) Err() error { return nil }
