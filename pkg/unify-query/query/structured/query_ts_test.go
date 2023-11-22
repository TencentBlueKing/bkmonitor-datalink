// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"

	omd "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	md "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

type m struct {
	shard []*shard.Shard
}

func (m *m) PublishShard(ctx context.Context, channelValue interface{}) error {
	panic("implement me")
}

func (m *m) SubscribeShard(ctx context.Context) <-chan *goRedis.Message {
	panic("implement me")
}

func (m *m) GetShardID(ctx context.Context, sd *shard.Shard) (string, error) {
	panic("implement me")
}

func (m *m) GetAllShards(ctx context.Context) map[string]*shard.Shard {
	panic("implement me")
}

func (m *m) SetShard(ctx context.Context, k string, sd *shard.Shard) error {
	panic("implement me")
}

func (m *m) GetShard(ctx context.Context, k string) (*shard.Shard, error) {
	panic("implement me")
}

func (m *m) GetDistributedLock(ctx context.Context, key, val string, expiration time.Duration) (string, error) {
	panic("implement me")
}

func (m *m) RenewalLock(ctx context.Context, key string, renewalDuration time.Duration) (bool, error) {
	panic("implement me")
}

func (m *m) GetPolicies(ctx context.Context, clusterName, tagRouter string) (map[string]*omd.Policy, error) {
	panic("implement me")
}

func (m *m) GetShards(ctx context.Context, clusterName, tagRouter, database string) (map[string]*shard.Shard, error) {
	panic("implement me")
}

func (m *m) GetReadShardsByTimeRange(ctx context.Context, clusterName, tagRouter, database, retentionPolicy string, start int64, end int64) ([]*shard.Shard, error) {
	log.Debugf(ctx, "check offline data archive query: %s %s %s %s %d %d", clusterName, tagRouter, database, retentionPolicy, start, end)
	var shards = make([]*shard.Shard, 0, len(m.shard))
	for _, sd := range m.shard {
		// 验证 meta 字段
		if sd.Meta.ClusterName != clusterName {
			continue
		}
		if sd.Meta.Database != database {
			continue
		}
		if sd.Meta.TagRouter != tagRouter {
			continue
		}
		if sd.Meta.RetentionPolicy != retentionPolicy {
			continue
		}
		if sd.Meta.TagRouter != tagRouter {
			continue
		}

		// 判断是否是过期的 shard，只有过期的 shard 才进行查询
		if sd.Spec.Expired.Unix() > time.Now().Unix() {
			continue
		}

		// 通过时间过滤
		if sd.Spec.Start.UnixNano() >= start && end < sd.Spec.End.UnixNano() {
			shards = append(shards, sd)
		}
	}
	return shards, nil
}

func TestQueryToMetric(t *testing.T) {
	spaceUid := "test_two_stage"
	db := "push_gateway_unify_query"
	measurement := "group"
	tableID := fmt.Sprintf("%s.%s", db, measurement)
	field := "unify_query_request_handler_total"
	field01 := "unify_query_request_handler01_total"
	dataLabel := "unify_query"
	storageID := "2"
	clusterName := "demo"

	storageIdInt, _ := strconv.ParseInt(storageID, 10, 64)

	ctx := context.Background()
	mock.SetRedisClient(ctx, "test")
	mock.SetSpaceTsDbMockData(
		ctx,
		"query_ts_test.db",
		"query_ts_test",
		ir.SpaceInfo{
			spaceUid: ir.Space{tableID: &ir.SpaceResultTable{TableId: tableID}},
		},
		ir.ResultTableDetailInfo{
			tableID: &ir.ResultTableDetail{
				Fields:          []string{field, field01},
				MeasurementType: redis.BKTraditionalMeasurement,
				DataLabel:       dataLabel,
				StorageId:       storageIdInt,
				ClusterName:     clusterName,
				DB:              db,
				Measurement:     measurement,
				TableId:         tableID,
			},
		},
		nil, nil,
	)
	router, _ := influxdb.GetSpaceTsDbRouter()
	ret := router.Print(ctx, "query_ts_test", false)
	fmt.Println(ret)

	var testCases = map[string]struct {
		query  *Query
		metric *md.QueryMetric
	}{
		"test table id query": {
			query: &Query{
				TableID:       TableID(tableID),
				FieldName:     field,
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        field,
						Fields:       []string{field},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    field,
				IsCount:       false,
			},
		},
		"test metric query": {
			query: &Query{
				FieldName:     field,
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        field,
						Fields:       []string{field},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    field,
				IsCount:       false,
			},
		},
		"test two stage metric query": {
			query: &Query{
				TableID:       TableID(dataLabel),
				FieldName:     field,
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        field,
						Fields:       []string{field},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    field,
				IsCount:       false,
			},
		},
		"test regexp metric query": {
			query: &Query{
				TableID:       TableID(tableID),
				FieldName:     "unify_query_.*_total",
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
				IsRegexp:      true,
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        "unify_query_.*_total",
						Fields:       []string{field, field01},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    "unify_query_.*_total",
				IsCount:       false,
			},
		},
	}
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = context.Background()
			metric, err := c.query.ToQueryMetric(ctx, spaceUid)
			assert.Nil(t, err)
			assert.Equal(t, 1, len(c.metric.QueryList))
			if err == nil {
				assert.Equal(t, c.metric.QueryList[0].TableID, metric.QueryList[0].TableID)
				assert.Equal(t, c.metric.QueryList[0].Field, metric.QueryList[0].Field)
				assert.Equal(t, c.metric.QueryList[0].Fields, metric.QueryList[0].Fields)
			}
		})
	}
}

