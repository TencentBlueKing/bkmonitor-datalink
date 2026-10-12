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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// slowBackend answers every MGET after a fixed delay, the way a store with a
// round trip does.
type slowBackend struct {
	*pipelineMemoryBackend
	delay time.Duration
	calls int
}

func (backend *slowBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	backend.calls++
	time.Sleep(backend.delay)
	return backend.pipelineMemoryBackend.MGet(ctx, keys)
}

// The preflight's time is split where the store's reads end: the reads,
// round trip included, are fetch; turning their bytes into views is decode.
// A caller that did not ask for the split gets the same result without it.
func TestThePreflightSplitsItsTimeAtTheStoreRead(t *testing.T) {
	version := applyVersion()
	backend := &slowBackend{pipelineMemoryBackend: newPipelineMemoryBackend(), delay: 20 * time.Millisecond}
	store := newBatchStore(t, backend, nil)
	items := make([]execution.StatePreflightItem, 3)
	for index := range items {
		identity := seriesIdentity(index)
		items[index] = execution.StatePreflightItem{Identity: identity, ApplyVersion: version}
		framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
		backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, version, 0, ""), 3)
	}
	request := execution.StatePreflightRequest{Contract: frozenRef(), Items: items}
	ctx, timing := execution.WithPreflightTiming(context.Background())
	timed, err := store.LoadRuntime(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Duration(backend.calls) * backend.delay; backend.calls == 0 || timing.Fetch < want {
		t.Fatalf("fetch %s over %d reads, want at least %s", timing.Fetch, backend.calls, want)
	}
	if timing.Decode <= 0 || timing.Decode >= timing.Fetch {
		t.Fatalf("decode %s, want some time and less than the store's %s", timing.Decode, timing.Fetch)
	}
	untimed, err := store.LoadRuntime(context.Background(), request)
	if err != nil || len(untimed.Items) != len(timed.Items) || untimed.LoadedBytes != timed.LoadedBytes {
		t.Fatalf("untimed %+v err %v, want the same read", untimed, err)
	}
}
