// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func retainedPoint(record, fingerprint string, levels int) execution.StateHistoryPoint {
	facts := make([]execution.StateLevelFact, levels)
	for index := range facts {
		facts[index] = execution.StateLevelFact{LevelID: uint32(index + 1),
			DetectFingerprint: fingerprint, Result: execution.LevelFactNormal}
	}
	return execution.StateHistoryPoint{RecordID: record, SourceTime: 1_788_000_000, Levels: facts}
}

func retainedView(points int, point execution.StateHistoryPoint) execution.RuntimeStateView {
	history := make([]execution.StateHistoryPoint, points)
	for index := range history {
		history[index] = point
	}
	return execution.RuntimeStateView{
		Identity: execution.StateKeyIdentity{SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64))},
		Status:   execution.StateFoundReady, History: history,
	}
}

// The strings a Level fact carries are charged, not just the array that holds
// them.
//
// This is the term a model of this cost went without for three days. It said
// 48 + len(RecordID) + 40 x cap(Levels) -- struct, record id, and the fact
// array's capacity -- and every part of that is charged and correct. What it
// left out is that each populated fact carries two strings of its own, and at
// the widths this system uses they are worth more than the array: a 64-byte
// detect fingerprint and the result word, 70 bytes for the ordinary one-Level
// point against the 40 the array costs. The gap read as an unexplained
// residual in a per-point figure, which is the shape a missing term takes when
// it is discovered by subtraction.
//
// Asserted as a difference rather than a constant: the same point with and
// without the strings, so the case says "the strings are charged" and stays
// true when a width changes.
func TestALevelFactsStringsAreChargedNotJustItsArray(t *testing.T) {
	const record, fingerprint = "record-id", "detect-fingerprint"
	empty := worker.PreparationObjectBytes(retainedPoint(record, "", 1))
	carried := worker.PreparationObjectBytes(retainedPoint(record, fingerprint, 1))
	if carried != empty+uint64(len(fingerprint)) {
		t.Fatalf("a point whose fact carries a %d-byte fingerprint costs %d against %d without it, a difference "+
			"of %d: the fingerprint inside the fact is not charged",
			len(fingerprint), carried, empty, carried-empty)
	}
	// The other string on the fact, by the same test: two results of
	// different widths, or a case that pins one string leaves the other free.
	short := retainedPoint(record, fingerprint, 1)
	short.Levels[0].Result = execution.LevelFactError
	long := retainedPoint(record, fingerprint, 1)
	long.Levels[0].Result = execution.LevelFactUnavailable
	widened := uint64(len(execution.LevelFactUnavailable) - len(execution.LevelFactError))
	if got := worker.PreparationObjectBytes(long) - worker.PreparationObjectBytes(short); got != widened {
		t.Fatalf("widening the result word by %d bytes moved the cost by %d: the result inside the fact is not charged",
			widened, got)
	}
	// And per Level, or a second fact's strings go uncharged while the first
	// one's are charged.
	two := worker.PreparationObjectBytes(retainedPoint(record, fingerprint, 2))
	perLevel := two - carried
	own := uint64(len(fingerprint) + len(execution.LevelFactNormal))
	if perLevel <= own {
		t.Fatalf("a second Level adds %d bytes, no more than the %d its own strings are: the strings are charged "+
			"for the first fact only", perLevel, own)
	}
}

// Everything in a series' retained State that is not its history is a fixed
// cost of the series, not of the window.
//
// The question this answers is which half to attack. A series retaining a long
// window is almost entirely that window -- at a thousand-odd points the rest is
// a rounding error -- so the lever is the retention bound and the point's own
// width, and nothing about the view around it. Measured rather than assumed,
// because the two are charged by one walk and a reader cannot tell them apart
// from the total.
func TestTheStateAroundTheHistoryDoesNotGrowWithTheWindow(t *testing.T) {
	point := retainedPoint(strings.Repeat("a", 64), strings.Repeat("f", 64), 1)
	rest := func(points int) uint64 {
		view := retainedView(points, point)
		whole := worker.PreparationObjectBytes(view)
		view.History = nil
		without := worker.PreparationObjectBytes(view)
		if whole <= without {
			t.Fatalf("%d points of history cost %d bytes", points, whole-without)
		}
		return without
	}
	short, long := rest(10), rest(1466)
	if short != long {
		t.Fatalf("the State around the history is %d bytes at ten points and %d at 1466: it is not a fixed cost of "+
			"the series, so the window is not the whole of what a long retention holds", short, long)
	}
	// And the window really is the whole of it at the lengths this is about.
	view := retainedView(1466, point)
	whole := worker.PreparationObjectBytes(view)
	if long*100 > whole {
		t.Fatalf("the State around the history is %d of %d bytes, over one percent: the claim that a retaining "+
			"series is its window no longer holds", long, whole)
	}
}
