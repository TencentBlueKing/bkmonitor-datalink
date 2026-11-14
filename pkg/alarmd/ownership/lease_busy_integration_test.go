// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ownership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A busy acquisition names the lease in the way, as the store holds it: a
// lease under a worker session by its holder, owner epoch and session epoch;
// a lease of its own by its holder, owner epoch and deadline. It is still
// ErrLeaseBusy to every caller that asks.
func TestABusyAcquisitionNamesTheLeaseInTheWay(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	publishTo(t, store, "worker-1")
	session, err := store.AcquireWorkerSession(ctx, "worker-1", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	held, err := store.AcquireUnder(ctx, sessionLeaseQueryGroup, session, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	impostor := session
	impostor.Token = "another-process"
	_, err = store.AcquireUnder(ctx, sessionLeaseQueryGroup, impostor, time.Now(), time.Minute)
	var busy *LeaseBusyError
	if !errors.Is(err, ErrLeaseBusy) || !errors.As(err, &busy) {
		t.Fatalf("another process under the same session = %v, want a LeaseBusyError", err)
	}
	if busy.OwnerID != "worker-1" || busy.OwnerEpoch != held.Fence.OwnerEpoch || busy.SessionEpoch != session.Epoch {
		t.Fatalf("the lease in the way = %+v, want worker-1 under owner epoch %d and session epoch %d",
			busy, held.Fence.OwnerEpoch, session.Epoch)
	}

	own := execution.QueryGroupIdentity("qg-own-lease")
	at := time.Now()
	first, err := store.acquire(ctx, own, "worker-a", at, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.acquire(ctx, own, "worker-b", at, time.Minute, false)
	busy = nil
	if !errors.Is(err, ErrLeaseBusy) || !errors.As(err, &busy) {
		t.Fatalf("another owner while the lease lives = %v, want a LeaseBusyError", err)
	}
	if busy.OwnerID != "worker-a" || busy.OwnerEpoch != first.Fence.OwnerEpoch || busy.SessionEpoch != 0 ||
		busy.DeadlineUnixMilli <= at.UnixMilli() {
		t.Fatalf("the lease in the way = %+v, want worker-a under owner epoch %d, no session, a deadline ahead",
			busy, first.Fence.OwnerEpoch)
	}
}

// A takeover with no worker session held yet is refused before the store is
// asked: still ErrLeaseBusy, and told apart as one that named no holder.
func TestATakeoverWithoutASessionIsRefusedBeforeTheStoreIsAsked(t *testing.T) {
	store := newIntegrationStore(t)
	_, err := OpenSessionUnder(context.Background(), store, sessionLeaseQueryGroup,
		func() (WorkerSession, bool) { return WorkerSession{}, false }, time.Now(), time.Minute)
	var busy *LeaseBusyError
	if !errors.Is(err, ErrLeaseBusy) || !errors.Is(err, ErrNoWorkerSession) || errors.As(err, &busy) {
		t.Fatalf("no session held = %v, want ErrNoWorkerSession, still ErrLeaseBusy, naming no holder", err)
	}
}
