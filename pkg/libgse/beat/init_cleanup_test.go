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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/logp"
	reloader2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/reloader"
	bkstorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
)

// resetBeatState restores the package level beat state so cleanup tests do not
// leak into other tests in this package.
func resetBeatState(t *testing.T) {
	t.Helper()

	// Stop() clears the package level logger when it runs; save the current
	// logger so the cleanup below restores it and tests stay order independent.
	savedLogger := logp.L
	t.Cleanup(func() {
		logp.SetLogger(savedLogger)

		resourceMutex.Lock()
		pidLocked = false
		storageInited = false
		resourceMutex.Unlock()

		mutex.Lock()
		commonBKBeat = BKBeat{
			Finished:    false,
			BeaterState: BeaterBeforeOpening,
			Done:        make(chan struct{}),
		}
		mutex.Unlock()
	})
}

func isDoneClosed(done chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func TestStopWithoutReloader(t *testing.T) {
	resetBeatState(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()

	// -T does not create a reloader and the client may already be released by
	// the framework shutdown path; Stop must still finish safely.
	if err := Stop(); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if !isDoneClosed(commonBKBeat.Done) {
		t.Fatal("Done channel was not closed")
	}

	// Repeated Stop is a no-op cleanup that reports the beat is not running.
	if err := Stop(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("second Stop() error = %v, want %v", err, ErrNotRunning)
	}
}

func TestStopWithoutInit(t *testing.T) {
	resetBeatState(t)

	// Cleanup must be safe before initialization succeeded.
	if err := Stop(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Stop() error = %v, want %v", err, ErrNotRunning)
	}
	if err := Stop(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("repeated Stop() error = %v, want %v", err, ErrNotRunning)
	}
}

func TestFreeResourceIdempotentWithoutInit(t *testing.T) {
	resetBeatState(t)

	freeResource()
	freeResource()
}

func TestFreeResourceClosesStorageOnce(t *testing.T) {
	resetBeatState(t)

	bkstorage.InitMemory()
	markStorageInited()

	freeResource()
	freeResource()

	err := bkstorage.Set("key", "value", 0)
	if !errors.Is(err, bkstorage.ErrStorageClosed) {
		t.Fatalf("storage was not closed exactly once, Set() error = %v", err)
	}
}

// TestFreeResourceClosesStorageExactlyOnce uses the closeStorage seam to count
// actual Close calls; the closed-state assertion above cannot distinguish one
// Close from two because MemoryStorage.Close is itself idempotent.
func TestFreeResourceClosesStorageExactlyOnce(t *testing.T) {
	resetBeatState(t)

	original := closeStorage
	t.Cleanup(func() { closeStorage = original })
	closeCount := 0
	closeStorage = func() { closeCount++ }

	markStorageInited()
	freeResource()
	freeResource()

	if closeCount != 1 {
		t.Fatalf("storage Close called %d times, want 1", closeCount)
	}
}

func TestStopReloaderTwice(t *testing.T) {
	resetBeatState(t)

	r := reloader2.NewReloader("cleanup-test", &commonBKBeat)
	resourceMutex.Lock()
	reloader = r
	resourceMutex.Unlock()

	// stopReloader must clear the global pointer so a repeated cleanup does not
	// close the reloader's done channel twice.
	stopReloader()
	stopReloader()

	resourceMutex.Lock()
	defer resourceMutex.Unlock()
	if reloader != nil {
		t.Fatal("reloader was not cleared after stopReloader")
	}
}

func TestStopAfterPartialInit(t *testing.T) {
	resetBeatState(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterFailToOpen
	mutex.Unlock()

	bkstorage.InitMemory()
	markStorageInited()

	if err := Stop(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Stop() error = %v, want %v", err, ErrNotRunning)
	}
	if err := bkstorage.Set("key", "value", 0); !errors.Is(err, bkstorage.ErrStorageClosed) {
		t.Fatalf("storage acquired during partial init was not released, Set() error = %v", err)
	}
}

func TestBKBeatStopIdempotent(t *testing.T) {
	resetBeatState(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()

	commonBKBeat.Stop()
	// A second call must not close the Done channel twice.
	commonBKBeat.Stop()

	if !isDoneClosed(commonBKBeat.Done) {
		t.Fatal("Done channel was not closed")
	}
}

// TestBKBeatStopConcurrent exercises the framework shutdown path racing with an
// explicit Stop: the Done channel and client must still be released exactly once.
func TestBKBeatStopConcurrent(t *testing.T) {
	resetBeatState(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			commonBKBeat.Stop()
		}()
	}
	wg.Wait()

	if !isDoneClosed(commonBKBeat.Done) {
		t.Fatal("Done channel was not closed")
	}
}
