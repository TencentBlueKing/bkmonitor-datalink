// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ftaclean

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

type testSession struct {
	sarama.ConsumerGroupSession
	ctx    context.Context
	marked []int64
}

func (s *testSession) Context() context.Context { return s.ctx }

func (s *testSession) MarkMessage(m *sarama.ConsumerMessage, _ string) {
	s.marked = append(s.marked, m.Offset)
}

type testClaim struct {
	sarama.ConsumerGroupClaim
	messages chan *sarama.ConsumerMessage
}

func (c testClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }

func (testClaim) Partition() int32 { return 0 }

func (testClaim) HighWaterMarkOffset() int64 { return 2 }

type testProducer struct {
	sarama.SyncProducer
	send func([]*sarama.ProducerMessage) error
}

func (p testProducer) SendMessages(msgs []*sarama.ProducerMessage) error { return p.send(msgs) }

func TestKafkaAcknowledgements(t *testing.T) {
	saved := config.FTAClean
	defer func() { config.FTAClean = saved }()
	config.FTAClean.BatchMessages = 1
	config.FTAClean.BatchBytes = 1024
	config.FTAClean.BatchWait = time.Second
	p, msgs := fixture(t, make(slots, 2), 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &testSession{ctx: ctx}
	claim := testClaim{messages: make(chan *sarama.ConsumerMessage, 2)}
	for i := 0; i < 2; i++ {
		claim.messages <- &sarama.ConsumerMessage{Offset: int64(i), Value: msgs[0].Value}
	}
	close(claim.messages)
	calls := 0
	prod := testProducer{send: func(out []*sarama.ProducerMessage) error {
		require.Len(t, out, 6)
		calls++
		if calls == 2 {
			cancel()
			return errors.New("partial broker failure")
		}
		return nil
	}}
	h := handler{pipeline: p, producers: map[uint]sarama.SyncProducer{7: prod}}
	require.ErrorIs(t, h.ConsumeClaim(session, claim), context.Canceled)
	require.Equal(t, 2, calls)
	require.Equal(t, []int64{0}, session.marked)
}

func TestPartialWriteRetriesFailedMessages(t *testing.T) {
	saved := config.FTAClean
	defer func() { config.FTAClean = saved }()
	config.FTAClean.BatchMessages = 1
	config.FTAClean.BatchBytes = 1024
	config.FTAClean.BatchWait = time.Second
	p, msgs := fixture(t, make(slots, 2), 3)
	session := &testSession{ctx: context.Background()}
	claim := testClaim{messages: make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- msgs[0]
	close(claim.messages)
	var pending []*sarama.ProducerMessage
	calls := 0
	prod := testProducer{send: func(batch []*sarama.ProducerMessage) error {
		calls++
		require.Empty(t, session.marked)
		if calls == 1 {
			require.Len(t, batch, 6)
			pending = []*sarama.ProducerMessage{batch[0], batch[2]}
			return sarama.ProducerErrors{&sarama.ProducerError{Msg: batch[2], Err: errors.New("retry")}, &sarama.ProducerError{Msg: batch[0], Err: errors.New("retry")}}
		}
		require.Equal(t, pending, batch)
		return nil
	}}
	h := handler{p, map[uint]sarama.SyncProducer{7: prod}}
	require.NoError(t, h.ConsumeClaim(session, claim))
	require.Equal(t, 2, calls)
	require.Equal(t, []int64{0}, session.marked)
}

func TestFailureWindow(t *testing.T) {
	var w failureWindow
	start := time.Now()
	require.Equal(t, 1, w.failure(start))
	require.Zero(t, w.failure(start.Add(time.Second)))
	require.Equal(t, 3, w.failure(start.Add(time.Minute)))
	require.Equal(t, 3, w.recovered())
	require.Zero(t, w.recovered())
	require.Equal(t, 1, w.failure(start.Add(time.Minute+time.Second)))
}
