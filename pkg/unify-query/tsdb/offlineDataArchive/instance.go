// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package offlineDataArchive

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	remoteRead "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/influxdb/proto"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	influxdbRouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

var (
	ErrorsHttpNotFound = errors.New("404 Not Found")
	client             remoteRead.QueryTimeSeriesServiceClient
	mutex              sync.Mutex
)

var _ tsdb.Instance = &Instance{}

type Instance struct {
	Ctx           context.Context
	Address       string
	Timeout       time.Duration
	MaxLimit      int
	MaxSLimit     int
	Toleration    int
	ReadRateLimit float64

	GrpcMaxCallRecvMsgSize int
	GrpcMaxCallSendMsgSize int
}

type StreamSeriesSetOption struct {
	Span    *trace.Span
	Stream  remoteRead.QueryTimeSeriesService_RawClient
	Limiter *rate.Limiter
	Timeout time.Duration
}

func (i *Instance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

// getLimitAndSlimit 获取真实的 limit 和 slimit
func (i *Instance) getLimitAndSlimit(limit, slimit int) (int64, int64) {
	var (
		resultLimit, resultSLimit int
	)

	if limit > 0 {
		resultLimit = limit
	}
	if limit == 0 || limit > i.MaxLimit {
		resultLimit = i.MaxLimit + i.Toleration
	}

	if slimit > 0 {
		resultSLimit = slimit
	}
	if slimit == 0 || slimit > i.MaxSLimit {
		resultSLimit = i.MaxSLimit + i.Toleration
	}

	return int64(resultLimit), int64(resultSLimit)
}

func (i Instance) setClient() error {
	if client == nil {
		// 增加全局 offline archive query 查询
		conn, err := grpc.DialContext(i.Ctx, i.Address, []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(i.GrpcMaxCallRecvMsgSize),
				grpc.MaxCallSendMsgSize(i.GrpcMaxCallSendMsgSize),
			),
		}...)
		if err != nil {
			return err
		}
		mutex.Lock()
		client = remoteRead.NewQueryTimeSeriesServiceClient(conn)
		mutex.Unlock()
	}

	return nil
}

func (i Instance) QueryRaw(
	ctx context.Context, query *metadata.Query,
	start, end time.Time,
) storage.SeriesSet {
	var (
		err error
	)

	ctx, span := trace.NewSpan(ctx, "offline-data-archive-query-raw-grpc-stream")

	user := metadata.GetUser(ctx)
	span.Set("query-space-uid", user.SpaceUid)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-url-path", i.Address)
	span.Set("query-cluster-name", query.ClusterName)

	span.Set("query-db", query.DB)
	span.Set("query-rp", query.RetentionPolicy)
	span.Set("query-measurement", query.Measurement)
	span.Set("query-field", query.Field)
	span.Set("query-where", query.Condition)

	limit, slimit := i.getLimitAndSlimit(query.OffsetInfo.Limit, query.OffsetInfo.SLimit)

	// 配置 client
	err = i.setClient()
	if err != nil {
		log.Errorf(ctx, err.Error())
		return storage.ErrSeriesSet(err)
	}

	if client == nil {
		err = fmt.Errorf("offline data archive client is null, %s", i.Address)
		log.Errorf(ctx, err.Error())
		return storage.ErrSeriesSet(err)
	}

	tagRouter, err := influxdbRouter.GetTagRouter(ctx, query.TagsKey, query.Condition)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return storage.ErrSeriesSet(err)
	}

	req := &remoteRead.ReadRequest{
		ClusterName: query.ClusterName,
		TagRouter:   tagRouter,
		Db:          query.DB,
		Rp:          query.RetentionPolicy,
		Measurement: query.Measurement,
		Field:       query.Field,
		Condition:   query.Condition,
		SLimit:      slimit,
		Limit:       limit,
		Start:       start.UnixMilli(),
		End:         end.UnixMilli(),
	}

	filterRequest, _ := json.Marshal(req)
	span.Set("query-filter-request", string(filterRequest))

	stream, err := client.Raw(ctx, req)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return storage.EmptySeriesSet()
	}
	limiter := rate.NewLimiter(rate.Limit(i.ReadRateLimit), int(i.ReadRateLimit))

	span.Set("start-stream-series-set", i.Address)
	return StartStreamSeriesSet(
		ctx, i.Address, &StreamSeriesSetOption{
			Span:    span,
			Stream:  stream,
			Limiter: limiter,
			Timeout: i.Timeout,
		},
	)
}

func (i Instance) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	panic("implement me")
}

func (i Instance) Query(ctx context.Context, promql string, end time.Time) (promql.Vector, error) {
	panic("implement me")
}

func (i Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	panic("implement me")
}

func (i Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	panic("implement me")
}

func (i Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	panic("implement me")
}

func (i Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	panic("implement me")
}

func (i Instance) GetInstanceType() string {
	return consul.OfflineDataArchive
}
