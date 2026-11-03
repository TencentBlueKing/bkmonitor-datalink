// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// handbackCandidatesForTest answers a fallback Leader's candidate reads with
// what the case sets, or the failure it sets.
type handbackCandidatesForTest struct {
	phaseTwoOwnershipRuntime
	candidates []ownership.InstanceRegistration
	err        error
}

func (reader *handbackCandidatesForTest) ReadActiveControlLeader(context.Context) (ownership.QueryGroupOwner, bool, error) {
	return ownership.QueryGroupOwner{}, true, nil
}

func (reader *handbackCandidatesForTest) ListPreferredControlCandidates(context.Context, time.Time) ([]ownership.InstanceRegistration, error) {
	return reader.candidates, reader.err
}

func (reader *handbackCandidatesForTest) ListFallbackControlCandidates(context.Context, time.Time) ([]ownership.InstanceRegistration, error) {
	return nil, nil
}

// A handback watch that starts over says so: a round that could not read the
// candidates, and a reading that comes more than two rounds after the last
// while a candidate's window was being counted, are each counted by reason
// and written as a line carrying the error or the gap. A gap with nothing
// being counted loses nothing and is not counted.
func TestAHandbackWatchThatStartsOverIsCountedAndSaid(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	maxGap := controlHandbackMaxGap(cfg)
	start := time.Unix(1_800_000_000, 0)
	ready := []ownership.InstanceRegistration{preferredRecordForTest("control", "1", true, start.Add(time.Hour))}
	type round struct {
		after      time.Duration
		candidates []ownership.InstanceRegistration
		err        error
	}
	for _, testCase := range []struct {
		name       string
		rounds     []round
		listFailed float64
		gap        float64
		line       string
	}{
		{name: "an unreadable round", rounds: []round{{candidates: ready}, {after: time.Second, err: errors.New("store unavailable")}},
			listFailed: 1, line: "read the preferred control candidates: store unavailable"},
		{name: "a reading past the gap while a window was counted",
			rounds: []round{{candidates: ready}, {after: maxGap + time.Second, candidates: ready}},
			gap:    1, line: "past the " + maxGap.String() + " a handback watch may go without one"},
		{name: "a reading within the gap", rounds: []round{{candidates: ready}, {after: maxGap, candidates: ready}}},
		{name: "a gap with nothing counted", rounds: []round{{}, {after: maxGap + time.Second}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := metric.NewRecorder(metric.BuildInfo{})
			var lines bytes.Buffer
			reader := &handbackCandidatesForTest{}
			now := start
			observer, err := newPhaseTwoRuntimeObserver(recorder, observability.New(observability.ComponentRuntime, &lines))
			if err != nil {
				t.Fatal(err)
			}
			bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
				Config: cfg, Ownership: reader, Recorder: recorder, Now: func() time.Time { return now }, Observer: observer,
			}}
			for _, round := range testCase.rounds {
				now = now.Add(round.after)
				reader.candidates, reader.err = round.candidates, round.err
				bundle.considerHandback(context.Background())
			}
			for reason, want := range map[string]float64{
				metric.ControlHandbackWatchResetListFailed: testCase.listFailed, metric.ControlHandbackWatchResetGap: testCase.gap,
			} {
				if got := counterValue(t, recorder, "alarmd_control_handback_watch_resets_total", map[string]string{"reason": reason}); got != want {
					t.Errorf("resets{reason=%s} = %v, want %v", reason, got, want)
				}
			}
			written := lines.String()
			if testCase.line == "" {
				if strings.Contains(written, observability.StageControlHandbackWatch) {
					t.Fatalf("lines = %q, want no handback watch line", written)
				}
				return
			}
			if strings.Count(written, observability.StageControlHandbackWatch) != 1 || !strings.Contains(written, testCase.line) {
				t.Fatalf("lines = %q, want one %s line carrying %q", written, observability.StageControlHandbackWatch, testCase.line)
			}
		})
	}
}

// The watch reads in one call from the fleet record: no candidate ready, or
// which one is, since when and when it is due; when the candidates were last
// read; and how often the watch started over.
func TestTheHandbackWatchSaysWhyTheLeaseHasNotBeenGivenBack(t *testing.T) {
	const window = time.Minute
	start := time.Unix(1_800_000_000, 0)
	var watch controlHandbackWatch
	empty := watch.facts(window)
	if empty.WindowSeconds != 60 || empty.LastReadingAt != nil || len(empty.Ready) != 0 ||
		empty.Resets[metric.ControlHandbackWatchResetListFailed] != 0 || empty.Resets[metric.ControlHandbackWatchResetGap] != 0 {
		t.Fatalf("an unread watch = %+v, want no reading, nobody ready and both resets at zero", empty)
	}
	ready := []ownership.InstanceRegistration{preferredRecordForTest("control", "7", true, start.Add(time.Hour))}
	watch.observe(ready, "fallback-self", start, window, 10*time.Second)
	watch.observe(ready, "fallback-self", start.Add(5*time.Second), window, 10*time.Second)
	watch.noteReset(metric.ControlHandbackWatchResetGap)
	facts := watch.facts(window)
	if facts.LastReadingAt == nil || !facts.LastReadingAt.Equal(start.Add(5*time.Second)) || len(facts.Ready) != 1 ||
		facts.Ready[0].Instance != "control" || facts.Ready[0].Incarnation != "7" || !facts.Ready[0].ReadySince.Equal(start) ||
		!facts.Ready[0].DueAt.Equal(start.Add(window)) || facts.Resets[metric.ControlHandbackWatchResetGap] != 1 {
		t.Fatalf("watch = %+v, want control#7 ready since the start, due a window later, read at +5s, one gap", facts)
	}
	watch.observe(nil, "fallback-self", start.Add(10*time.Second), window, 10*time.Second)
	if facts := watch.facts(window); len(facts.Ready) != 0 {
		t.Fatalf("watch after the candidate went = %+v, want nobody ready", facts)
	}
}

// A candidate store that stays down writes the first failure and then holds
// the rest for the limiter's window, so a long outage is one line a minute
// with the count of the ones it held, not one a round.
func TestALongCandidateOutageWritesOneLineNotOneARound(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var lines bytes.Buffer
	observer, err := newPhaseTwoRuntimeObserver(recorder, observability.New(observability.ComponentRuntime, &lines))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
		Config: validGoAccessRuntimeConfig(), Ownership: &handbackCandidatesForTest{err: errors.New("store unavailable")},
		Recorder: recorder, Now: func() time.Time { return now }, Observer: observer,
	}}
	for round := 0; round < 50; round++ {
		now = now.Add(5 * time.Second)
		bundle.considerHandback(context.Background())
	}
	if got := counterValue(t, recorder, "alarmd_control_handback_watch_resets_total",
		map[string]string{"reason": metric.ControlHandbackWatchResetListFailed}); got != 50 {
		t.Fatalf("list_failed = %v, want every round counted", got)
	}
	if written := strings.Count(lines.String(), observability.StageControlHandbackWatch); written != 1 {
		t.Fatalf("%d lines in a burst of 50 failing rounds, want 1: %s", written, lines.String())
	}
}
