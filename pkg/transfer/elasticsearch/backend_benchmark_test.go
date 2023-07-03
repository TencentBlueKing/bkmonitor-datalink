// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// BenchmarkPush :
func BenchmarkPush(b *testing.B) {
	b.StopTimer()

	killCh := make(chan error)

	conf := config.NewConfiguration()
	conf.SetDefault(pipeline.ConfKeyPayloadFlushInterval, utils.GetEnvOr("ES_FLUSH_INTERVAL", "1s"))
	conf.SetDefault(pipeline.ConfKeyPayloadFlushReties, conv.Int(utils.GetEnvOr("ES_FLUSH_RETRIES", "3")))
	conf.SetDefault(pipeline.ConfKeyPipelineChannelSize, conv.Int(utils.GetEnvOr("ES_BUFFER_SIZE", "100")))
	conf.SetDefault(pipeline.ConfKeyPayloadFlushConcurrency, conv.Int(utils.GetEnvOr("ES_BUFFER_CONCURRENCY", "10")))

	ctx := config.IntoContext(context.Background(), conf)
	ctx = config.PipelineConfigIntoContext(ctx, config.NewPipelineConfig())

	rt := config.MetaResultTableConfig{
		ResultTable: utils.GetEnvOr("ES_RESULT_TABLE", "test"),
		FieldList: []*config.MetaFieldConfig{
			{
				FieldName: "time",
				Type:      define.MetaFieldTypeTimestamp,
			},
		},
	}
	ctx = config.ResultTableConfigIntoContext(ctx, &rt)

	cluster := config.NewMetaClusterInfo()
	cluster.ClusterType = "elasticsearch"
	esCluster := cluster.AsElasticSearchCluster()
	esCluster.SetDomain(utils.GetEnvOr("ES_DOMAIN", "localhost"))
	esCluster.SetPort(conv.Int(utils.GetEnvOr("ES_PORT", "9200")))
	esCluster.SetIndex(utils.GetEnvOr("ES_INDEX", "test"))
	esCluster.SetVersion(utils.GetEnvOr("ES_VERSION", "v7"))
	ctx = config.ShipperConfigIntoContext(ctx, cluster)

	backend, err := elasticsearch.NewBackend(ctx, "test", 0)
	if err != nil {
		panic(err)
	}

	now := time.Now().Unix()
	b.Logf("benchmark N: %d\n", b.N)
	for i := 0; i < b.N; i++ {
		payload := define.NewJSONPayloadFrom([]byte(fmt.Sprintf(`{
			"time": %d,
			"dimensions":{"key":"%d"},
			"metrics":{"value":%d}
		}`, now+int64(i), rand.Int(), rand.Int())), 0)
		b.StartTimer()
		backend.Push(payload, killCh)
		b.StopTimer()
	}
	err = backend.Close()
	if err != nil {
		panic(err)
	}

	close(killCh)
	b.Logf("backend closed\n")
}
