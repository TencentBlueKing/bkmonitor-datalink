// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"golang.org/x/time/rate"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	_ storage.SeriesSet = (*streamSeriesSet)(nil)
	_ chunkenc.Iterator = (*seriesIterator)(nil)
)

type streamSeriesSet struct {
	ctx context.Context

	stream remote.QueryTimeSeriesService_RawClient
	name   string

	limiter *rate.Limiter

	currSeries *remote.TimeSeries
	recvCh     chan *remote.TimeSeries

	errMtx sync.Mutex
	err    error
	warns  storage.Warnings

	timeout time.Duration
}

type recvResponse struct {
	r   *remote.TimeSeries
	err error
}

func frameCtx(responseTimeout time.Duration) (context.Context, context.CancelFunc) {
	frameTimeoutCtx := context.Background()
	var cancel context.CancelFunc
	if responseTimeout != 0 {
		frameTimeoutCtx, cancel = context.WithTimeout(frameTimeoutCtx, responseTimeout)
		return frameTimeoutCtx, cancel
	}
	return frameTimeoutCtx, func() {}
}

func StartStreamSeriesSet(
	ctx context.Context,
	name string,
	opt *StreamSeriesSetOption,
) *streamSeriesSet {
	var (
		span *trace.Span
		err  error
	)

	s := &streamSeriesSet{
		ctx:    ctx,
		name:   name,
		recvCh: make(chan *remote.TimeSeries, 10),
	}
	if opt != nil {
		s.stream = opt.Stream
		s.timeout = opt.Timeout
		s.limiter = opt.Limiter
		span = opt.Span
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)

	start := time.Now()

	go func(ctx context.Context) {
		seriesNum := 0
		pointsNum := 0

		ctx, cancel := context.WithCancel(ctx)
		defer func() {
			if span != nil {
				sub := time.Since(start)

				span.Set("query-cost-second", int(sub.Seconds()))
				span.Set("query-cost", sub.String())
				span.Set("query-rate-limiter", int(s.limiter.Limit()))
				span.Set("resp-series-num", seriesNum)
				span.Set("resp-point-num", pointsNum)

				metric.TsDBRequestSecond(
					ctx, sub, fmt.Sprintf("%s_grpc", consul.InfluxDBStorageType), name,
				)

				span.End(&err)
			}

			cancel()
			close(s.recvCh)
			wg.Done()
		}()

		rCh := make(chan *recvResponse)
		done := make(chan struct{})
		go func() {
			for {
				r, err := s.stream.Recv()
				if r != nil {
					if opt.MetricName != "" {
						r.Labels = append(r.Labels, &remote.LabelPair{
							Name:  labels.MetricName,
							Value: opt.MetricName,
						})
					}

					if s.limiter != nil {
						s.limiter.WaitN(ctx, len(r.Samples))
					}
					seriesNum++
					pointsNum += len(r.GetSamples())
				}

				select {
				case <-done:
					close(rCh)
					return
				case rCh <- &recvResponse{r: r, err: err}:
				}
			}
		}()
		// The `defer` only executed when function return, we do `defer cancel` in for loop,
		// so make the loop body as a function, release timers created by context as early.
		handleRecvResponse := func() (next bool) {
			frameTimeoutCtx, frameCancel := frameCtx(s.timeout)
			defer frameCancel()
			var rr *recvResponse
			select {
			case <-ctx.Done():
				s.handleErr(errors.Wrapf(ctx.Err(), "failed to receive any data from %s", s.name), done)
				return false
			case <-frameTimeoutCtx.Done():
				s.handleErr(errors.Wrapf(frameTimeoutCtx.Err(), "failed to receive any data in %s from %s", s.timeout.String(), s.name), done)
				return false
			case rr = <-rCh:
			}

			if rr.err == io.EOF {
				close(done)
				return false
			}

			if rr.err != nil {
				s.handleErr(errors.Wrapf(rr.err, "receive series from %s", s.name), done)
				return false
			}

			if series := rr.r; series != nil {
				select {
				case s.recvCh <- series:
				case <-ctx.Done():
					err := errors.Wrapf(ctx.Err(), "failed to receive any data from %s", s.name)
					s.handleErr(err, done)
					return false
				}
			}
			return true
		}
		for {
			if !handleRecvResponse() {
				return
			}
		}
	}(ctx)
	return s
}

