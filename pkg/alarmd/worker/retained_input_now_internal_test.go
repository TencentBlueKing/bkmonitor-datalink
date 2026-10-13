// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"sync"
	"testing"
)

// What the stream says it holds to another goroutine is its input retention
// alone - not what it retained for gaps, output or State - read while the
// series path keeps adding, and back to nothing when the stream lets go.
func TestTheStreamSaysItsRetainedInputToAnyGoroutine(t *testing.T) {
	stream := &streamedExecution{coordinator: &SlotExecutionCoordinator{}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = stream.RetainedInputBytesNow()
		}
	}()
	for i := 0; i < 100; i++ {
		stream.retainBytes(retainPhaseInput, 10)
	}
	wg.Wait()
	stream.retainBytes(retainPhaseGap, 7)
	stream.retainBytes(retainPhaseOutput, 5)
	stream.retainBytes(retainPhaseState, 3)
	if got := stream.RetainedInputBytesNow(); got != 1_000 {
		t.Fatalf("retained input now = %d, want the input share 1000", got)
	}
	stream.releaseProvisional()
	if got := stream.RetainedInputBytesNow(); got != 0 {
		t.Fatalf("after release = %d, want 0", got)
	}
}
