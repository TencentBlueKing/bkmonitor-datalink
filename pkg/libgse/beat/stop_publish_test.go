// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"errors"
	"sync"
	"testing"
	"time"

	reloader2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/reloader"
	bkstorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
	"github.com/elastic/beats/libbeat/beat"
)

// TestStopPublishWithoutInit documents that the forced preview cleanup is safe
// when the beat never reached the running state.
func TestStopPublishWithoutInit(t *testing.T) {
	resetBeatState(t)

	StopPublish()
	StopPublish()

	if commonBKBeat.getClient() != nil {
		t.Fatal("client was set by StopPublish without a client")
	}
	if isDoneClosed(commonBKBeat.Done) {
		t.Fatal("StopPublish closed the Done channel")
	}
}

// TestStopPublishClosesClientExactlyOnce covers the idempotency contract: the
// coordinator may call StopPublish before, and Stop again after it.
func TestStopPublishClosesClientExactlyOnce(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	fake := installFakeClient(t)

	StopPublish()
	if got := fake.closeCount(); got != 1 {
		t.Fatalf("client Close calls = %d, want 1", got)
	}
	if commonBKBeat.getClient() != nil {
		t.Fatal("StopPublish did not clear the client reference")
	}

	// Repeated calls must not close the same client twice.
	StopPublish()
	if got := fake.closeCount(); got != 1 {
		t.Fatalf("client Close calls after repeated StopPublish = %d, want 1", got)
	}
}

// TestStopPublishConcurrent verifies the reference swap is safe when the
// framework shutdown path races with the forced preview cleanup.
func TestStopPublishConcurrent(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	fake := installFakeClient(t)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			StopPublish()
		}()
	}
	wg.Wait()

	if got := fake.closeCount(); got != 1 {
		t.Fatalf("client Close calls = %d, want 1", got)
	}
}

// TestStopPublishKeepsReloaderStorageAndDone verifies the narrow contract: the
// forced preview path only releases the publisher. The reloader, the storage
// and the Done channel still belong to Stop, which runs after Registrar.Stop.
func TestStopPublishKeepsReloaderStorageAndDone(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()

	fake := installFakeClient(t)

	r := reloader2.NewReloader("stop-publish-test", &commonBKBeat)
	resourceMutex.Lock()
	reloader = r
	resourceMutex.Unlock()
	t.Cleanup(stopReloader)

	bkstorage.InitMemory()
	markStorageInited()
	t.Cleanup(bkstorage.Close)

	StopPublish()

	if got := fake.closeCount(); got != 1 {
		t.Fatalf("client Close calls = %d, want 1", got)
	}
	if isDoneClosed(commonBKBeat.Done) {
		t.Fatal("StopPublish closed the Done channel")
	}
	if err := bkstorage.Set("stop-publish-key", "value", 0); err != nil {
		t.Fatalf("storage was released by StopPublish: %v", err)
	}
	resourceMutex.Lock()
	keptReloader := reloader
	resourceMutex.Unlock()
	if keptReloader == nil {
		t.Fatal("reloader was cleared by StopPublish")
	}
}

// TestStopAfterStopPublish covers the normal framework cleanup after a forced
// preview abort: Stop must still finish the remaining resources without closing
// the already released client again.
func TestStopAfterStopPublish(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()

	fake := installFakeClient(t)

	bkstorage.InitMemory()
	markStorageInited()
	t.Cleanup(bkstorage.Close)

	StopPublish()
	StopPublish()

	if err := Stop(); err != nil {
		t.Fatalf("Stop() after StopPublish error = %v", err)
	}
	if !isDoneClosed(commonBKBeat.Done) {
		t.Fatal("Stop did not close the Done channel")
	}
	if got := fake.closeCount(); got != 1 {
		t.Fatalf("client Close calls = %d, want 1", got)
	}
	if err := bkstorage.Set("stop-publish-key", "value", 0); !errors.Is(err, bkstorage.ErrStorageClosed) {
		t.Fatalf("storage was not released by Stop, Set() error = %v", err)
	}

	// Calling the forced cleanup after Stop is a no-op.
	StopPublish()
	if got := fake.closeCount(); got != 1 {
		t.Fatalf("client Close calls after post-Stop StopPublish = %d, want 1", got)
	}
}

