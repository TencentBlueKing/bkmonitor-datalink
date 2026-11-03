// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package testwait is how a test waits for something that should happen: on
// the thing itself, bounded by a cap no working host comes near and by the
// test binary's deadline, never by a duration tuned to how fast a host runs.
// A test that must show something does not happen keeps its duration and
// says why it is enough (// fixed-wait:).
package testwait

import (
	"context"
	"testing"
	"time"
)

// margin is left before the test binary's deadline so a wait that runs out
// still fails with its own message rather than the binary's panic.
const margin = 5 * time.Second

// perWait caps one wait. It is not a speed a host must reach: something
// that should happen and has not within two minutes, on however loaded a
// host, has failed. Without it one failed wait would spend the binary's
// whole deadline, and every test after it would fail at once with nothing
// left to wait with.
const perWait = 2 * time.Minute

// Deadline is when a wait begun now in t gives up: two minutes on, or the
// test binary's deadline less a margin, whichever is first.
func Deadline(t testing.TB) time.Time {
	t.Helper()
	at, _ := bound(t)
	return at
}

// bound is Deadline and which of its two limits set it, for a wait that ran
// out to say.
func bound(t testing.TB) (time.Time, string) {
	at, by := time.Now().Add(perWait), "the cap on one wait"
	if deadline, ok := t.(interface{ Deadline() (time.Time, bool) }); ok {
		if binary, set := deadline.Deadline(); set && binary.Add(-margin).Before(at) {
			at, by = binary.Add(-margin), "the test binary's deadline"
		}
	}
	return at, by
}

// Expired is a channel that fires at Deadline, for a select that waits on a
// channel of its own.
func Expired(t testing.TB) <-chan time.Time {
	t.Helper()
	return time.After(time.Until(Deadline(t))) // fixed-wait: fires at the test's deadline, the bound itself
}

// Context is a context ending at Deadline, cancelled when t ends: a bound on
// an operation the case expects to finish.
func Context(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), Deadline(t)) // fixed-wait: ends at the test's deadline, the bound itself
	t.Cleanup(cancel)
	return ctx
}

// Until polls cond until it holds, failing t with what if Deadline passes
// first, and saying how long it waited and which bound ended it. The poll
// starts at a millisecond and backs off to 50 ms.
func Until(t testing.TB, what string, cond func() bool) {
	t.Helper()
	started := time.Now()
	deadline, by := bound(t)
	for step := time.Millisecond; !cond(); step = min(2*step, 50*time.Millisecond) { // fixed-wait: a condition wait bounded by Deadline
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen in %s, when %s ran out", what, time.Since(started).Round(time.Millisecond), by)
		}
		time.Sleep(step) // fixed-wait: the poll interval of a condition wait, not a bound
	}
}
