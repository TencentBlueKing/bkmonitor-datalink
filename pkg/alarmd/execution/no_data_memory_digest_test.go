// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// The v4 memory digest (decision-020 batch 8, 2.7, K2): one memory has one
// digest whatever order and whatever segments its groups were folded in, and
// any change to the memory moves it.

func digestFixtureMemory() []NoDataGroupMemory {
	memory := make([]NoDataGroupMemory, 0, 40)
	for index := 0; index < 40; index++ {
		group := NoDataGroupMemory{GroupKey: fmt.Sprintf("group-%02d", index)}
		switch index % 4 {
		case 0:
			group.LastSeen = 90
		case 1:
			group.LastSeen, group.FirstAbsent = 30, 60
		case 2:
			group.FirstAbsent = 60
		default:
			group.LastSeen, group.FirstAbsent, group.SuppressedAt = 10, 20, 80
		}
		memory = append(memory, group)
	}
	return memory
}

func builtMemoryDigest(t *testing.T, memory []NoDataGroupMemory) (PlanNoDataMutation, MutationDigest) {
	t.Helper()
	built, err := BuildPlanNoDataMutation(noDataUpdate(memory...))
	if err != nil {
		t.Fatal(err)
	}
	return built, built.MemoryDigest
}

func foldedDigest(t *testing.T, mutation PlanNoDataMutation, groups []NoDataGroupMemory) MutationDigest {
	t.Helper()
	var accumulator NoDataMemoryDigestAccumulator
	for _, group := range groups {
		if err := accumulator.Add(group); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := accumulator.MemoryDigest(mutation)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// The same memory folded in any order, built or accumulated, has one digest.
func TestNoDataMemoryDigestIsTheSameInAnyGroupOrder(t *testing.T) {
	memory := digestFixtureMemory()
	mutation, want := builtMemoryDigest(t, memory)
	random := rand.New(rand.NewSource(7))
	for round := 0; round < 20; round++ {
		shuffled := append([]NoDataGroupMemory(nil), memory...)
		random.Shuffle(len(shuffled), func(left, right int) { shuffled[left], shuffled[right] = shuffled[right], shuffled[left] })
		if got := foldedDigest(t, mutation, shuffled); got != want {
			t.Fatalf("round %d: folded in a shuffled order = %s, want %s", round, got, want)
		}
		if _, got := builtMemoryDigest(t, shuffled); got != want {
			t.Fatalf("round %d: built from a shuffled order = %s, want %s", round, got, want)
		}
	}
}

// The accumulator a segmented read uses is the batch function: the batch
// digest equals the memory folded in segments, last segment first.
func TestNoDataMemoryDigestAccumulatorEqualsTheBatchDigest(t *testing.T) {
	memory := digestFixtureMemory()
	mutation, _ := builtMemoryDigest(t, memory)
	want, err := derivePlanNoDataMemoryDigest(mutation, memory)
	if err != nil {
		t.Fatal(err)
	}
	if want != mutation.MemoryDigest {
		t.Fatalf("batch digest %s, mutation states %s", want, mutation.MemoryDigest)
	}
	var accumulator NoDataMemoryDigestAccumulator
	for end := len(memory); end > 0; end -= 7 {
		start := end - 7
		if start < 0 {
			start = 0
		}
		for _, group := range memory[start:end] {
			if err := accumulator.Add(group); err != nil {
				t.Fatal(err)
			}
		}
	}
	if accumulator.Groups() != uint64(len(memory)) {
		t.Fatalf("folded %d groups, want %d", accumulator.Groups(), len(memory))
	}
	got, err := accumulator.MemoryDigest(mutation)
	if err != nil || got != want {
		t.Fatalf("accumulated in segments = %s, %v; want the batch digest %s", got, err, want)
	}

	// The empty memory too: nothing folded is a digest, not an error.
	empty, _ := builtMemoryDigest(t, nil)
	if got := foldedDigest(t, empty, nil); got != empty.MemoryDigest {
		t.Fatalf("empty memory folded = %s, want %s", got, empty.MemoryDigest)
	}
}

// Every change to the memory moves the digest: each field of one group, a
// group added, a group removed, two groups trading values, and each fact the
// header stores.
func TestNoDataMemoryDigestMovesWhenTheMemoryDoes(t *testing.T) {
	memory := digestFixtureMemory()
	base, want := builtMemoryDigest(t, memory)

	changed := map[string]func([]NoDataGroupMemory) []NoDataGroupMemory{
		"last seen": func(groups []NoDataGroupMemory) []NoDataGroupMemory { groups[1].LastSeen++; return groups },
		"first absent": func(groups []NoDataGroupMemory) []NoDataGroupMemory {
			groups[1].FirstAbsent++
			return groups
		},
		"suppressed at": func(groups []NoDataGroupMemory) []NoDataGroupMemory {
			groups[3].SuppressedAt++
			return groups
		},
		"key": func(groups []NoDataGroupMemory) []NoDataGroupMemory {
			groups[0].GroupKey = "group-renamed"
			return groups
		},
		"a group added": func(groups []NoDataGroupMemory) []NoDataGroupMemory {
			return append(groups, NoDataGroupMemory{GroupKey: "group-new", LastSeen: 90})
		},
		"a group removed": func(groups []NoDataGroupMemory) []NoDataGroupMemory { return groups[1:] },
		"two groups trading values": func(groups []NoDataGroupMemory) []NoDataGroupMemory {
			groups[0].GroupKey, groups[1].GroupKey = groups[1].GroupKey, groups[0].GroupKey
			return groups
		},
	}
	for name, change := range changed {
		t.Run(name, func(t *testing.T) {
			_, got := builtMemoryDigest(t, change(append([]NoDataGroupMemory(nil), memory...)))
			if got == want {
				t.Fatalf("the memory changed and the digest did not: %s", got)
			}
		})
	}

	facts := map[string]func(*PlanNoDataMutation){
		"roster version":        func(mutation *PlanNoDataMutation) { mutation.RosterVersion = "TARGET_STATIC/2" },
		"present as of":         func(mutation *PlanNoDataMutation) { mutation.PresentAsOf++ },
		"tracking exhausted at": func(mutation *PlanNoDataMutation) { mutation.TrackingExhaustedAt = 50 },
		"schedule revision":     func(mutation *PlanNoDataMutation) { mutation.ScheduleRevision = "revision-2" },
	}
	for name, change := range facts {
		t.Run(name, func(t *testing.T) {
			mutation := base
			change(&mutation)
			if got := foldedDigest(t, mutation, memory); got == want {
				t.Fatalf("the header fact changed and the digest did not: %s", got)
			}
		})
	}
}

// Deduplicating is the caller's, and this is why: a group folded twice is a
// different digest, never the same one - and never the digest of the memory
// without it, which is what an XOR fold would give.
func TestNoDataMemoryDigestCountsAGroupFoldedTwice(t *testing.T) {
	memory := digestFixtureMemory()
	mutation, want := builtMemoryDigest(t, memory)
	var accumulator NoDataMemoryDigestAccumulator
	for _, group := range append(append([]NoDataGroupMemory(nil), memory...), memory[5]) {
		if err := accumulator.Add(group); err != nil {
			t.Fatal(err)
		}
	}
	// The count says so first: the statement carries one group fewer than was
	// folded, and a digest of a memory nobody holds is refused.
	if _, err := accumulator.MemoryDigest(mutation); err == nil ||
		!strings.Contains(err.Error(), "folds 41 groups and the mutation states 40") {
		t.Fatalf("MemoryDigest() error = %v, want the fold and the statement disagreeing on the group count", err)
	}
	// And with the count made to agree, the sum still differs.
	padded := mutation
	padded.GroupCount++
	got, err := accumulator.MemoryDigest(padded)
	if err != nil {
		t.Fatal(err)
	}
	without, _ := builtMemoryDigest(t, append(append([]NoDataGroupMemory(nil), memory[:5]...), memory[6:]...))
	if got == want || got == without.MemoryDigest {
		t.Fatalf("a group folded twice digests as %s; want neither the memory (%s) nor the memory without it (%s)",
			got, want, without.MemoryDigest)
	}
}

// A group no record could store is refused as the canonical memory refuses it.
func TestNoDataMemoryDigestRefusesAGroupNoRecordCouldStore(t *testing.T) {
	for name, group := range map[string]NoDataGroupMemory{
		"no key":            {LastSeen: 90},
		"negative":          {GroupKey: "a", LastSeen: -1},
		"remembers nothing": {GroupKey: "a"},
	} {
		var accumulator NoDataMemoryDigestAccumulator
		if err := accumulator.Add(group); err == nil {
			t.Fatalf("%s: Add(%+v) was accepted", name, group)
		}
		if accumulator.Groups() != 0 {
			t.Fatalf("%s: a refused group was counted", name)
		}
	}
}

// The digest is the v4 one: a mutation states the schema it is in, and the
// schema is part of what the digest covers.
func TestNoDataMemoryDigestIsTakenUnderTheWrittenSchema(t *testing.T) {
	mutation, want := builtMemoryDigest(t, digestFixtureMemory())
	if mutation.SchemaVersion != NoDataMemorySchemaV4 {
		t.Fatalf("built schema = %d, want v4", mutation.SchemaVersion)
	}
	older := mutation
	older.SchemaVersion = NoDataMemorySchemaV3
	var accumulator NoDataMemoryDigestAccumulator
	if _, err := accumulator.MemoryDigest(older); err == nil {
		t.Fatal("a v3 statement was given a memory digest; this build writes only v4")
	}
	if want == "" {
		t.Fatal("no memory digest")
	}
}
