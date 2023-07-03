// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"sync"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type OffsetCommitFn func(topic string, partition int32, offset int64, metadata string)

type OffsetManager interface {
	Mark(*sarama.ConsumerMessage, string)
	Close()
}

type DelayOffsetManager struct {
	locker    utils.Semaphore
	wg        sync.WaitGroup
	interval  time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
	callback  OffsetCommitFn
	topic     string
	locked    map[int32]int64
	waiting   map[int32]int64
	committed map[int32]int64
	session   sarama.ConsumerGroupSession
}

func (m *DelayOffsetManager) RegisterSession(session sarama.ConsumerGroupSession) {
	if m.session == nil {
		m.session = session
	}
}

func (m *DelayOffsetManager) Mark(msg *sarama.ConsumerMessage) {
	m.once.Do(func() {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			logging.Infof("topic %s offset manager will check every %v", m.topic, m.interval)
			ticker := time.NewTicker(m.interval)
		loop:
			for {
				select {
				case <-m.ctx.Done():
					break loop

				case now := <-ticker.C:
					// 进入逻辑之前再确定 ctx.Done() 是否有接收到信号
					// 避免 ticker 抢到 ctx.Done 和 ticker 同时抢到执行权限时 ticker 高于 Done
					select {
					case <-m.ctx.Done():
						break loop
					default:
					}
					logging.Debugf("ready to commit offsets on topic %s at %v", m.topic, now)

					// 提交往前退一个周期 即当次提交只会提交上一个周期内的 commit offset
					// 确保调度的时候不会丢点 但这个特性可能会导致调度时往前消费数据而拉高 CPU
					var forward bool
					for partition, offset := range m.locked {
						// 只有当 commit offset 向前推进的时候才 MarkOffset 保证相同 offset 不会被重复 commit
						if offset > m.committed[partition] {
							forward = true
							m.callback(m.topic, partition, offset, "")
						}
					}

					// 当且仅当 session 存在以及有提交内容的时候才 Commit
					if m.session != nil && forward {
						m.session.Commit()
					}

					// 提交完更新 committed 用于下一轮判断 offset 是否向前推进
					committed := make(map[int32]int64)
					for partition, offset := range m.locked {
						committed[partition] = offset
					}
					m.committed = committed

					if err := m.locker.Acquire(m.ctx, 1); err != nil {
						logging.Infof("topic %s acquire lock failed %v, break loop", m.topic, err)
						break loop
					}

					for partition, offset := range m.waiting {
						logging.Debugf("topic %s[%d] prepare to commit offset %d", m.topic, partition, offset)
						m.locked[partition] = offset
					}

					m.locker.Release(1)
				}
			}

			ticker.Stop()
			logging.Infof("topic %s offset manager finished", m.topic)
		}()
	})

	if m.locker.Acquire(m.ctx, 1) == nil {
		defer m.locker.Release(1)
		m.waiting[msg.Partition] = msg.Offset
	}
}

func (m *DelayOffsetManager) Close() {
	m.cancel()
	m.wg.Wait()
}

func NewDelayOffsetManager(ctx context.Context, callback OffsetCommitFn, topic string, interval time.Duration) *DelayOffsetManager {
	ctx, cancel := context.WithCancel(ctx)
	return &DelayOffsetManager{
		locker:   utils.NewWeightedSemaphore(1),
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
		callback: callback,
		topic:    topic,
		locked:   map[int32]int64{},
		waiting:  map[int32]int64{},
	}
}