func (s *streamSeriesSet) handleErr(err error, done chan struct{}) {
	defer close(done)

	s.errMtx.Lock()
	codedErr := errno.ErrDataProcessFailed().
		WithComponent("InfluxDB数据流").
		WithOperation("启动数据流处理").
		WithError(err).
		WithSolution("检查数据流处理器配置")
	log.ErrorWithCodef(s.ctx, codedErr)
	s.err = nil
	s.errMtx.Unlock()
}

// Next blocks until new message is received or stream is closed or operation is timed out.
func (s *streamSeriesSet) Next() (ok bool) {
	s.currSeries, ok = <-s.recvCh
	return ok
}

func (s *streamSeriesSet) At() storage.Series {
	if s.currSeries == nil {
		return nil
	}

	lbs := make(labels.Labels, 0, len(s.currSeries.GetLabels()))
	for _, l := range s.currSeries.GetLabels() {
		lbs = append(lbs, labels.Label{
			Name:  l.GetName(),
			Value: l.GetValue(),
		})
	}
	sort.Sort(lbs)

	return &remoteSeries{
		labels: lbs,
		iterator: &seriesIterator{
			list: s.currSeries.Samples,
			idx:  -1,
		},
	}
}

func (s *streamSeriesSet) Err() error {
	s.errMtx.Lock()
	defer s.errMtx.Unlock()

	if s.err != nil {
		codedErr := errno.ErrBusinessQueryExecution().
			WithComponent("InfluxDB序列集").
			WithOperation("获取错误信息").
			WithContext("series_name", s.name).
			WithContext("error", s.err.Error()).
			WithSolution("检查InfluxDB查询和数据处理")
		log.ErrorWithCodef(s.ctx, codedErr)
	}
	return errors.Wrap(s.err, s.name)
}

func (s *streamSeriesSet) Warnings() storage.Warnings {
	return s.warns
}

type remoteSeries struct {
	labels   labels.Labels
	iterator *seriesIterator
}

func (rs *remoteSeries) Labels() labels.Labels {
	return rs.labels
}

func (rs *remoteSeries) Iterator(chunkenc.Iterator) chunkenc.Iterator {
	return rs.iterator
}

type seriesIterator struct {
	list []*remote.Sample
	idx  int
	err  error
}

func (it *seriesIterator) AtHistogram() (int64, *histogram.Histogram) {
	panic("tsdb series set implement me AtHistogram")
}

func (it *seriesIterator) AtFloatHistogram() (int64, *histogram.FloatHistogram) {
	panic("tsdb series set implement me AtFloatHistogram")
}

func (it *seriesIterator) AtT() int64 {
	s := it.list[it.idx]
	return s.GetTimestampMs()
}

func (it *seriesIterator) At() (int64, float64) {
	s := it.list[it.idx]
	return s.GetTimestampMs(), s.GetValue()
}

func (it *seriesIterator) Next() chunkenc.ValueType {
	it.idx++
	if it.idx < len(it.list) {
		return chunkenc.ValFloat
	}
	return chunkenc.ValNone
}

func (it *seriesIterator) Seek(t int64) chunkenc.ValueType {
	if it.idx == -1 {
		it.idx = 0
	}
	if it.idx >= len(it.list) {
		return chunkenc.ValNone
	}
	if s := it.list[it.idx]; s.GetTimestampMs() >= t {
		return chunkenc.ValFloat
	}
	// Do binary search between current position and end.
	it.idx += sort.Search(len(it.list)-it.idx, func(i int) bool {
		s := it.list[i+it.idx]
		return s.GetTimestampMs() >= t
	})
	if it.idx < len(it.list) {
		return chunkenc.ValFloat
	}

	return chunkenc.ValNone
}

func (it *seriesIterator) Err() error {
	return it.err
}
