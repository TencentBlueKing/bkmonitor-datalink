// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// BenchmarkPush :
func BenchmarkPush(b *testing.B) {
	b.StopTimer()

	var wg sync.WaitGroup
	killCh := make(chan error)

	dimensions := strings.Split(utils.GetEnvOr("INFLUX_DIMENSIONS", "a,b,c,d"), ",")

	conf := config.NewConfiguration()
	influxdb.InitConfiguration(conf)
	ctx := config.PipelineConfigIntoContext(context.Background(), config.NewPipelineConfig())
	ctx = config.IntoContext(ctx, conf)

	msc := config.NewMetaClusterInfo()
	msc.ClusterType = "influxdb"
	dbInfo := msc.AsInfluxCluster()
	dbInfo.SetDataBase(utils.GetEnvOr("INFLUX_DATABASE", "transfer"))
	dbInfo.SetTable(utils.GetEnvOr("INFLUX_TABLE", "transfer"))
	dbInfo.SetDomain(utils.GetEnvOr("INFLUX_DOMAIN", "127.0.0.1"))
	port := conv.Int(utils.GetEnvOr("INFLUX_PORT", "8086"))
	dbInfo.SetPort(port)
	ctx = config.ShipperConfigIntoContext(ctx, msc)

	influxInstance, err := influxdb.NewBackend(ctx, "transfer.transfer", 0)
	if err != nil {
		panic(err)
	}

	now := time.Now().Unix()

	b.Logf("benchmark N: %d\n", b.N)
	for i := 0; i < b.N; i++ {
		payload := define.NewJSONPayloadFrom([]byte(fmt.Sprintf(`{
			"time": %d,
			"dimensions":{"name":"%s"},
			"metrics":{"value":%d}
		}`, now+int64(i), dimensions[rand.Intn(len(dimensions))], rand.Int())), 0)
		b.StartTimer()
		influxInstance.Push(payload, killCh)
		b.StopTimer()
	}

	b.Logf("check outCh done\n")
	err = influxInstance.Close()
	if err != nil {
		panic(err)
	}
	b.StopTimer()
	close(killCh)
	wg.Wait()
	b.Logf("backend closed\n")
}
