// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remote

import (
	"context"
	"encoding/json"
	"fmt"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ResultTableDetail struct {
	Token      string `json:"token"`
	ModifyTime int64  `json:"modify_time"`
}

type Reporter interface {
	Do(ctx context.Context, spaceUID string, tsList ...prompb.TimeSeries) error
	Close(ctx context.Context) error
}

type reporter struct {
	client goRedis.UniversalClient
	writer *PrometheusWriter

	key string
}

func NewSpaceReporter(key string, writerUrl string) (Reporter, error) {
	inst := redis.GetStorageRedisInstance()
	if inst == nil {
		return nil, fmt.Errorf("failed to create redis client")
	}

	report := &reporter{
		client: inst.Client,
		writer: NewPrometheusWriterClient("", writerUrl, map[string]string{}),
		key:    key,
	}

	logger.Infof("[cmdb_relation] start_space_reporter key: %s url: %s", key, writerUrl)
	return report, nil
}

func (r *reporter) Close(ctx context.Context) error {
	return r.writer.Close(ctx)
}

func (r *reporter) Do(ctx context.Context, spaceUID string, tsList ...prompb.TimeSeries) error {
	var (
		resultTableDetail ResultTableDetail

		err error
		res []byte
	)

	if ok := r.client.HExists(ctx, r.key, spaceUID).Val(); !ok {
		err = r.client.HSet(ctx, r.key, spaceUID, `{}`).Err()
		if err != nil {
			return err
		}
	}
	res, err = r.client.HGet(ctx, r.key, spaceUID).Bytes()
	if err != nil {
		return err
	}
	if err = json.Unmarshal(res, &resultTableDetail); err != nil {
		return err
	}

	// token is empty
	if resultTableDetail.Token == "" {
		logger.Warnf("[cmdb_relation] build in result table token is empty with %s", spaceUID)
		return nil
	}

	// 上报数据
	err = r.writer.WriteBatch(ctx, resultTableDetail.Token, prompb.WriteRequest{Timeseries: tsList})
	if err != nil {
		return err
	}
	return nil
}
