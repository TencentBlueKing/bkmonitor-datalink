// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
)

// An inactive close the link refuses (every item 400) is sent once, and
// not again for inactiveRefusedRetry: the steps in between - one a second -
// send nothing. Past the interval it is sent once more, and each refusal is
// counted as such (job_done_refused). Once the link takes it, it settles.
func TestARefusedInactiveCloseWaitsBeforeItIsSentAgain(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	f.stub.itemRefused = true
	requests := func() int { return len(f.stub.requests("/api/v1/alerts/close")) }
	f.m.step(context.Background())
	first := requests()
	if first != 2 || len(f.closed()) != 1 {
		t.Fatalf("first step: %d requests, %d closes, want a dry run and a close", first, len(f.closed()))
	}
	for _, d := range []time.Duration{time.Second, time.Second, inactiveRefusedRetry - 3*time.Second} {
		f.advance(d)
		f.m.step(context.Background())
		if n := requests(); n != first {
			t.Fatalf("%d requests %s after the refusal, want none more within %s", n-first, d, inactiveRefusedRetry)
		}
	}
	f.advance(time.Second)
	f.m.step(context.Background())
	if n := requests(); n != 2*first || len(f.closed()) != 2 {
		t.Fatalf("%d requests, %d closes at %s: want the close sent once more", n, len(f.closed()), inactiveRefusedRetry)
	}
	if refused := f.m.filter.closer.Stats()[filterclose.OutcomeJobDoneRefused]; refused != 2 {
		t.Fatalf("job_done_refused %d, want each of the two refusals counted", refused)
	}
	// The link takes it the next time: it settles, and waits no more as
	// refused.
	f.stub.mu.Lock()
	f.stub.itemRefused = false
	f.stub.mu.Unlock()
	f.advance(inactiveRefusedRetry)
	f.m.step(context.Background())
	f.matchAgain(maintenanceMatches...)
	f.advance(time.Second)
	f.m.step(context.Background())
	if len(f.closed()) != 3 || f.m.filter.closer.Stats()[filterclose.OutcomeJobDone] != 1 {
		t.Fatalf("closes %d, filter close %v: want the third close taken and the next step to send nothing",
			len(f.closed()), nonZeroCounts(f.m.filter.closer.Stats()))
	}
}

// Every inactive close that ends neither settled nor having got somewhere
// waits inactiveRefusedRetry, whatever the reason (filterclose.NextAfter):
// the request refused (400: link_refused), the link failing (500:
// link_failed) or busy (429: link_busy) - sent once, then nothing for the
// steps of the interval, then once more. A close that got somewhere - items
// busy, found again by the filter (job_done_rederived) - is sent again the
// next step.
func TestAnInactiveCloseThatGotNowhereWaitsWhateverItsWord(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   func(*stubControlPlane)
		word  string
		again bool
	}{
		{"request refused", func(s *stubControlPlane) { s.closeStatus = http.StatusBadRequest }, filterclose.OutcomeLinkRefused, false},
		{"link failing", func(s *stubControlPlane) { s.closeStatus = http.StatusInternalServerError }, filterclose.OutcomeLinkFailed, false},
		{"link busy", func(s *stubControlPlane) { s.busy = true }, filterclose.OutcomeLinkBusy, false},
		{"items found again", func(s *stubControlPlane) { s.itemBusy = true }, filterclose.OutcomeJobDoneRederived, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
			f.stub.mu.Lock()
			tc.set(f.stub)
			f.stub.mu.Unlock()
			// Both routes: a strategy with no record is closed alert by
			// alert, its search first.
			requests := func() int {
				return len(f.stub.requests("/api/v1/alerts/close")) + len(f.stub.requests("/api/v1/alerts/search"))
			}
			f.m.step(context.Background())
			first := requests()
			if first == 0 || f.m.filter.closer.Stats()[tc.word] != 1 {
				t.Fatalf("first step: %d requests, filter close %v: want one close ended %s", first, nonZeroCounts(f.m.filter.closer.Stats()), tc.word)
			}
			f.advance(time.Second)
			f.m.step(context.Background())
			if sentAgain := requests() > first; sentAgain != tc.again {
				t.Fatalf("%d requests a second after a close ended %s: sent again %v, want %v", requests()-first, tc.word, sentAgain, tc.again)
			}
			if tc.again {
				return
			}
			f.advance(inactiveRefusedRetry - 2*time.Second)
			f.m.step(context.Background())
			if n := requests(); n != first {
				t.Fatalf("%d requests within %s of a close ended %s, want none", n-first, inactiveRefusedRetry, tc.word)
			}
			f.advance(time.Second)
			f.m.step(context.Background())
			if n := requests(); n != 2*first {
				t.Fatalf("%d requests %s after a close ended %s, want it sent once more", n-first, inactiveRefusedRetry, tc.word)
			}
		})
	}
}
