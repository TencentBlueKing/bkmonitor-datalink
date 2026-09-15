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
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/tsdbutil"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func consumeFloatSeriesSet(set storage.SeriesSet) ([]float64, error) {
	values := make([]float64, 0)
	for set.Next() {
		iterator := set.At().Iterator(nil)
		for valueType := iterator.Next(); valueType != chunkenc.ValNone; valueType = iterator.Next() {
			if valueType == chunkenc.ValFloat {
				_, value := iterator.At()
				values = append(values, value)
			}
		}
		if err := iterator.Err(); err != nil {
			return nil, err
		}
	}
	return values, set.Err()
}

type testSampleIterator struct {
	valueType chunkenc.ValueType
	h         *histogram.Histogram
	fh        *histogram.FloatHistogram
	total     int
	nextCalls *atomic.Int32
	onNext    func(int)
	index     int
}

func (i *testSampleIterator) Next() chunkenc.ValueType {
	i.index++
	if i.nextCalls != nil {
		i.nextCalls.Add(1)
	}
	if i.onNext != nil {
		i.onNext(i.index)
	}
	if i.index > i.total {
		return chunkenc.ValNone
	}
	return i.valueType
}

func (i *testSampleIterator) Seek(int64) chunkenc.ValueType { return i.Next() }
func (i *testSampleIterator) At() (int64, float64)          { return int64(i.index), float64(i.index) }
func (i *testSampleIterator) AtHistogram() (int64, *histogram.Histogram) {
	return int64(i.index), i.h
}
func (i *testSampleIterator) AtFloatHistogram() (int64, *histogram.FloatHistogram) {
	return int64(i.index), i.fh
}
func (i *testSampleIterator) AtT() int64 { return int64(i.index) }
func (i *testSampleIterator) Err() error { return nil }

func iteratorSeriesSet(iterator chunkenc.Iterator) storage.SeriesSet {
	return &oneSeriesSet{series: &storage.SeriesEntry{
		Lset: labels.FromStrings("service", "api"),
		SampleIteratorFn: func(chunkenc.Iterator) chunkenc.Iterator {
			return iterator
		},
	}}
}

func readNativeHistogram(set storage.SeriesSet, valueType chunkenc.ValueType) (float64, error) {
	if !set.Next() {
		return 0, set.Err()
	}
	iterator := set.At().Iterator(nil)
	if got := iterator.Next(); got != valueType {
		return 0, errors.New("unexpected histogram value type")
	}
	if valueType == chunkenc.ValHistogram {
		_, value := iterator.AtHistogram()
		return float64(value.Count), nil
	}
	_, value := iterator.AtFloatHistogram()
	return value.Count, nil
}

type oneSeriesSet struct {
	series storage.Series
	done   bool
}

type beforeNextSeriesSet struct {
	storage.SeriesSet
	beforeNext func()
	once       sync.Once
}

func (s *beforeNextSeriesSet) Next() bool {
	s.once.Do(s.beforeNext)
	return s.SeriesSet.Next()
}

func (s *oneSeriesSet) Next() bool {
	if s.done {
		return false
	}
	s.done = true
	return true
}

func (s *oneSeriesSet) At() storage.Series         { return s.series }
func (s *oneSeriesSet) Err() error                 { return nil }
func (s *oneSeriesSet) Warnings() storage.Warnings { return nil }

func generatedFloatSeriesSet(start, count int) storage.SeriesSet {
	return &oneSeriesSet{series: storage.NewListSeries(
		labels.FromStrings("service", "api"),
		tsdbutil.GenerateSamples(start, count),
	)}
}

