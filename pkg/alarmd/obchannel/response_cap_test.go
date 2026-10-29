// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// An answer's bound is a 32nd of the container's memory, between 16 MiB and
// 256 MiB, set once at start.
func TestTheResponseBoundIsAShareOfTheContainersMemory(t *testing.T) {
	for memory, want := range map[uint64]int{0: 16 << 20, 256 << 20: 16 << 20, 1 << 30: 32 << 20, 2 << 30: 64 << 20, 8 << 30: 256 << 20, 64 << 30: 256 << 20} {
		if got := ResponseCap(memory); got != want {
			t.Errorf("memory %d: bound %d, want %d", memory, got, want)
		}
	}
	before := MaxResponseBytes()
	t.Cleanup(func() { maxResponseBytes.Store(int64(before)) })
	SetMemoryLimit(1 << 30)
	if MaxResponseBytes() != 32<<20 {
		t.Fatalf("after a 1 GiB container: %d", MaxResponseBytes())
	}
}

// A busy agent's pace, several invocations a second for a minute, is never
// stopped by the session's rate; only a client looping without pause is.
func TestABusyAgentsPaceIsNotStopped(t *testing.T) {
	c := testChannel(t, &testAuth{})
	for i := 0; i < 300; i++ {
		if allowed, _ := c.allowInvoke("busy-agent"); !allowed {
			t.Fatalf("invocation %d in one minute was stopped", i+1)
		}
	}
	stopped := -1
	for i := 0; i < 1000; i++ {
		if allowed, _ := c.allowInvoke("looping"); !allowed {
			stopped = i
			break
		}
	}
	if stopped != InvokesPerSessionPerMinute {
		t.Fatalf("a loop was stopped at %s, want at %d", fmt.Sprint(stopped), InvokesPerSessionPerMinute)
	}
}

// A request has 30 seconds to arrive and be admitted: the read deadline
// set before its body is read.
func TestARequestHasThirtySecondsToArrive(t *testing.T) {
	c := testChannel(t, &testAuth{})
	raw, _ := json.Marshal(envelope(c, "discover", "", nil))
	r := httptest.NewRequest("POST", "/api/cli/channel", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer test-only")
	w := &requestDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	c.ServeHTTP(w, r)
	if len(w.read) == 0 || w.read[0].Sub(start) < RequestReadTimeout-time.Second || RequestReadTimeout != 30*time.Second {
		t.Fatalf("read deadlines %v from %v, want 30 s", w.read, start)
	}
}
