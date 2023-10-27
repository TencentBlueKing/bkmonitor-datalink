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
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	oleltrace "go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/singleflight"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// InfluxdbQuerier
type InfluxdbQuerier struct {
	ctx context.Context
	g   singleflight.Group
}

// NewInfluxdbQuerier
func NewInfluxdbQuerier(ctx context.Context) (storage.Querier, error) {
	return &InfluxdbQuerier{
		ctx: ctx,
	}, nil
}

// checkCtxDone
func (i *InfluxdbQuerier) checkCtxDone() bool {
	select {
	case <-i.ctx.Done():
		return true
	default:
		return false
	}
}

// Select : 方法返回对应标签(维度)命中的序列集合。
// 调用者可以指定返回的数据是否需要排序，但最好不要进行排序以提供更好的性能。
// 可以提供额外的信息帮助优化查询返回，但是至于如何使用则是由实现自定决定。
// 注意，此处DB、measurement和指标名都隐藏在Matcher当中：DB和measurement都是由查询模块根据http头追加，指标名是prometheus默认追加
func (i *InfluxdbQuerier) Select(_ bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	promise := make(chan storage.SeriesSet, 1)
	go func() {
		defer close(promise)
		var (
			err error
		)
		if ok := i.checkCtxDone(); ok {
			promise <- NewErrorSeriesSet(ErrContextDone)
			return
		}
		set, err := i.selectFn(hints, matchers...)
		if err != nil {
			promise <- NewErrorSeriesSet(err)
			return
		}

		promise <- set
	}()

	return &lazySeriesSet{
		create: func() (s storage.SeriesSet, ok bool) {
			set, ok := <-promise
			if !ok {
				return NewErrorSeriesSet(ErrChannelReceived), false
			}
			return set, set.Next()
		},
		set: nil,
	}
}

// selectFn
func (i *InfluxdbQuerier) selectFn(hints *storage.SelectHints, matchers ...*labels.Matcher) (storage.SeriesSet, error) {
	var (
		ctx  context.Context
		span oleltrace.Span
		err  error
		key  string

		errs     []error
		sqlInfos []influxdb.SQLInfo

		ticker *time.Ticker
		ret    singleflight.Result
	)

	ctx, span = trace.IntoContext(i.ctx, trace.TracerName, "promql-query-select-fn")
	if span != nil {
		defer span.End()
	}
	sqlInfos, err = MakeInfluxdbQuerys(ctx, hints, matchers...)
	if err != nil {
		log.Errorf(ctx, "failed to make query for error->[%s]", err)
		return nil, err
	}
	if ok := i.checkCtxDone(); ok {
		return nil, ErrContextDone
	}

	key = selectKey(sqlInfos)
	trace.InsertStringIntoSpan("select-fn-key", key, span)

	ch := i.g.DoChan(key, func() (interface{}, error) {
		var (
			result *influxdb.Tables // 查询结果缓存
		)
		if result, errs = influxdb.QueryAsync(ctx, sqlInfos, ""); len(errs) != 0 {
			log.Errorf(ctx, "failed to async query result for errs->[%v]", errs)
			return nil, errs[0]
		}
		if ok := i.checkCtxDone(); ok {
			return nil, ErrContextDone
		}
		return result, nil
	})
	ticker = time.NewTicker(time.Minute)
	defer ticker.Stop()
	select {
	case <-ticker.C:
		return nil, ErrTimeout
	case ret = <-ch:
		trace.InsertStringIntoSpan("select-fn-shared", fmt.Sprintf("%v", ret.Shared), span)
		if ret.Err != nil {
			return nil, ret.Err
		}

		return NewInfluxdbSeriesSet(ret.Val.(*influxdb.Tables)), nil
	}
}

// LabelValues: 返回可能的标签(维度)值。
// 在查询器的生命周期以外使用这些字符串是不安全的
func (i *InfluxdbQuerier) LabelValues(_ string, _ ...*labels.Matcher) ([]string, storage.Warnings, error) {
	// 和promethues的remote read对齐  https://github.com/prometheus/prometheus/issues/3351
	return nil, nil, errors.New("not implemented")
}

// LabelNames: 以块中的排序顺序返回所有的唯一的标签
func (i *InfluxdbQuerier) LabelNames(matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	// 和promethues的remote read对齐  https://github.com/prometheus/prometheus/issues/3351
	return nil, nil, errors.New("not implemented")
}

// Close: 释放查询器的所有资源
func (i *InfluxdbQuerier) Close() error {
	return nil
}
