// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestInstance_QuerySegmentedRaw(t *testing.T) {
	ctx := context.Background()

	log.InitTestLogger()

	url := ""
	username := ""
	password := ""
	timeout := time.Minute * 10
	maxRouting := 10
	keepAlive := "1s"
	maxSize := 10000

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ins, err := NewInstance(ctx, &InstanceOption{
		Url:        url,
		Username:   username,
		Password:   password,
		MaxRouting: maxRouting,
		MaxSize:    maxSize,
		KeepAlive:  keepAlive,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	end := time.Now()
	start := end.Add(time.Hour * -1)

	tcs := []struct {
		name     string
		query    *metadata.Query
		start    int64
		end      int64
		interval int64
	}{
		{
			name: "test-1",
			query: &metadata.Query{
				DataSource: BKLOG,
				TableID:    "2_bklog_bk_unify_query",
				Field:      Timestamp,

				TimeAggregation: &metadata.TimeAggregation{
					Function:       CountOT,
					WindowDuration: time.Minute,
				},
				AggregateMethodList: []metadata.AggrMethod{
					{
						Name: SUM,
					},
				},
			},
			start:    start.UnixMilli(),
			end:      end.UnixMilli(),
			interval: time.Minute.Milliseconds(),
		},
	}

	for _, c := range tcs {
		t.Run(c.name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			hints := &storage.SelectHints{
				Start: c.start,
				End:   c.end,
				Step:  c.interval,
			}

			ss := ins.QueryRaw(ctx, c.query, hints, nil)
			if ss.Err() != nil {
				assert.Nil(t, ss.Err())
				return
			}

			seriesNum := 0
			pointsNum := 0

			for ss.Next() {
				seriesNum++
				series := ss.At()
				lbs := series.Labels()
				it := series.Iterator(nil)
				log.Infof(ctx, "------------------------------------------------")
				log.Infof(ctx, "series: %s", lbs)
				log.Infof(ctx, "------------------------------------------------")
				if it.Err() != nil {
					panic(it.Err())
				}
				for it.Next() == chunkenc.ValFloat {
					pointsNum++
					ts, val := it.At()
					tt := time.UnixMilli(ts)

					log.Infof(ctx, "V: %d, T: %s", int(val), tt.Format("2006-01-02 15:04:05"))
				}
			}

			if ws := ss.Warnings(); len(ws) > 0 {
				panic(ws)
			}

			log.Infof(ctx, "series: %d, points: %d", seriesNum, pointsNum)
		})
	}

}