func TestSelectorCacheReplaysSuccessPartialAndErrorWithoutReload(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	cache := NewSelectorCache(SelectorCacheLimits{MaxSeries: 10, MaxPoints: 10, MaxBytes: 1024 * 1024})

	var successLoads int
	loadSuccess := func(context.Context) storage.SeriesSet {
		successLoads++
		return generatedFloatSeriesSet(3, 2)
	}
	first := cache.GetOrLoad(ctx, "success", loadSuccess)
	second := cache.GetOrLoad(ctx, "success", loadSuccess)
	firstValues, err := consumeFloatSeriesSet(first)
	require.NoError(t, err)
	secondValues, err := consumeFloatSeriesSet(second)
	require.NoError(t, err)
	require.Equal(t, []float64{3, 4}, firstValues)
	require.Equal(t, firstValues, secondValues)
	require.Equal(t, 1, successLoads)
	stats := cache.Stats()
	require.Equal(t, int64(1), stats.Misses)
	require.Equal(t, int64(1), stats.Hits)

	partialCtx := metadata.WithStatusScope(ctx, 1)
	var partialLoads int
	partial := cache.GetOrLoad(partialCtx, "partial", func(selectorCtx context.Context) storage.SeriesSet {
		partialLoads++
		metadata.SetStatus(selectorCtx, metadata.QueryTsPartial, "partial selector")
		return generatedFloatSeriesSet(5, 1)
	})
	_, err = consumeFloatSeriesSet(partial)
	require.NoError(t, err)

	partialReplayCtx := metadata.WithStatusScope(ctx, 2)
	partial = cache.GetOrLoad(partialReplayCtx, "partial", func(context.Context) storage.SeriesSet {
		partialLoads++
		return storage.EmptySeriesSet()
	})
	_, err = consumeFloatSeriesSet(partial)
	require.NoError(t, err)
	require.Equal(t, 1, partialLoads)
	require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(partialReplayCtx).Code)
	metadata.SetStatus(ctx, "ROUTE_PARTIAL", "base route status")
	partialWithBaseCtx := metadata.WithStatusScope(ctx, 3)
	partial = cache.GetOrLoad(partialWithBaseCtx, "partial", func(context.Context) storage.SeriesSet {
		panic("cached partial selector must not reload")
	})
	_, err = consumeFloatSeriesSet(partial)
	require.NoError(t, err)
	require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(partialWithBaseCtx).Code)
	require.Equal(t, "base route status; partial selector", metadata.GetStatus(partialWithBaseCtx).Message)

	var errorLoads int
	loadError := func(context.Context) storage.SeriesSet {
		errorLoads++
		return storage.ErrSeriesSet(errors.New("selector failed"))
	}
	_, err = consumeFloatSeriesSet(cache.GetOrLoad(ctx, "error", loadError))
	require.ErrorContains(t, err, "selector failed")
	_, err = consumeFloatSeriesSet(cache.GetOrLoad(ctx, "error", loadError))
	require.ErrorContains(t, err, "selector failed")
	require.Equal(t, 1, errorLoads)
}

func TestSelectorCacheSingleflightAndCapacity(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	cache := NewSelectorCache(SelectorCacheLimits{MaxSeries: 10, MaxPoints: 1, MaxBytes: 1024 * 1024})

	started := make(chan struct{})
	release := make(chan struct{})
	var loads atomic.Int32
	loader := func(context.Context) storage.SeriesSet {
		if loads.Add(1) == 1 {
			close(started)
		}
		<-release
		return generatedFloatSeriesSet(3, 2)
	}

	var wg sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := consumeFloatSeriesSet(cache.GetOrLoad(ctx, "same", loader))
			errorsSeen <- err
		}()
	}
	<-started
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		require.ErrorContains(t, err, "max_points")
	}
	require.Equal(t, int32(1), loads.Load())
	stats := cache.Stats()
	require.Equal(t, int64(1), stats.Misses)
	require.Equal(t, int64(1), stats.Waits)
}

func TestSelectorCacheKeepsSelectorStatusLocal(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	cache := NewSelectorCache(SelectorCacheLimits{MaxSeries: 10, MaxPoints: 10, MaxBytes: 1024 * 1024})

	outputACtx := metadata.WithStatusScope(ctx, 0)
	_, err := consumeFloatSeriesSet(cache.GetOrLoad(outputACtx, "A", func(selectorCtx context.Context) storage.SeriesSet {
		metadata.SetStatus(selectorCtx, metadata.QueryTsPartial, "A partial")
		return generatedFloatSeriesSet(1, 1)
	}))
	require.NoError(t, err)
	_, err = consumeFloatSeriesSet(cache.GetOrLoad(outputACtx, "B", func(context.Context) storage.SeriesSet {
		return generatedFloatSeriesSet(2, 1)
	}))
	require.NoError(t, err)
	require.Equal(t, "A partial", metadata.GetStatus(outputACtx).Message)

	outputBCtx := metadata.WithStatusScope(ctx, 1)
	_, err = consumeFloatSeriesSet(cache.GetOrLoad(outputBCtx, "B", func(context.Context) storage.SeriesSet {
		panic("cached B selector must not reload")
	}))
	require.NoError(t, err)
	require.Nil(t, metadata.GetStatus(outputBCtx), "A partial must not contaminate cached B status")
}

