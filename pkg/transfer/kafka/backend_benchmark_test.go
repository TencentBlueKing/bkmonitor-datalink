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
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/kafka"
)

// TestBackend_Auth: test auth
func BenchmarkAuth(b *testing.B) {
	producerConfig := func(conf define.Configuration) (*sarama.Config, error) {
		c := sarama.NewConfig()
		c.Producer.Partitioner = sarama.NewRandomPartitioner
		c.Producer.Return.Successes = false
		c.Net.SASL.Enable = true
		c.Net.SASL.User = "admin"
		c.Net.SASL.Password = "admin"
		c.Net.SASL.Handshake = true

		return c, c.Validate()
	}
	ctx := context.Background()
	conf := config.Configuration
	ctx = config.IntoContext(ctx, conf)
	kafka.NewKafkaProducerConfig = producerConfig
	backend, _ := kafka.NewKafkaBackend(ctx, "test")
	_ = backend.Close()
}

// runBackendForRealTest:测试真实数据
func (s *BackendSuit) runBackendForRealTest(producerConfig newProducerConfig, cases []testCase) {
	conf := config.Configuration
	s.CTX = config.IntoContext(s.CTX, conf)
	kafka.NewKafkaProducerConfig = producerConfig
	s.backend, _ = kafka.NewKafkaBackend(s.CTX, "test")

	for i := 0; i < 30*1000; i++ {
		for _, v := range cases {
			payload := define.NewJSONPayloadFrom([]byte(v.data), 1)
			s.backend.Push(payload, s.KillCh)
		}
	}
	s.CheckKillChan(s.KillCh)
	s.NotNil(s.backend)
	time.Sleep(1 * time.Second) // 等待 input 获取数据完成
	s.NoError(s.backend.Close())
}