// blockingPublishClient is a beat.Client double whose Publish/PublishAll block
// until Close is called, mirroring a Task or Sender goroutine stuck inside the
// publisher on a full output queue. Close releases that wait, which is the only
// operation the preview abort can rely on.
type blockingPublishClient struct {
	mu        sync.Mutex
	closeDone sync.Once
	closed    int
	release   chan struct{}
	entered   chan struct{}
}

func newBlockingPublishClient() *blockingPublishClient {
	return &blockingPublishClient{
		release: make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
}

func (c *blockingPublishClient) waitForClose() {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-c.release
}

func (c *blockingPublishClient) Publish(Event) {
	c.waitForClose()
}

func (c *blockingPublishClient) PublishAll([]Event) {
	c.waitForClose()
}

func (c *blockingPublishClient) Close() error {
	c.mu.Lock()
	c.closed++
	c.mu.Unlock()
	// Closing the client is what unblocks a pending Publish. sync.Once keeps the
	// double safe if a test cleanup closes it after StopPublish already did.
	c.closeDone.Do(func() { close(c.release) })
	return nil
}

func (c *blockingPublishClient) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func installBlockingPublishClient(t *testing.T) *blockingPublishClient {
	t.Helper()
	fake := newBlockingPublishClient()
	var client beat.Client = fake
	commonBKBeat.setClient(&client)
	return fake
}

func waitForBlockedPublish(t *testing.T, c *blockingPublishClient) {
	t.Helper()
	select {
	case <-c.entered:
	case <-time.After(time.Second):
		t.Fatal("publish did not block before StopPublish")
	}
}

// TestStopPublishReleasesBlockedPublish covers the contract the -T preview
// coordinator depends on: StopPublish must close the installed client so a
// goroutine blocked inside Publish/PublishAll returns promptly, without
// releasing the reloader, the storage or the Done channel.
func TestStopPublishReleasesBlockedPublish(t *testing.T) {
	cases := []struct {
		name    string
		publish func(client beat.Client)
	}{
		{
			name: "Publish",
			publish: func(client beat.Client) {
				client.Publish(Event{Fields: MapStr{"field": "value"}})
			},
		},
		{
			name: "PublishAll",
			publish: func(client beat.Client) {
				client.PublishAll([]Event{{Fields: MapStr{"field": "value"}}})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetBeatState(t)
			resetManagedPreview(t)
			fake := installBlockingPublishClient(t)
			// Release the blocking double even if the assertion below fails, so
			// a regression cannot leak a permanently blocked goroutine.
			t.Cleanup(func() { _ = fake.Close() })

			client := commonBKBeat.getClient()
			if client == nil {
				t.Fatal("test client was not installed")
			}

			publishReturned := make(chan struct{})
			go func() {
				defer close(publishReturned)
				tc.publish(*client)
			}()
			waitForBlockedPublish(t, fake)

			start := time.Now()
			StopPublish()
			select {
			case <-publishReturned:
			case <-time.After(time.Second):
				t.Fatal("StopPublish did not release a blocked " + tc.name)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("blocked %s returned after %s, want < 1s", tc.name, elapsed)
			}

			if got := fake.closeCount(); got != 1 {
				t.Fatalf("client Close calls = %d, want 1", got)
			}
			if commonBKBeat.getClient() != nil {
				t.Fatal("StopPublish did not clear the client reference")
			}
			if isDoneClosed(commonBKBeat.Done) {
				t.Fatal("StopPublish closed the Done channel")
			}
		})
	}
}