func TestQueryToMetricWithOfflineDataArchiveQuery(t *testing.T) {
	ctx := context.Background()

	mock.SetRedisClient(ctx, "")

	testCases := map[string]struct {
		spaceUid      string
		tableID       string
		field         string
		referenceName string

		clusterName     string
		tagsKey         []string
		db              string
		measurement     string
		retentionPolicy string
		storageID       string
		vmRt            string

		tagRouter         string
		expectedStorageID string
		expired           time.Time

		start string
		end   string
	}{
		"offlineDataArchiveQuery": {
			spaceUid: "q_test", tableID: "pushgateway_bkmonitor_unify_query.__default__", field: "q_test", referenceName: "a",
			start: "0", end: "60",

			storageID: "2", clusterName: "cluster_internal", tagsKey: []string{"bk_biz_id"},
			db: "pushgateway_bkmonitor_unify_query", measurement: "unify_query_request_handler_total", retentionPolicy: "",
			tagRouter: "bk_biz_id==2", expired: time.Now().Add(-time.Minute),

			expectedStorageID: consul.OfflineDataArchive,
		},
		"notOfflineDataArchiveQuery": {
			spaceUid: "q_test", tableID: "pushgateway_bkmonitor_unify_query.__default__", field: "q_test", referenceName: "a",
			start: "0", end: "60",

			storageID: "2", clusterName: "cluster_internal", tagsKey: []string{"bk_biz_id"},
			db: "pushgateway_bkmonitor_unify_query", measurement: "unify_query_request_handler_total", retentionPolicy: "",
			tagRouter: "bk_biz_id==2", expired: time.Now().Add(time.Minute),

			expectedStorageID: "2",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			stoIdInt, _ := strconv.ParseInt(tc.storageID, 10, 64)
			mock.SetSpaceTsDbMockData(
				ctx, "query_ts_test", "query_ts_test",
				ir.SpaceInfo{
					tc.spaceUid: ir.Space{
						tc.tableID: &ir.SpaceResultTable{TableId: tc.tableID},
					},
				},
				ir.ResultTableDetailInfo{
					tc.tableID: &ir.ResultTableDetail{
						Fields:          []string{tc.field},
						MeasurementType: redis.BkSplitMeasurement,
						StorageId:       stoIdInt,
						ClusterName:     tc.clusterName,
						TagsKey:         tc.tagsKey,
						DB:              tc.db,
						Measurement:     tc.measurement,
						VmRt:            tc.vmRt,
					},
				},
				nil, nil,
			)
			mockMd := &m{
				shard: []*shard.Shard{
					{
						Meta: shard.Meta{
							ClusterName:     tc.clusterName,
							Database:        tc.db,
							RetentionPolicy: tc.retentionPolicy,
							TagRouter:       tc.tagRouter,
						},
						Spec: shard.Spec{
							Start:   time.Unix(0, 0),
							End:     time.Unix(6000, 0),
							Expired: tc.expired,
						},
					},
				},
			}
			mock.SetOfflineDataArchiveMetadata(mockMd)

			query := &Query{
				TableID:       TableID(tc.tableID),
				FieldName:     tc.field,
				ReferenceName: tc.referenceName,
				Start:         tc.start,
				End:           tc.end,
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "contains",
							Value:         []string{"2"},
						},
					},
				},
			}

			metric, err := query.ToQueryMetric(ctx, tc.spaceUid)
			assert.Nil(t, err)
			if len(metric.QueryList) > 0 {
				assert.Equal(t, tc.expectedStorageID, metric.QueryList[0].StorageID)
			} else {
				panic("query list length is 0")
			}
		})
	}

}
