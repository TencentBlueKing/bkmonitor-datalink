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
	"sync"

	"github.com/Shopify/sarama"
	"github.com/golang/mock/gomock"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/kafka"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// FrontendSuite :
type FrontendSuite struct {
	ConfigSuite
	newKafkaConfig        func(define.Configuration, map[string]interface{}) (*sarama.Config, error)
	newKafkaConsumerGroup func([]string, string, *sarama.Config) (sarama.ConsumerGroup, error)
}

// SetupTest :
func (s *FrontendSuite) SetupTest() {
	s.ConfigSuite.SetupTest()

	kafkaConfig := s.PipelineConfig.MQConfig.AsKafkaCluster()
	kafkaConfig.SetTopic("test")
	kafkaConfig.SetDomain("localhost")
	kafkaConfig.SetPort(9092)
	kafkaConfig.SetPartition(0)

	s.newKafkaConsumerGroup = kafka.NewKafkaConsumerGroup
	s.newKafkaConfig = kafka.NewKafkaConsumerConfig
}

// TearDownTest :
func (s *FrontendSuite) TearDownTest() {
	s.ConfigSuite.TearDownTest()
	kafka.NewKafkaConsumerGroup = s.newKafkaConsumerGroup
	kafka.NewKafkaConsumerConfig = s.newKafkaConfig
}

// TestPull :
func (s *FrontendSuite) TestPull() {
	ctrl := gomock.NewController(s.T())
	defer ctrl.Finish()

	var wg sync.WaitGroup
	topic := "test"

	message := []byte(`test`)
	payload := NewMockPayload(ctrl)
	payload.EXPECT().From(message).Return(nil)
	payload.EXPECT().To(gomock.Any()).DoAndReturn(func(v map[string]interface{}) error {
		v["message"] = message
		return nil
	})

	f := kafka.NewFrontend(s.CTX, topic)
	k := f.(*kafka.Frontend)
	k.PayloadCreator = func() define.Payload {
		return payload
	}

	cfg := sarama.NewConfig()
	kafka.NewKafkaConsumerConfig = func(_ define.Configuration, _ map[string]interface{}) (*sarama.Config, error) {
		return cfg, nil
	}

	session := NewMockConsumerGroupSession(ctrl)
	session.EXPECT().MarkMessage(gomock.Any(), "")

	msgCh := make(chan *sarama.ConsumerMessage)
	claim := NewMockConsumerGroupClaim(ctrl)
	claim.EXPECT().Messages().Return(msgCh).AnyTimes()

	group := NewMockConsumerGroup(ctrl)
	kafka.NewKafkaConsumerGroup = func([]string, string, *sarama.Config) (sarama.ConsumerGroup, error) {
		return group, nil
	}
	group.EXPECT().Close().Return(nil)
	group.EXPECT().Consume(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(c context.Context, ts []string, h sarama.ConsumerGroupHandler) error {
		s.NoError(k.ConsumeClaim(session, claim))
		return nil
	})
	outCh := make(chan define.Payload)
	killCh := make(chan error)

	wg.Add(1)
	go func() {
		for err := range killCh {
			panic(err)
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		msgCh <- &sarama.ConsumerMessage{
			Value: message,
		}
		close(msgCh)
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		output, ok := <-outCh
		s.True(ok)
		value := make(map[string]interface{})
		s.NoError(output.To(value))
		s.Equal(message, value["message"])
		wg.Done()
	}()

	f.Pull(outCh, killCh)

	close(killCh)
	close(outCh)
	s.NoError(f.Close())
	wg.Wait()
}
