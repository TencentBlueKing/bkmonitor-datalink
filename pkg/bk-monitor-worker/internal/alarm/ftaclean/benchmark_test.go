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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

func BenchmarkClean(b *testing.B) {
	for _, limit := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("limit%d", limit), func(b *testing.B) {
			const events = 512
			p, msgs := fixture(b, make(slots, limit), events)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := p.clean(context.Background(), msgs)
				if err != nil || len(out[7]) != events*2 {
					b.Fatal("invalid output")
				}
			}
			b.ReportMetric(float64(b.N)*events/b.Elapsed().Seconds(), "events/s")
		})
	}
}

func BenchmarkPipeline(b *testing.B) {
	previous := config.FTAClean
	defer func() { config.FTAClean = previous }()
	config.FTAClean.BatchMessages = 128
	config.FTAClean.BatchBytes = 1 << 20
	config.FTAClean.BatchWait = 50 * time.Millisecond
	for _, scenario := range []string{"hot-partition", "four-partitions", "mixed-sources", "slow-output"} {
		for _, limit := range []int{1, 2, 4, 8, 16} {
			b.Run(fmt.Sprintf("%s/limit%d", scenario, limit), func(b *testing.B) {
				slots := make(slots, limit)
				hot, msgs := fixture(b, slots, 256)
				low, single := fixture(b, slots, 1)
				low.id = 43
				partitions := 1
				if scenario != "hot-partition" {
					partitions = 4
				}
				events := partitions * 256
				if scenario == "mixed-sources" {
					events = 3*256 + 1
				}
				var lowLatency time.Duration
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var wg sync.WaitGroup
					for part := 0; part < partitions; part++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							p, input := hot, msgs
							isLow := scenario == "mixed-sources" && part == 3
							if isLow {
								p, input = low, single
							}
							ch := make(chan *sarama.ConsumerMessage, 1)
							ch <- input[0]
							close(ch)
							start := time.Now()
							producer := testProducer{send: func(messages []*sarama.ProducerMessage) error {
								if len(messages) == 0 {
									b.Error("empty output")
								}
								if scenario == "slow-output" && part == 0 {
									time.Sleep(time.Millisecond)
								}
								return nil
							}}
							h := handler{pipeline: p, producers: map[uint]sarama.SyncProducer{7: producer}}
							session := &testSession{ctx: context.Background()}
							err := h.ConsumeClaim(session, testClaim{messages: ch})
							if len(session.marked) != 1 {
								b.Error("batch not acknowledged")
							}
							if err != nil {
								b.Error(err)
							}
							if isLow {
								lowLatency += time.Since(start)
							}
						}()
					}
					wg.Wait()
				}
				b.ReportMetric(float64(events*b.N)/b.Elapsed().Seconds(), "events/s")
				if lowLatency > 0 {
					b.ReportMetric(float64(lowLatency.Microseconds())/float64(b.N), "low-us/op")
				}
			})
		}
	}
}
