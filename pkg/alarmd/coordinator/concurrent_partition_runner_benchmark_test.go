// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const benchmarkRecordsPerMessage = 128

func BenchmarkConcurrentRunnerScheduling(b *testing.B) {
	benchmarkConcurrentRunnerMatrix(b, 0)
}

func BenchmarkConcurrentRunnerWaitHiding(b *testing.B) {
	benchmarkConcurrentRunnerMatrix(b, 500*time.Microsecond)
}

func benchmarkConcurrentRunnerMatrix(b *testing.B, wait time.Duration) {
	for _, hotKey := range []bool{false, true} {
		shape := "disjoint"
		if hotKey {
			shape = "hot-key"
		}
		for _, workers := range []int{1, 2, 4, 8} {
			b.Run(fmt.Sprintf("%s/workers-%d", shape, workers), func(b *testing.B) {
				previous := runtime.GOMAXPROCS(workers)
				defer runtime.GOMAXPROCS(previous)
				benchmarkConcurrentRunner(b, workers, hotKey, wait)
			})
		}
	}
}

func benchmarkConcurrentRunner(b *testing.B, workers int, hotKey bool, wait time.Duration) {
	b.Helper()
	limits := ConcurrentRunnerLimits{
		PreparationWorkers: workers, StatefulWorkers: workers,
		MaxInflightMessages: workers * 8, MaxInflightBytes: workers * 8 * 64,
		MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: workers * 8,
	}
	runner, err := NewConcurrentRoutedPartitionRunner(
		context.Background(), benchmarkTaskBuilder{hotKey: hotKey, wait: wait}, completedCriticalPhase{},
		partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil, limits, nil,
	)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(benchmarkRecordsPerMessage)
	payload := make([]byte, 8)
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		binary.LittleEndian.PutUint64(payload, uint64(index))
		if err := runner.Submit(int64(index), payload); err != nil {
			b.Fatal(err)
		}
	}
	if err := runner.Drain(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	records := float64(b.N * benchmarkRecordsPerMessage)
	seconds := b.Elapsed().Seconds()
	if seconds > 0 {
		rate := records / seconds
		b.ReportMetric(rate, "records/s")
		b.ReportMetric(rate/float64(workers), "records/s/core")
	}
	b.ReportMetric(float64(workers), "logical-cores")
}

type benchmarkTaskBuilder struct {
	hotKey bool
	wait   time.Duration
}

func (builder benchmarkTaskBuilder) BuildMessageTask(
	_ context.Context,
	payload []byte,
) (RoutedMessageTask, error) {
	sequence := binary.LittleEndian.Uint64(payload)
	strategyID := "hot"
	if !builder.hotKey {
		strategyID = strconv.FormatUint(sequence, 10)
	}
	return &benchmarkRoutedTask{key: RuntimeKey{StrategyID: strategyID}, seed: sequence, wait: builder.wait}, nil
}

type benchmarkRoutedTask struct {
	key      RuntimeKey
	seed     uint64
	checksum uint64
	wait     time.Duration
}

func (task *benchmarkRoutedTask) RuntimeKeys() []RuntimeKey { return []RuntimeKey{task.key} }

func (task *benchmarkRoutedTask) Prepare(context.Context) error {
	value := task.seed
	for index := 0; index < benchmarkRecordsPerMessage; index++ {
		value = value*1_099_511_628_211 + uint64(index+1)
	}
	task.checksum = value
	return nil
}

func (task *benchmarkRoutedTask) Evaluate(context.Context) (MessageOutcome, error) {
	if task.wait > 0 {
		time.Sleep(task.wait)
	}
	value := task.checksum
	for index := 0; index < benchmarkRecordsPerMessage; index++ {
		value ^= value<<7 ^ uint64(index)
	}
	task.checksum = value
	return MessageOutcome{
		Kind: MessageOutcomeCompleted, Message: &MessageResult{Receipt: &contract.MessageReceiptV1{}},
	}, nil
}
