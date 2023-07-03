// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package stores

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/instance"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
)

type InfluxDB struct {
	log log.Logger

	clusterName  string
	instanceName string
	dataDir      string

	targetName string
	targetDir  string

	tagName  string
	tagValue string

	address string
	client  http.Client
}

var _ Store = (*InfluxDB)(nil)

func NewInfluxDB(
	log log.Logger, clusterName, instanceName, tagName, tagValue,
	sourceDir, targetName, targetDir, address, username, password string,
) *InfluxDB {
	// todo address, username, password 通过option传进来

	if address == "" {
		address = fmt.Sprintf(
			"%s://%s:%d/", "http", "127.0.0.1", 8086,
		)
	}

	dataDir := fmt.Sprintf("%s/data", sourceDir)
	influxdb := &InfluxDB{
		log: log,

		clusterName:  clusterName,
		instanceName: instanceName,
		tagName:      tagName,
		tagValue:     tagValue,

		dataDir:    dataDir,
		targetName: targetName,
		targetDir:  targetDir,
		client: http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		address: address,
	}
	return influxdb
}

func (s *InfluxDB) GetLocalShards(ctx context.Context, database string) ([]shard.SimpleShard, error) {
	/*
		获取机器的shards信息，原理是通过show shards命令拿到结果进行解析
		{
		    "results": [
		        {
		            "statement_id": 0,
		            "series": [
		                {
		                    "name": "test_api",
		                    "columns": [
		                        "id",
		                        "database",
		                        "retention_policy",
		                        "shard_group",
		                        "start_time",
		                        "end_time",
		                        "expiry_time",
		                        "owners"
		                    ],
		                    "values": [
		                        [
		                            2,
		                            "test_api",
		                            "autogen",
		                            2,
		                            "2023-03-13T00:00:00Z",
		                            "2023-03-20T00:00:00Z",
		                            "",
		                            ""
		                        ]
		                    ]
		                }
		            ]
		        }
		    ]
		}
	*/

	// 获取URL
	url := fmt.Sprintf("%s/%s", s.address, "query?q=show%20shards")

	s.log.Infof(ctx, "get show shards, database: %s", database)
	// 获取请求
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		s.log.Errorf(ctx, "get request error, err:%s", err)
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Errorf(ctx, "get shards request error, err:%s", err)
		return nil, err
	}

	var rawDataInfo shard.RawDataInfo
	// 解析请求结果
	err = json.NewDecoder(resp.Body).Decode(&rawDataInfo)
	if err != nil {
		s.log.Errorf(ctx, "decode resp body error, err:%s", err)
		return nil, err
	}

	// simple shard : shard 简单的信息，，包含了开始时间，id
	var simpleShards []shard.SimpleShard
	for _, results := range rawDataInfo.Results {
		// 遍历所有的series
		for _, series := range results.Series {
			// 根据database 过滤
			if series.Name == database {
				// 遍历所有的value
				for _, values := range series.Values {
					/*
						   [
								  2,  // shard_id
								  "test_api", // database
								  "autogen", // rp
								  2, // sg
								  "2023-03-13T00:00:00Z", // start
								  "2023-03-20T00:00:00Z", // end
								  "", // expired
								  "" //owners
							]

					*/
					// todo 后续要识别返回的有 start 和 end，并直接查询到对应的下标进行取值
					if len(values) <= 6 {
						continue
					}
					// 格式化时间
					start, err := time.Parse("2006-01-02T15:04:05Z", values[4].(string))
					if err != nil {
						s.log.Errorf(ctx, "covert time error, err:%s", err)
						return nil, err
					}
					end, err := time.Parse("2006-01-02T15:04:05Z", values[5].(string))
					if err != nil {
						s.log.Errorf(ctx, "covert time error, err:%s", err)
						return nil, err
					}

					// 分片必须要完全结束了之后才参与同步，给 1h 作为缓冲区间，end时间超过 1h 之后，才进行操作
					if time.Since(end).Hours() < 1 {
						continue
					}

					// 封装 simpleshard对象
					var simpleShard = shard.SimpleShard{
						ShardID:         values[0].(float64),
						Database:        values[1].(string),
						RetentionPolicy: values[2].(string),
						Start:           start,
						End:             end,
					}

					s.log.Infof(ctx, "find a simple Shard, shardID: %f, database: %s, rp:%s, start: %s, end:%s",
						simpleShard.ShardID, simpleShard.Database, simpleShard.RetentionPolicy, simpleShard.Start, simpleShard.End,
					)

					simpleShards = append(simpleShards, simpleShard)
				}

			}
		}

	}

	return simpleShards, nil

}

func (s *InfluxDB) SetTarget(ctx context.Context, name, dir string) error {
	s.targetName = name
	s.targetDir = dir
	return nil
}

func (s *InfluxDB) GetActiveShards(
	ctx context.Context, db string, shards map[string]*shard.Shard,
) map[string]*shard.Shard {

	// 获取到本机文件当前的shards
	localShards, err := s.GetLocalShards(ctx, db)

	if err != nil {
		return nil
	}

	activeShards := make(map[string]*shard.Shard, len(shards))
	now := time.Now()
	for _, localShard := range localShards {
		sid := fmt.Sprintf("%.f", localShard.ShardID)
		shardPath := fmt.Sprintf("%s/%s/%s/%s", s.dataDir, db, localShard.RetentionPolicy, sid)
		targetPath := fmt.Sprintf("%s/%s/%s/%s/%s", s.targetDir, s.instanceName, db, localShard.RetentionPolicy, sid)

		sd := &shard.Shard{
			Ctx: ctx,
			Log: s.log,

			Meta: shard.Meta{
				ClusterName:     s.clusterName,
				Database:        db,
				RetentionPolicy: localShard.RetentionPolicy,
				TagName:         s.tagName,
				TagValue:        s.tagValue,
			},
			Spec: shard.Spec{
				Source: shard.Instance{
					InstanceType: INFLUXDB,
					Name:         s.instanceName,
					ShardID:      sid,
					Path:         shardPath,
				},
				Target: shard.Instance{
					InstanceType: instance.CosName,
					Name:         s.targetName,
					ShardID:      sid,
					Path:         targetPath,
				},
				Start: localShard.Start,
				End:   localShard.End,
			},
			Status: shard.Status{
				Code: shard.Move,
			},
		}

		// 当前运行时间必须要超过分片结束时间
		if now.After(localShard.End) {
			key := sd.Unique()
			s.log.Infof(ctx, "find a archive shard, shard: %s", key)
			if _, ok := activeShards[key]; !ok {
				if osd, ok := shards[key]; ok {
					activeShards[key] = osd
				} else {
					activeShards[key] = sd
				}
			}
		}
	}

	return activeShards
}
