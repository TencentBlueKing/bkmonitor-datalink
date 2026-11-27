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
	"sync"
	"time"

	"github.com/Shopify/sarama"
	"github.com/prometheus/client_golang/prometheus"
)

// Each partition owns its pending batch. At most one write is in flight per claim.
func consumeBatches(ctx context.Context, messages <-chan *sarama.ConsumerMessage, maxCount, maxBytes int, wait time.Duration, process func([]*sarama.ConsumerMessage) error, mark func(*sarama.ConsumerMessage)) error {
	var pending []*sarama.ConsumerMessage
	var size int
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var deadline <-chan time.Time
	flush := func(reason string) error {
		if len(pending) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flushes.WithLabelValues(reason).Inc()
		batchSize.Observe(float64(len(pending)))
		if err := process(pending); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// 写出成功且仍持有分区时才推进位点；重平衡后的接管方可重读该批。
		mark(pending[len(pending)-1])
		clear(pending)
		pending = pending[:0]
		size = 0
		deadline = nil
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			if err := flush("timeout"); err != nil {
				return err
			}
		case msg, ok := <-messages:
			if !ok {
				return flush("closed")
			}
			if len(pending) == 0 {
				timer.Reset(wait)
				deadline = timer.C
			}
			pending = append(pending, msg)
			size += len(msg.Value)
			if len(pending) >= maxCount || size >= maxBytes {
				reason := "count"
				if len(pending) < maxCount {
					reason = "bytes"
				}
				if err := flush(reason); err != nil {
					return err
				}
			}
		}
	}
}

// All shards borrow from the same slots. Jobs only touch their own result range.
type slots chan struct{}

func (s slots) run(ctx context.Context, n, chunk int, wait prometheus.Observer, fn func(int, int)) error {
	var wg sync.WaitGroup
	for start := 0; start < n; start += chunk {
		end := min(start+chunk, n)
		started := time.Now()
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case s <- struct{}{}:
		}
		wait.Observe(time.Since(started).Seconds())
		slotsUsed.Inc()
		if end == n || cap(s) == 1 {
			fn(start, end)
			slotsUsed.Dec()
			<-s
		} else {
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { slotsUsed.Dec(); <-s }(); fn(start, end) }()
		}
	}
	wg.Wait()
	return ctx.Err()
}
