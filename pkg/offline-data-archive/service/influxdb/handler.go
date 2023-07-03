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
	"time"

	"github.com/influxdata/influxdb/services/storage"
	"github.com/influxdata/influxdb/tsdb"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	remote "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/influxdb/proto"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/trace"
)

type Server struct {
	remote.UnimplementedQueryTimeSeriesServiceServer
	timeout time.Duration

	dataDir string
	walDir  string

	ss  *storage.Store
	log log.Logger

	getShard func(ctx context.Context, clusterName, db, rp, tagName, tagValue string, start, end int64) ([]*shard.Shard, error)
}

func (s *Server) Raw(req *remote.ReadRequest, stream remote.QueryTimeSeriesService_RawServer) error {
	var (
		ctx  context.Context
		span oteltrace.Span
		err  error
	)

	ctx = context.Background()
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "grpc-raw")
	if span != nil {
		defer span.End()
	}

	if req.GetRp() == "" {
		req.Rp = "autogen"
	}

	shards, err := s.getShard(
		ctx, req.GetClusterName(), req.GetTagKey(), req.GetTagValue(),
		req.GetDb(), req.GetRp(), req.GetStart(), req.GetEnd(),
	)
	if err != nil {
		return err
	}

	if len(shards) == 0 {
		return nil
	}

	rawQuery := &RawQuery{
		Ctx:           ctx,
		Log:           s.log,
		WalDir:        s.walDir,
		DataDir:       s.dataDir,
		DB:            req.GetDb(),
		RP:            req.GetRp(),
		Measurement:   req.GetMeasurement(),
		Field:         req.GetField(),
		Start:         req.GetStart(),
		End:           req.GetEnd(),
		Condition:     req.GetCondition(),
		EngineOptions: s.ss.TSDBStore.EngineOptions,
		Shards:        shards,
	}

	rs, err := rawQuery.ReadFilter()
	if err != nil {
		return err
	}

	if err != nil {
		return err
	}
	if rs != nil {
		defer rs.Close()
	} else {
		return nil
	}

	var (
		seriesNum int64 = 0
		pointsNum int64 = 0
	)

	startTime := time.Now()
	s.log.Infof(ctx, "start query %+v", req)

	for rs.Next() {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		default:
		}

		if req.GetSLimit() > 0 && seriesNum >= req.GetSLimit() {
			break
		}
		if req.GetLimit() > 0 && pointsNum >= req.GetLimit() {
			break
		}

		err = func(stream remote.QueryTimeSeriesService_RawServer) error {
			cur := rs.Cursor()
			if cur == nil {
				return nil
			}
			tags := removeInfluxSystemTags(rs.Tags())
			series := &remote.TimeSeries{
				Labels: modelTagsToLabelPairs(tags),
			}

			defer func() {
				cur.Close()

				if len(series.Samples) > 0 {
					seriesNum++
					stream.Send(series)
				}
			}()

			var unsupportedCursor string
			switch cur := cur.(type) {
			case tsdb.FloatArrayCursor:
				for {
					a := cur.Next()
					if a.Len() == 0 {
						return nil
					}

					for i, ts := range a.Timestamps {
						pointsNum++
						series.Samples = append(series.Samples, &remote.Sample{
							TimestampMs: ts / int64(time.Millisecond),
							Value:       a.Values[i],
						})

						if req.GetLimit() > 0 && pointsNum >= req.GetLimit() {
							return nil
						}
					}
				}
			case tsdb.IntegerArrayCursor:
				for {
					a := cur.Next()
					if a.Len() == 0 {
						return nil
					}

					for i, ts := range a.Timestamps {
						pointsNum++
						series.Samples = append(series.Samples, &remote.Sample{
							TimestampMs: ts / int64(time.Millisecond),
							Value:       float64(a.Values[i]),
						})

						if req.GetLimit() > 0 && pointsNum >= req.GetLimit() {
							return nil
						}
					}
				}
			case tsdb.UnsignedArrayCursor:
				unsupportedCursor = "uint"
			case tsdb.BooleanArrayCursor:
				unsupportedCursor = "bool"
			case tsdb.StringArrayCursor:
				unsupportedCursor = "string"
			default:
				return fmt.Errorf("unreachable: %T", cur)
			}

			if len(unsupportedCursor) > 0 {
				return fmt.Errorf("raw can't read cursor, cursor_type: %s, series: %s", unsupportedCursor, tags)
			}
			return nil
		}(stream)

		if err != nil {
			return err
		}
	}

	cosTime := time.Since(startTime).String()
	trace.InsertStringIntoSpan("resp-series-num", fmt.Sprintf("%d", seriesNum), span)
	trace.InsertStringIntoSpan("resp-points-num", fmt.Sprintf("%d", pointsNum), span)
	trace.InsertStringIntoSpan("query-cos-time", cosTime, span)

	s.log.Infof(ctx, "resp series num %d", seriesNum)
	s.log.Infof(ctx, "resp points num %d", pointsNum)
	s.log.Infof(ctx, "query cos %s", cosTime)

	return nil
}
