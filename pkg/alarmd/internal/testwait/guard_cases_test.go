// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testwait

import (
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// guardCases are sources the guard is run over, each a way a fixed wait has
// been written past a guard, or a bounded wait it must let be. A line the
// guard must report ends in a /* want <kind> */ comment; every other line it
// must not report. The expectation is read from the source text, not from
// the syntax tree the guard reads.
var guardCases = map[string]string{
	"waits.go": `package waits

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// clock is not the time package, whatever its methods are called.
type clock struct{}

func (clock) After(time.Duration) <-chan struct{} { return nil }

func (clock) Sleep(time.Duration) {}

func TestWaits(t *testing.T) {
	ctx := t.Context()
	var c clock
	<-c.After(time.Second)
	c.Sleep(time.Second)
	time.Sleep(time.Second) // fixed-wait: nothing may arrive within the second
	time.Sleep(time.Second) /* want time.Sleep */ // fixed-wait:
	time.Sleep(time.Second) /* fixed-wait: a reason, in a block comment */ /* want time.Sleep */
	<-time.After(time.Second) // fixed-wait: the window the absence is shown in
	<-time.After(time.Second) /* want time.After */
	time.AfterFunc(time.Second, func() {}) /* want time.AfterFunc */
	ticker := time.NewTicker(time.Second) /* want time.NewTicker */
	defer ticker.Stop()
	timer := time.NewTimer(time.Second) /* want time.NewTimer */
	defer timer.Stop()
	for range time.Tick(time.Second) { /* want time.Tick */
	}
	sleep := time.Sleep /* want time.Sleep as a value */
	sleep(time.Second)
	a, cancelA := context.WithTimeout(context.TODO(), time.Second) /* want context.WithTimeout on context.TODO() */
	defer cancelA()
	b, cancelB := context.WithTimeout(t.Context(), time.Second) /* want context.WithTimeout on t.Context() */
	defer cancelB()
	d, cancelD := context.WithTimeout(ctx, time.Second) /* want context.WithTimeout on a variable */
	defer cancelD()
	e, cancelE := context.WithTimeout( /* want context.WithTimeout on context.Background() */
		context.Background(),
		time.Second)
	defer cancelE()
	f, cancelF := context.WithDeadline(ctx, time.Now().Add(time.Second)) /* want context.WithDeadline on a variable */
	defer cancelF()
	g, cancelG := context.WithDeadline(ctx, testwait.Deadline(t))
	defer cancelG()
	h, cancelH := context.WithTimeout(ctx, time.Second) // fixed-wait: the operation under test must give up within the second
	defer cancelH()
	i, cancelI := context.WithTimeoutCause(testwait.Context(t), time.Second, nil) /* want context.WithTimeoutCause on testwait.Context(t) */
	defer cancelI()
	_, _, _, _, _, _, _, _, _ = a, b, d, e, f, g, h, i, ctx
}
`,

	"loops.go": `package loops

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

func busyUntil(done func() bool) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) { /* want clock loop */
		if done() {
			return true
		}
	}
	return false
}

func busyForever(done func() bool) bool {
	deadline := time.Now().Add(10 * time.Second)
	for { /* want clock loop */
		if time.Now().After(deadline) {
			return false
		}
		if done() {
			return true
		}
	}
}

func elapsed(start time.Time, d time.Duration, done func() bool) {
	for time.Since(start) < d { /* want clock loop */
	}
	for !done() { /* want clock loop */
		if left := time.Until(start.Add(d)); left <= 0 {
			return
		}
	}
	for { /* want clock loop */
		switch {
		case time.Now().After(start.Add(d)):
			return
		}
	}
	// Waits for the next minute: the clock is compared with nothing that
	// could be the test's deadline.
	for { /* want clock loop */
		if time.Now().Second() == 0 {
			return
		}
	}
}

// A loop nested in another is reported once, at its own for.
func nested(t *testing.T, cases []int, done func(int) bool) {
	for _, c := range cases {
		deadline := time.Now().Add(time.Second)
		for !done(c) { /* want clock loop */
			if time.Now().After(deadline) {
				t.Fatal(c)
			}
		}
	}
}

func onTheTestsDeadline(t *testing.T, done func() bool) {
	deadline := testwait.Deadline(t)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
	}
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("not done")
		}
	}
	for deadline := testwait.Deadline(t); !done() && time.Now().Before(deadline); {
	}
	go func() {
		for !done() && time.Now().Before(deadline) {
		}
	}()
	for !done() {
		if left := time.Until(deadline); left <= 0 {
			t.Fatal("not done")
		}
	}
}

// The clock read here is a value handed to the code under test, not the
// clock the loop watches.
func clockAsData(acquire func(time.Time) (int, error), state func(time.Time) int) {
	var err error
	for round := 0; round < 3; round++ {
		if _, err = acquire(time.Now()); err != nil {
			return
		}
	}
	for _, word := range []string{"a", "b"} {
		if got := state(time.Now()); got != len(word) {
			return
		}
	}
}

// testwait.Deadline is computed afresh on every pass, so the loop never
// reaches it.
func inlineDeadline(t *testing.T) {
	for time.Now().Before(testwait.Deadline(t)) { /* want clock loop */
	}
}

func movedDeadline(t *testing.T) {
	deadline := testwait.Deadline(t)
	deadline = deadline.Add(time.Hour)
	for time.Now().Before(deadline) { /* want clock loop */
	}
}

func handedOutDeadline(t *testing.T, extend func(*time.Time)) {
	deadline := testwait.Deadline(t)
	extend(&deadline)
	for time.Now().Before(deadline) { /* want clock loop */
	}
}

// The clock set into a variable on a line of its own and tested later in the
// loop is the clock the loop watches.
func clockSetInTheBody(t *testing.T, start time.Time, d time.Duration, done func() bool) {
	for !done() { /* want clock loop */
		expired := time.Since(start) > d
		if expired {
			return
		}
	}
	var now time.Time
	for !done() { /* want clock loop */
		now = time.Now()
		if now.After(start.Add(d)) {
			return
		}
	}
	deadline := testwait.Deadline(t)
	for !done() {
		now := time.Now()
		if now.After(deadline) {
			t.Fatal("not done")
		}
	}
	for left := time.Until(deadline); !done(); left = time.Until(deadline) {
		if left <= 0 {
			t.Fatal("not done")
		}
	}
}

// A clock variable set from another, moved by a constant, or stepped in the
// loop's header is the clock still, and moved it is a fixed time again.
func clockThroughVariables(t *testing.T, start time.Time, done func() bool) {
	for !done() { /* want clock loop */
		now := time.Now()
		elapsed := now.Sub(start)
		if elapsed > time.Second {
			return
		}
	}
	deadline := testwait.Deadline(t)
	for !done() { /* want clock loop */
		now := time.Now()
		later := now.Add(119 * time.Second)
		if later.After(deadline) {
			return
		}
	}
	for !done() { /* want clock loop */
		now := time.Now()
		now = now.Add(119 * time.Second)
		if now.After(deadline) {
			return
		}
	}
	for now := time.Now(); !done(); now = now.Add(time.Millisecond) { /* want clock loop */
		if now.After(deadline) {
			return
		}
	}
	for now := time.Now(); !done(); now = time.Now() {
		if now.After(deadline) {
			t.Fatal("not done")
		}
	}
}

// A clock variable set some other way on one path is not the clock on it,
// and so is not plainly the clock at all.
func clockSetOtherwise(t *testing.T, start time.Time, early func() bool, done func() bool) {
	deadline := testwait.Deadline(t)
	for !done() { /* want clock loop */
		now := time.Now()
		if early() {
			now = start.Add(119 * time.Second)
		}
		if now.After(deadline) {
			return
		}
	}
}

// A variable set from a clock variable assigned further down the loop is
// the clock too: the next pass reads it so.
func clockSetBelow(start time.Time, done func() bool) {
	var now time.Time
	var elapsed time.Duration
	for !done() { /* want clock loop */
		elapsed = now.Sub(start)
		if elapsed > time.Second {
			return
		}
		now = time.Now()
	}
}

// The test's deadline moved by a constant is a fixed time again, whichever
// side of the comparison carries the arithmetic.
func shortenedDeadline(t *testing.T, done func() bool) {
	deadline := testwait.Deadline(t)
	for time.Now().Before(deadline.Add(-119 * time.Second)) { /* want clock loop */
	}
	for time.Until(deadline) > 119*time.Second { /* want clock loop */
	}
	for time.Now().Add(119 * time.Second).Before(deadline) { /* want clock loop */
	}
	for !done() && deadline.After(time.Now()) {
	}
}

// A goroutine started in a loop runs apart from it: its clock is not the one
// the loop waits on.
func goroutineInALoop(cases []int, start time.Time, d time.Duration) {
	for range cases {
		go func() {
			if time.Since(start) > d {
				return
			}
		}()
	}
}

func givenDeadline(deadline time.Time) {
	for time.Now().Before(deadline) { /* want clock loop */
	}
	for time.Now().Before(deadline) { // fixed-wait: the window the case shows nothing in
	}
}
`,

	"alias.go": `package alias

import stdtime "time"

func aliased(d stdtime.Duration) {
	stdtime.Sleep(d) /* want time.Sleep */
}
`,

	"shadow.go": `package shadow

import "time"

type sleeper struct{}

func (sleeper) Sleep(time.Duration) {}

func shadowed(d time.Duration) {
	{
		time := sleeper{}
		time.Sleep(d)
	}
	time.Sleep(d) /* want time.Sleep */
}
`,

	"dot.go": `package dot

import . "time"

func dotted(d Duration) {
	Sleep(d) /* want time.Sleep */
	<-After(d) // fixed-wait: the window the absence is shown in
}
`,
}

// The guard reads every test file and the test helpers under internal/, and
// no other code: a helper under internal/ waits as every test that calls it.
func TestTheGuardReadsTheTestsAndTheirHelpers(t *testing.T) {
	for path, read := range map[string]bool{
		"fleet/store_test.go":           true,
		"cmd/alarmd/runtime_test.go":    true,
		"internal/redistest/start.go":   true,
		"internal/testwait/testwait.go": true,
		"fleet/store.go":                false,
		"cmd/alarmd/main.go":            false,
		"internal/redistest/README.md":  false,
	} {
		if guarded(path) != read {
			t.Errorf("guarded(%q) = %t, want %t", path, !read, read)
		}
	}
}

// The guard reports each way a fixed wait has been hidden from it, at the
// wait's own line, and nothing that is bounded by the test's deadline or says
// why on its own line.
func TestTheGuardReportsExactlyTheWaitsThatDoNotSayWhy(t *testing.T) {
	for name, src := range guardCases {
		t.Run(name, func(t *testing.T) {
			var want []string
			for i, line := range strings.Split(src, "\n") {
				if _, after, ok := strings.Cut(line, "/* want "); ok {
					kind, _, _ := strings.Cut(after, " */")
					want = append(want, name+":"+strconv.Itoa(i+1)+": "+kind)
				}
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, wait := range fixedWaits(fset, file, name) {
				got = append(got, wait.String())
			}
			slices.Sort(want)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("the guard reported\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
			}
		})
	}
}
