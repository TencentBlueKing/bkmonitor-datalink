// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"context"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"go.opentelemetry.io/otel/trace"
	"os"
	"sync"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	offlineDataArchiveMetadata "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/offlineDataArchive"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

var (
	once       sync.Once
	genTraceID int
	genSpanID  int
	lock       sync.Mutex
)

func Init(ctx context.Context) context.Context {
	once.Do(func() {
		config.CustomConfigFilePath = os.Getenv("UNIFY-QUERY-CONFIG-FILE-PATH")
		log.InitTestLogger()

		metadata.InitMetadata()
	})

	lock.Lock()
	defer lock.Unlock()
	genTraceID++
	genSpanID++
	tid, _ := trace.TraceIDFromHex(fmt.Sprintf("%032x", genTraceID))
	sid, _ := trace.SpanIDFromHex(fmt.Sprintf("%016x", genTraceID))
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid})
	return trace.ContextWithRemoteSpanContext(ctx, sc)
}

func SetOfflineDataArchiveMetadata(m offlineDataArchiveMetadata.Metadata) {
	offlineDataArchive.MockMetaData(m)
}

func SetSpaceTsDbMockData(
	ctx context.Context, path string, bucketName string, spaceInfo ir.SpaceInfo, rtInfo ir.ResultTableDetailInfo,
	fieldInfo ir.FieldToResultTable, dataLabelInfo ir.DataLabelToResultTable) {
	sr, err := influxdb.SetSpaceTsDbRouter(ctx, path, bucketName, "", 100)
	Init(ctx)
	if err != nil {
		panic(err)
	}
	for sid, space := range spaceInfo {
		err = sr.Add(ctx, ir.SpaceToResultTableKey, sid, &space)
		if err != nil {
			panic(err)
		}
	}
	for rid, rt := range rtInfo {
		err = sr.Add(ctx, ir.ResultTableDetailKey, rid, rt)
		if err != nil {
			panic(err)
		}
	}
	for field, rts := range fieldInfo {
		err = sr.Add(ctx, ir.FieldToResultTableKey, field, &rts)
		if err != nil {
			panic(err)
		}
	}
	for dataLabel, rts := range dataLabelInfo {
		err = sr.Add(ctx, ir.DataLabelToResultTableKey, dataLabel, &rts)
		if err != nil {
			panic(err)
		}
	}
}

func SetRedisClient(ctx context.Context, serverName string) {
	Init(ctx)
	host := viper.GetString("redis.host")
	port := viper.GetInt("redis.port")
	pwd := viper.GetString("redis.password")
	options := &goRedis.UniversalOptions{
		DB:       0,
		Addrs:    []string{fmt.Sprintf("%s:%d", host, port)},
		Password: pwd,
	}
	redis.SetInstance(ctx, serverName, options)
}
