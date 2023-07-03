// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka_test

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/Shopify/sarama"
	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// BenchmarkPull :
func BenchmarkPull(b *testing.B) {
	var wg sync.WaitGroup
	outCh := make(chan define.Payload)
	killCh := make(chan error)
	groupPrefix := fmt.Sprintf("transfer-bench-%d", rand.Uint32())

	topic := utils.GetEnvOr("KAFKA_TOPIC", "test")

	conf := config.Configuration
	conf.Set(kafka.ConfKafkaVersion, utils.GetEnvOr("KAFKA_VERSION", "1.0.0"))
	conf.Set(kafka.ConfKafkaConsumerGroupPrefix, utils.GetEnvOr("KAFKA_GROUP_PREFIX", groupPrefix))
	conf.Set(kafka.ConfKafkaConsumerOffsetInitial, sarama.OffsetOldest)

	var (
		mqConfig    = config.NewMetaClusterInfo()
		kafkaConfig = mqConfig.AsKafkaCluster()
	)

	kafkaConfig.SetDomain(utils.GetEnvOr("KAFKA_DOMAIN", "localhost"))
	kafkaConfig.SetTopic(topic)
	port := conv.Int(utils.GetEnvOr("KAFKA_PORT", "9092"))
	kafkaConfig.SetPort(port)

	b.Logf("%s: %s\n", kafka.ConfKafkaVersion, conf.GetString(kafka.ConfKafkaVersion))
	b.Logf("%s: %s\n", "domain", kafkaConfig.GetDomain())
	b.Logf("%s: %d\n", "port", kafkaConfig.GetPort())
	b.Logf("%s: %s\n", kafka.ConfKafkaConsumerGroupPrefix, conf.GetString(kafka.ConfKafkaConsumerGroupPrefix))
	b.Logf("%s: %v\n", kafka.ConfKafkaConsumerOffsetInitial, conf.GetInt64(kafka.ConfKafkaConsumerOffsetInitial))
	b.Logf("topic: %s\n", topic)

	ctx := config.IntoContext(context.Background(), conf)
	ctx = config.MQConfigIntoContext(ctx, mqConfig)
	f := kafka.NewFrontend(ctx, topic)

	wg.Add(1)
	go func() {
		for err := range killCh {
			panic(err)
		}
		b.Logf("check killCh done\n")
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		f.Pull(outCh, killCh)
		b.Logf("pull done\n")
		wg.Done()
	}()

	b.Logf("benchmark N: %d\n", b.N)
	b.StartTimer()
	for index := 0; index < b.N; index++ {
		out := <-outCh
		b.Logf("out chan received: %s\n", string(out.(*define.JSONPayload).Data))
	}
	b.StopTimer()
	b.Logf("check outCh done\n")

	wg.Add(1)
	go func() {
		for out := range outCh {
			b.Logf("out chan remain: %s\n", string(out.(*define.JSONPayload).Data))
		}
		b.Logf("take remains done\n")
		wg.Done()
	}()

	err := f.Close()
	if err != nil {
		panic(err)
	}

	close(killCh)
	close(outCh)
	wg.Wait()
	b.Logf("frontend closed\n")
}
