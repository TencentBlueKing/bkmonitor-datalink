// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"context"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type Instance interface {
	QueryRawData(ctx context.Context, query *metadata.Query, start, end time.Time, dataCh chan<- map[string]any) (int64, metadata.ResultTableOptions, error)
	QuerySeriesSet(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet
	QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error)

	QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error)
	QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) ([]string, error)
	QuerySeries(ctx context.Context, query *metadata.Query, start, end time.Time) ([]map[string]string, error)

	Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string
	DirectQueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error)
	DirectQuery(ctx context.Context, qs string, end time.Time) (promql.Vector, error)
	DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error)
	DirectLabelValues(ctx context.Context, name string, start, end time.Time, limit int, matchers ...*labels.Matcher) ([]string, error)

	InstanceType() string
	InstanceConnects() []string
	ScrollHandler() ScrollHandler
}

var (
	_ Instance = &DefaultInstance{}
)

type ScrollHandler interface {
	MakeSlices(ctx context.Context, session *redis.ScrollSession, connect, tableID string) ([]*redis.SliceInfo, error)
	IsCompleted(opt *metadata.ResultTableOption, dataLen int) bool
	UpdateScrollStatus(ctx context.Context, session *redis.ScrollSession, connect, tableID string, resultOption *metadata.ResultTableOption, status string) error
}

type DefaultInstance struct {
}

func (d *DefaultInstance) InstanceConnects() []string {
	return nil
}

func (d *DefaultInstance) ScrollHandler() ScrollHandler {
	return nil
}

func (d *DefaultInstance) QueryRawData(ctx context.Context, query *metadata.Query, start, end time.Time, dataCh chan<- map[string]any) (int64, metadata.ResultTableOptions, error) {
	return 0, nil, nil
}

func (d *DefaultInstance) QuerySeriesSet(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet {
	return storage.EmptySeriesSet()
}

func (d *DefaultInstance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	return nil, nil
}

func (d *DefaultInstance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (d *DefaultInstance) QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (d *DefaultInstance) QuerySeries(ctx context.Context, query *metadata.Query, start, end time.Time) ([]map[string]string, error) {
	return nil, nil
}

func (d *DefaultInstance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

func (d *DefaultInstance) DirectQueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	return nil, nil
}

func (d *DefaultInstance) DirectQuery(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
	return nil, nil
}

func (d *DefaultInstance) DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (d *DefaultInstance) DirectLabelValues(ctx context.Context, name string, start, end time.Time, limit int, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (d *DefaultInstance) InstanceType() string {
	return "default"
}
