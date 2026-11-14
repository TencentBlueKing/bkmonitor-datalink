// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// gatedPipelineBackend holds its first pipeline until a second arrives:
// a request that sends its batches one after another never gets past it.
type gatedPipelineBackend struct {
	*pipelineMemoryBackend
	t       *testing.T
	mu      sync.Mutex
	calls   int
	arrived chan struct{}
}

func (backend *gatedPipelineBackend) CompareAndSetManyByDigest(ctx context.Context, guard *FenceGuard, writes []FencedWrite) ([]FencedWriteOutcome, error) {
	backend.mu.Lock()
	backend.calls++
	first := backend.calls == 1
	backend.mu.Unlock()
	if first {
		select {
		case <-backend.arrived:
		case <-testwait.Expired(backend.t):
			return nil, errors.New("alarmd test: the second batch was not sent while the first was out")
		}
	} else {
		select {
		case backend.arrived <- struct{}{}:
		default:
		}
	}
	return backend.pipelineMemoryBackend.CompareAndSetManyByDigest(ctx, guard, writes)
}

// A request of more than one batch exchanges the next while the one before
// is out (runtimeApplyBatchesInFlight), and every write lands.
func TestARequestsBatchesAreExchangedTwoAtOnce(t *testing.T) {
	mutations := seriesMutations(t, 2*runtimeApplyBatchItems+10, applyVersion(), 0)
	backend := &gatedPipelineBackend{pipelineMemoryBackend: newPipelineMemoryBackend(), t: t, arrived: make(chan struct{}, 1)}
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations,
	}, testApplyFence())
	if err != nil {
		t.Fatal(err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	if backend.calls != 3 || len(backend.values) != len(mutations) {
		t.Fatalf("%d pipelines left %d keys, want three pipelines and every key", backend.calls, len(backend.values))
	}
}

// A batch answered by a stale owner fails the request, as it did when the
// batches went one at a time, and the request returns with no batch of it
// still out.
func TestAStaleOwnerInOneBatchFailsTheRequestWithNoneOut(t *testing.T) {
	mutations := seriesMutations(t, 3*runtimeApplyBatchItems, applyVersion(), 0)
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(mutations))
	for index, mutation := range mutations {
		key, err := RuntimeStateKeyV3(store.options.Prefix, mutation.Identity)
		if err != nil {
			t.Fatal(err)
		}
		keys[index] = key
	}
	backend.staleKey = keys[runtimeApplyBatchItems+1]
	_, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations,
	}, testApplyFence())
	if !errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("err = %v, want the stale fence", err)
	}
	backend.pipelineMu.Lock()
	sent := backend.pipelines
	backend.pipelineMu.Unlock()
	time.Sleep(50 * time.Millisecond) // fixed-wait: a batch sent after the return would land in it; nothing to wait on
	backend.pipelineMu.Lock()
	defer backend.pipelineMu.Unlock()
	if backend.pipelines != sent {
		t.Fatalf("a batch was exchanged after the request returned: %d, then %d", sent, backend.pipelines)
	}
}
