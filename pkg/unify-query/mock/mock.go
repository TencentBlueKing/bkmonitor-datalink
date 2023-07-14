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
	"sync"

	goRedis "github.com/go-redis/redis/v8"

	offlineDataArchiveMetadata "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/offlineDataArchive"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

var (
	once sync.Once
)

func logInit() {
	once.Do(func() {
		log.InitTestLogger()
	})
}

func SetOfflineDataArchiveMetadata(m offlineDataArchiveMetadata.Metadata) {
	offlineDataArchive.MockMetaData(m)
}

func SetSpaceAndProxyMockData(ctx context.Context, spaceUid string, tdb *redis.TsDB, proxy *ir.Proxy) {
	logInit()

	space := redis.Space{
		tdb.TableID: tdb,
	}
	sr, _ := influxdb.GetSpaceRouter("", "")
	sr.Add(ctx, spaceUid, space)

	proxyInfo := ir.ProxyInfo{
		tdb.TableID: proxy,
	}
	influxdb.MockRouter(proxyInfo)
}

func SetRedisClient(ctx context.Context, serverName string) {
	logInit()
	options := &goRedis.UniversalOptions{
		DB:    0,
		Addrs: []string{"127.0.0.1:6379"},
	}
	redis.SetInstance(ctx, serverName, options)
}