func TestSelectorCacheConcurrentPopulateKeepsSelectorStatusLocal(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	outputACtx := metadata.WithStatusScope(ctx, 0)
	cache := NewSelectorCache(SelectorCacheLimits{MaxSeries: 10, MaxPoints: 10, MaxBytes: 1024 * 1024})

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	aPartialSet := make(chan struct{})
	errorsSeen := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := consumeFloatSeriesSet(cache.GetOrLoad(outputACtx, "A", func(selectorCtx context.Context) storage.SeriesSet {
			started <- struct{}{}
			<-release
			metadata.SetStatus(selectorCtx, metadata.QueryTsPartial, "A partial")
			close(aPartialSet)
			return generatedFloatSeriesSet(1, 1)
		}))
		errorsSeen <- err
	}()
	go func() {
		defer wg.Done()
		_, err := consumeFloatSeriesSet(cache.GetOrLoad(outputACtx, "B", func(context.Context) storage.SeriesSet {
			started <- struct{}{}
			<-release
			return &beforeNextSeriesSet{
				SeriesSet: generatedFloatSeriesSet(2, 1),
				beforeNext: func() {
					<-aPartialSet
				},
			}
		}))
		errorsSeen <- err
	}()
	<-started
	<-started
	close(release)
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		require.NoError(t, err)
	}
	require.Equal(t, "A partial", metadata.GetStatus(outputACtx).Message)

	outputBCtx := metadata.WithStatusScope(ctx, 1)
	_, err := consumeFloatSeriesSet(cache.GetOrLoad(outputBCtx, "B", func(context.Context) storage.SeriesSet {
		panic("concurrently populated B selector must be cached")
	}))
	require.NoError(t, err)
	require.Nil(t, metadata.GetStatus(outputBCtx), "A partial must not contaminate concurrently populated B")
}

func TestSelectorCacheReplaysIndependentHistogramCopies(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	cache := NewSelectorCache(SelectorCacheLimits{MaxSeries: 10, MaxPoints: 10, MaxBytes: 1024 * 1024})

	histogramSet := func(context.Context) storage.SeriesSet {
		return iteratorSeriesSet(&testSampleIterator{
			valueType: chunkenc.ValHistogram,
			h:         &histogram.Histogram{Count: 3, Sum: 6},
			total:     1,
		})
	}
	first := cache.GetOrLoad(ctx, "histogram", histogramSet)
	require.True(t, first.Next())
	firstIterator := first.At().Iterator(nil)
	require.Equal(t, chunkenc.ValHistogram, firstIterator.Next())
	_, firstHistogram := firstIterator.AtHistogram()
	firstHistogram.Count = 99
	count, err := readNativeHistogram(cache.GetOrLoad(ctx, "histogram", histogramSet), chunkenc.ValHistogram)
	require.NoError(t, err)
	require.Equal(t, float64(3), count)

	floatHistogramSet := func(context.Context) storage.SeriesSet {
		return iteratorSeriesSet(&testSampleIterator{
			valueType: chunkenc.ValFloatHistogram,
			fh:        &histogram.FloatHistogram{Count: 4, Sum: 8},
			total:     1,
		})
	}
	first = cache.GetOrLoad(ctx, "float-histogram", floatHistogramSet)
	require.True(t, first.Next())
	firstIterator = first.At().Iterator(nil)
	require.Equal(t, chunkenc.ValFloatHistogram, firstIterator.Next())
	_, firstFloatHistogram := firstIterator.AtFloatHistogram()
	firstFloatHistogram.Count = 99
	count, err = readNativeHistogram(cache.GetOrLoad(ctx, "float-histogram", floatHistogramSet), chunkenc.ValFloatHistogram)
	require.NoError(t, err)
	require.Equal(t, float64(4), count)
}

func TestSelectorCacheStopsMaterializingAtCapacityOrCancellation(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	var capacityCalls atomic.Int32
	cache := NewSelectorCache(SelectorCacheLimits{MaxSeries: 10, MaxPoints: 1, MaxBytes: 1024 * 1024})
	set := cache.GetOrLoad(ctx, "capacity", func(context.Context) storage.SeriesSet {
		return iteratorSeriesSet(&testSampleIterator{
			valueType: chunkenc.ValFloat,
			total:     100,
			nextCalls: &capacityCalls,
		})
	})
	_, err := consumeFloatSeriesSet(set)
	require.ErrorContains(t, err, "max_points")
	require.LessOrEqual(t, capacityCalls.Load(), int32(2), "capacity must stop materialization incrementally")

	cancelCtx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
	var cancellationCalls atomic.Int32
	cache = NewSelectorCache(SelectorCacheLimits{MaxSeries: 10, MaxPoints: 100, MaxBytes: 1024 * 1024})
	set = cache.GetOrLoad(cancelCtx, "cancel", func(context.Context) storage.SeriesSet {
		return iteratorSeriesSet(&testSampleIterator{
			valueType: chunkenc.ValFloat,
			total:     100,
			nextCalls: &cancellationCalls,
			onNext: func(index int) {
				if index == 2 {
					cancel()
				}
			},
		})
	})
	_, err = consumeFloatSeriesSet(set)
	require.ErrorIs(t, err, context.Canceled)
	require.LessOrEqual(t, cancellationCalls.Load(), int32(2), "cancellation must stop materialization")
}
