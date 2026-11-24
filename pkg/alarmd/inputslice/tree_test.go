// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package inputslice_test

import (
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
)

// series is one series' dimension values; a dimension absent from the map is
// a missing label.
type series map[string]string

// tracker keeps every leaf the tree ever had that was not split: the landed
// ones, which Pending no longer lists, and the pending ones.
type tracker struct {
	tree   *inputslice.Tree
	landed []execution.InputSlice
}

func (tr *tracker) leaves() []execution.InputSlice {
	all := append([]execution.InputSlice(nil), tr.landed...)
	for _, leaf := range tr.tree.Pending() {
		all = append(all, leaf.Slice)
	}
	return all
}

func (tr *tracker) land(t *testing.T, leaf inputslice.Leaf) {
	t.Helper()
	if err := tr.tree.Land(leaf.ID); err != nil {
		t.Fatalf("land %d: %v", leaf.ID, err)
	}
	tr.landed = append(tr.landed, leaf.Slice)
}

// promMatches reads a slice the way the store does: a Prometheus matcher
// anchors its expression again as ^(?:…)$, has no (?s) of its own, and reads
// a missing label as the empty value.
func promMatches(t *testing.T, slice execution.InputSlice, values series) bool {
	t.Helper()
	for _, condition := range slice.Conditions {
		pattern, err := regexp.Compile("^(?:" + condition.Pattern + ")$")
		if err != nil {
			t.Fatalf("pattern %q does not compile as a matcher: %v", condition.Pattern, err)
		}
		if pattern.MatchString(values[condition.Dimension]) == condition.Negated {
			return false
		}
	}
	return true
}

// assertPartition checks the property the tree exists for, independently of
// how it built its leaves: every series is in exactly one leaf, read either
// way.
func assertPartition(t *testing.T, leaves []execution.InputSlice, population []series) {
	t.Helper()
	for index, slice := range leaves {
		if err := slice.Validate(); err != nil {
			t.Fatalf("leaf %d does not validate: %v", index, err)
		}
		if slice.ConditionBytes() > inputslice.ConditionBudget {
			t.Fatalf("leaf %d takes %d bytes", index, slice.ConditionBytes())
		}
	}
	for _, values := range population {
		goMatches, storeMatches := 0, 0
		for _, slice := range leaves {
			matched, err := slice.Matches(values)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if matched {
				goMatches++
			}
			if promMatches(t, slice, values) {
				storeMatches++
			}
		}
		if goMatches != 1 || storeMatches != 1 {
			t.Fatalf("series %q is in %d leaves by Matches and %d by the store's matcher, want 1 and 1; leaves %s",
				values, goMatches, storeMatches, describe(leaves))
		}
	}
}

func describe(leaves []execution.InputSlice) string {
	var lines []string
	for _, slice := range leaves {
		var parts []string
		for _, condition := range slice.Conditions {
			parts = append(parts, fmt.Sprintf("%s neg=%v %q", condition.Dimension, condition.Negated, condition.Pattern))
		}
		lines = append(lines, "{"+strings.Join(parts, " & ")+"}")
	}
	return strings.Join(lines, " ")
}

// members is the series of population in slice.
func members(t *testing.T, slice execution.InputSlice, population []series) []series {
	t.Helper()
	var in []series
	for _, values := range population {
		matched, err := slice.Matches(values)
		if err != nil {
			t.Fatal(err)
		}
		if matched {
			in = append(in, values)
		}
	}
	return in
}

// sampleOf is the sample a discarded slice hands Split, as the worker keeps
// it (worker sampleSlice): each dimension's distinct values with the series
// that carried each, a missing label read as empty.
func sampleOf(dimensions []string, in []series) inputslice.Sample {
	values := map[string][]string{}
	for _, dimension := range dimensions {
		for _, series := range in {
			values[dimension] = append(values[dimension], series[dimension])
		}
	}
	return inputslice.SampleOf(values)
}

// awkwardRunes are characters that mean something in a bracket expression or
// in a regular expression, are not printable, are multi-byte, or are the
// replacement character itself.
var awkwardRunes = []string{
	"]", "[", "\\", "^", "-", ":", ".", "$", "*", "(", ")", "{", "}", "|", "+", "?",
	"\n", "\r", "\t", "\x00", "\x7f", " ", "中", "é", "😀", "�", "\U0010FFFF", "​",
}

// invalidUTF8 are byte strings that are not UTF-8, each of which Go's regexp
// reads as one U+FFFD per byte.
var invalidUTF8 = []string{"\xff", "\xe4\xb8", "\x80", "\xc0\xaf", "\xed\xa0\x80", "\xf0\x9f\x98"}

func randomValue(rng *rand.Rand) string {
	switch rng.Intn(10) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("svc-%d.svc", rng.Intn(1000))
	case 2:
		return fmt.Sprintf("api-%d-prod", rng.Intn(1000))
	case 3:
		return fmt.Sprintf("%d0", rng.Intn(10000))
	case 4:
		return invalidUTF8[rng.Intn(len(invalidUTF8))] + fmt.Sprint(rng.Intn(10)) + invalidUTF8[rng.Intn(len(invalidUTF8))]
	default:
		var builder strings.Builder
		length := 1 + rng.Intn(8)
		for index := 0; index < length; index++ {
			switch rng.Intn(3) {
			case 0:
				builder.WriteString(awkwardRunes[rng.Intn(len(awkwardRunes))])
			default:
				builder.WriteByte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"[rng.Intn(62)])
			}
		}
		return builder.String()
	}
}

func randomPopulation(rng *rand.Rand, dimensions []string, size int) []series {
	population := make([]series, 0, size)
	for index := 0; index < size; index++ {
		values := series{}
		for _, dimension := range dimensions {
			if rng.Intn(12) == 0 {
				continue // missing label
			}
			values[dimension] = randomValue(rng)
		}
		population = append(population, values)
	}
	return population
}

// TestEveryValueInExactlyOneLeafAfterAnySplits drives the tree from its public
// entry with random landings and splits, samples that are the whole slice, a
// prefix of it as a stream would deliver, or nothing, and checks after every
// step that the leaves partition both the series the samples came from and
// series no sample ever showed.
func TestEveryValueInExactlyOneLeafAfterAnySplits(t *testing.T) {
	dimensions := []string{"service", "pod", "zone"}
	for seed := int64(1); seed <= 12; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			population := randomPopulation(rng, dimensions, 300)
			unseen := randomPopulation(rng, dimensions, 300)
			probe := append(append([]series(nil), population...), unseen...)
			tree, err := inputslice.New(dimensions, 1+rng.Intn(12), dimensions[0])
			if err != nil {
				t.Fatal(err)
			}
			tr := &tracker{tree: tree}
			assertPartition(t, tr.leaves(), probe)
			for step := 0; step < 120; step++ {
				pending := tree.Pending()
				if len(pending) == 0 {
					break
				}
				leaf := pending[rng.Intn(len(pending))]
				if rng.Intn(4) == 0 {
					tr.land(t, leaf)
					continue
				}
				in := members(t, leaf.Slice, population)
				var sample inputslice.Sample
				switch rng.Intn(5) {
				case 0:
				case 1:
					sample = sampleOf(dimensions, in[:rng.Intn(len(in)+1)])
				default:
					sample = sampleOf(dimensions, in)
				}
				before := tree.Leaves()
				children, err := tree.Split(leaf.ID, sample)
				if err != nil {
					if !errors.Is(err, inputslice.ErrValueConcentrated) {
						t.Fatalf("split: %v", err)
					}
					if tree.Leaves() != before {
						t.Fatalf("a refused split changed the leaves from %d to %d", before, tree.Leaves())
					}
					tr.land(t, leaf)
					continue
				}
				if tree.Leaves() != before-1+len(children) {
					t.Fatalf("leaves went from %d to %d with %d children", before, tree.Leaves(), len(children))
				}
				if tree.Leaves() > inputslice.MaxLeaves {
					t.Fatalf("tree has %d leaves", tree.Leaves())
				}
				if tree.Discarded() > tree.Leaves()-1 {
					t.Fatalf("%d discarded with %d leaves", tree.Discarded(), tree.Leaves())
				}
				assertPartition(t, tr.leaves(), probe)
			}
		})
	}
}

// TestRootSplitConditions pins the conditions of a split without a sample
// to the forms of §7.3, worked out by hand: the fixed alphabet's 36
// characters in four groups of nine, the last group left to the complement,
// and the root's exhausted child negated.
func TestRootSplitConditions(t *testing.T) {
	tree, err := inputslice.New([]string{"pod"}, 1, "pod")
	if err != nil {
		t.Fatal(err)
	}
	pending := tree.Pending()
	if len(pending) != 1 || !pending[0].Slice.IsZero() {
		t.Fatalf("a tree of one leaf is the zero slice, got %+v", pending)
	}
	children, err := tree.Split(pending[0].ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []execution.InputSliceCondition{
		{Dimension: "pod", Pattern: "^(?s:.*)[0-8]$"},
		{Dimension: "pod", Pattern: "^(?s:.*)[9a-h]$"},
		{Dimension: "pod", Pattern: "^(?s:.*)[i-q]$"},
		{Dimension: "pod", Pattern: "^(?s:.*)[^0-9a-q]$"},
		{Dimension: "pod", Negated: true, Pattern: "^(?s:.+)$"},
	}
	if len(children) != len(want) {
		t.Fatalf("got %d children, want %d", len(children), len(want))
	}
	for index, child := range children {
		if len(child.Slice.Conditions) != 1 || child.Slice.Conditions[0] != want[index] {
			t.Fatalf("child %d is %+v, want %+v", index, child.Slice.Conditions, want[index])
		}
	}
	// The next level puts the new class in front of the one it refines, and
	// an exhausted child at depth one is the class alone, anchored.
	grandchildren, err := tree.Split(children[0].ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := grandchildren[1].Slice.Conditions[0].Pattern; got != "^(?s:.*)[9a-h][0-8]$" {
		t.Fatalf("second grandchild is %q", got)
	}
	if got := grandchildren[len(grandchildren)-1].Slice.Conditions[0].Pattern; got != "^[0-8]$" {
		t.Fatalf("exhausted grandchild is %q", got)
	}
	if tree.Discarded() != 2 {
		t.Fatalf("discarded %d, want 2", tree.Discarded())
	}
}

// TestSampledClassesEscapeBracketCharacters splits on a sample whose next
// characters are the ones that mean something in a bracket expression, and
// checks each child holds only its own share of them.
func TestSampledClassesEscapeBracketCharacters(t *testing.T) {
	var population []series
	for _, r := range []string{"]", "\\", "^", "-", "[", ":", "\n", "\x00", "中", "\xff", "a"} {
		for copyIndex := 0; copyIndex < 3; copyIndex++ {
			population = append(population, series{"pod": fmt.Sprintf("p%d%s", copyIndex, r)})
		}
	}
	population = append(population, series{}, series{"pod": ""})
	tree, err := inputslice.New([]string{"pod"}, 1, "pod")
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Pending()[0]
	children, err := tree.Split(root.ID, sampleOf([]string{"pod"}, population))
	if err != nil {
		t.Fatal(err)
	}
	tr := &tracker{tree: tree}
	assertPartition(t, tr.leaves(), population)
	nonEmpty := 0
	for _, child := range children {
		if len(members(t, child.Slice, population)) > 0 {
			nonEmpty++
		}
	}
	// Eleven distinct last characters and the empty values: every class and
	// the exhausted child should hold some of the sample.
	if nonEmpty != len(children) {
		t.Fatalf("only %d of %d children hold sampled series: %s", nonEmpty, len(children), describe(tr.leaves()))
	}
}

// TestFixedSuffixMakesProgress is values sharing a long suffix: each split
// on the slice holding them moves one character further from the end, and
// within the suffix's length plus one the series are spread over more than
// one child.
func TestFixedSuffixMakesProgress(t *testing.T) {
	for _, suffix := range []string{".svc", "-prod", "0"} {
		t.Run(suffix, func(t *testing.T) {
			var population []series
			for index := 0; index < 200; index++ {
				population = append(population, series{"pod": fmt.Sprintf("name-%d%s", index, suffix)})
			}
			tree, err := inputslice.New([]string{"pod"}, 1, "pod")
			if err != nil {
				t.Fatal(err)
			}
			tr := &tracker{tree: tree}
			leaf := tree.Pending()[0]
			for splits := 1; ; splits++ {
				if splits > len(suffix)+1 {
					t.Fatalf("still one slice holds every series after %d splits", splits-1)
				}
				children, err := tree.Split(leaf.ID, sampleOf([]string{"pod"}, members(t, leaf.Slice, population)))
				if err != nil {
					t.Fatal(err)
				}
				assertPartition(t, tr.leaves(), population)
				largest, largestCount := children[0], -1
				for _, child := range children {
					if count := len(members(t, child.Slice, population)); count > largestCount {
						largest, largestCount = child, count
					}
				}
				if largestCount < len(population) {
					break
				}
				leaf = largest
			}
		})
	}
}

// TestSingleValueSwitchesDimension is a slice whose series all share one
// value of the dimension being cut: the split moves to another dimension,
// keeps the slice's own condition, and never compares to the shared value.
// Four clusters, a quarter of the series each, split the root more evenly
// than two pods, half each, so the root is cut on cluster.
func TestSingleValueSwitchesDimension(t *testing.T) {
	dimensions := []string{"cluster", "pod"}
	var population []series
	for index := 0; index < 30; index++ {
		for _, cluster := range []string{"a1", "b2", "c3", "d4"} {
			population = append(population, series{"cluster": cluster, "pod": fmt.Sprintf("pod-%d", index%2)})
		}
	}
	population = append(population, series{"pod": "x"}, series{"cluster": "a1"}, series{})
	tree, err := inputslice.New(dimensions, 1, dimensions[0])
	if err != nil {
		t.Fatal(err)
	}
	tr := &tracker{tree: tree}
	leaf := tree.Pending()[0]
	// Cut on cluster until one slice holds only cluster a1.
	for {
		in := members(t, leaf.Slice, population)
		children, err := tree.Split(leaf.ID, sampleOf(dimensions, in))
		if err != nil {
			t.Fatal(err)
		}
		assertPartition(t, tr.leaves(), population)
		var next *inputslice.Leaf
		for index := range children {
			child := children[index]
			got := members(t, child.Slice, population)
			if len(got) > 0 && got[0]["cluster"] == "a1" {
				next = &child
			}
		}
		if next == nil {
			t.Fatal("no child holds cluster a1")
		}
		leaf = *next
		only := true
		for _, values := range members(t, leaf.Slice, population) {
			if values["cluster"] != "a1" {
				only = false
			}
		}
		if only {
			break
		}
	}
	clusterCondition := leaf.Slice.Conditions[len(leaf.Slice.Conditions)-1]
	in := members(t, leaf.Slice, population)
	children, err := tree.Split(leaf.ID, sampleOf(dimensions, in))
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, tr.leaves(), population)
	sawPodExhausted := false
	for _, child := range children {
		conditions := child.Slice.Conditions
		if len(conditions) != 2 || conditions[0] != clusterCondition || conditions[1].Dimension != "pod" {
			t.Fatalf("child after the switch is %s, want %+v and a pod condition", describe([]execution.InputSlice{child.Slice}), clusterCondition)
		}
		for _, condition := range conditions {
			if strings.Contains(condition.Pattern, "a1") {
				t.Fatalf("condition compares to the sampled value: %q", condition.Pattern)
			}
		}
		if conditions[1].Negated && conditions[1].Pattern == "^(?s:.+)$" {
			sawPodExhausted = true
		}
	}
	if !sawPodExhausted {
		t.Fatal("the switch did not include the new dimension's root exhausted child")
	}
	spread := 0
	for _, child := range children {
		if len(members(t, child.Slice, population)) > 0 {
			spread++
		}
	}
	if spread < 2 {
		t.Fatalf("the switch put every series in one child")
	}
}

// TestSwitchFromRootCarriesNoEmptyCondition is a Query Group whose first
// dimension has one value throughout: the root is cut on the second
// dimension straight away, and the first writes no condition.
func TestSwitchFromRootCarriesNoEmptyCondition(t *testing.T) {
	dimensions := []string{"cluster", "pod"}
	var population []series
	for index := 0; index < 40; index++ {
		population = append(population, series{"cluster": "c", "pod": fmt.Sprintf("p%d", index)})
	}
	tree, err := inputslice.New(dimensions, 1, dimensions[0])
	if err != nil {
		t.Fatal(err)
	}
	children, err := tree.Split(tree.Pending()[0].ID, sampleOf(dimensions, population))
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		if len(child.Slice.Conditions) != 1 || child.Slice.Conditions[0].Dimension != "pod" {
			t.Fatalf("child is %s, want a single pod condition", describe([]execution.InputSlice{child.Slice}))
		}
	}
	probe := append(append([]series(nil), population...), series{}, series{"cluster": "d"}, series{"pod": ""})
	assertPartition(t, (&tracker{tree: tree}).leaves(), probe)
}

// TestConcentratedOnEveryDimensionIsNamed is a slice whose series share one
// value on every dimension: no split can separate them, so the split is
// refused by name and the leaf stays as it was.
func TestConcentratedOnEveryDimensionIsNamed(t *testing.T) {
	dimensions := []string{"cluster", "pod"}
	population := []series{{"cluster": "c", "pod": "p"}, {"cluster": "c", "pod": "p"}}
	tree, err := inputslice.New(dimensions, 1, dimensions[0])
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Pending()[0]
	_, err = tree.Split(root.ID, sampleOf(dimensions, population))
	assertConcentrated(t, err, inputslice.ReasonSingleValue)
	if pending := tree.Pending(); len(pending) != 1 || pending[0].ID != root.ID || tree.Leaves() != 1 || tree.Discarded() != 0 {
		t.Fatalf("a refused split changed the tree: pending %+v, leaves %d, discarded %d", pending, tree.Leaves(), tree.Discarded())
	}
}

// TestMaxLeavesIsNamed splits without samples until the tree would pass
// MaxLeaves.
func TestMaxLeavesIsNamed(t *testing.T) {
	tree, err := inputslice.New([]string{"pod", "zone"}, 1, "pod")
	if err != nil {
		t.Fatal(err)
	}
	for {
		pending := tree.Pending()
		_, err := tree.Split(pending[0].ID, nil)
		if err != nil {
			assertConcentrated(t, err, inputslice.ReasonMaxLeaves)
			break
		}
		if tree.Leaves() > inputslice.MaxLeaves {
			t.Fatalf("tree has %d leaves", tree.Leaves())
		}
	}
	if tree.Discarded() > tree.Leaves()-1 || 2*inputslice.MaxLeaves-1 < tree.Discarded()+tree.Leaves() {
		t.Fatalf("discarded %d with %d leaves", tree.Discarded(), tree.Leaves())
	}
}

// TestInitialTreeReachesK0 builds the starting tree for every K_0 the tree
// can hold and checks it has at least that many leaves, all unread and
// together complete; a K_0 past what MaxLeaves allows is refused by name.
func TestInitialTreeReachesK0(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	dimensions := []string{"pod"}
	probe := randomPopulation(rng, dimensions, 400)
	reachable := 0
	for k0 := 0; k0 <= inputslice.MaxLeaves; k0++ {
		tree, err := inputslice.New(dimensions, k0, dimensions[0])
		if err != nil {
			assertConcentrated(t, err, inputslice.ReasonMaxLeaves)
			if k0 <= 2 {
				t.Fatalf("K_0 %d refused", k0)
			}
			continue
		}
		reachable = k0
		if tree.Leaves() < k0 || tree.Leaves() > inputslice.MaxLeaves {
			t.Fatalf("K_0 %d gave %d leaves", k0, tree.Leaves())
		}
		if len(tree.Pending()) != tree.Leaves() || tree.Discarded() != 0 {
			t.Fatalf("K_0 %d: %d pending of %d leaves, %d discarded", k0, len(tree.Pending()), tree.Leaves(), tree.Discarded())
		}
		assertPartition(t, (&tracker{tree: tree}).leaves(), probe)
	}
	// Each split without a sample replaces one leaf by five, so the largest
	// tree reachable within 64 leaves has 1 + 4·15 = 61.
	if reachable != 61 {
		t.Fatalf("largest K_0 reached is %d, want 61", reachable)
	}
}

// TestLandAndSplitRefuseSettledLeaves is the rule that only unlanded leaves
// change.
func TestLandAndSplitRefuseSettledLeaves(t *testing.T) {
	tree, err := inputslice.New([]string{"pod"}, 2, "pod")
	if err != nil {
		t.Fatal(err)
	}
	pending := tree.Pending()
	if err := tree.Land(pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := tree.Land(pending[0].ID); err == nil {
		t.Fatal("landed a leaf twice")
	}
	if _, err := tree.Split(pending[0].ID, nil); err == nil {
		t.Fatal("split a landed leaf")
	}
	if _, err := tree.Split(0, nil); err == nil {
		t.Fatal("split the already split root")
	}
	if _, err := tree.Split(1000, nil); err == nil {
		t.Fatal("split a leaf that does not exist")
	}
	if _, err := inputslice.New(nil, 1, ""); err == nil {
		t.Fatal("built a tree with no dimension")
	}
	if _, err := inputslice.New([]string{"pod", "pod"}, 1, "pod"); err == nil {
		t.Fatal("built a tree with a dimension twice")
	}
}

func assertConcentrated(t *testing.T, err error, reason string) {
	t.Helper()
	if !errors.Is(err, inputslice.ErrValueConcentrated) {
		t.Fatalf("got %v, want a value-concentrated error", err)
	}
	var concentrated *inputslice.ValueConcentratedError
	if !errors.As(err, &concentrated) || concentrated.Reason != reason {
		t.Fatalf("got %v, want reason %s", err, reason)
	}
}

// TestAStreamPrefixSampleNeverLumpsDistinctValuesForGood is a numbered fleet
// read in order, host-00 to host-23, each slice let go at the series that
// passes its room, as a slice is: the sample of every split is the front of
// the stream, never the whole slice. A class an early split took from such
// a sample lumps characters it did not see - [^0] at the tens digit holds
// both the 1x and the 2x hosts - and every character before it is "-tsoh"
// for all of them, so going deeper never separates them. The tree cuts that
// class instead, and every slice comes to fit with the series in exactly one
// slice, instead of distinct hosts being refused as one value. Rooms from
// four to nine series, as a slice limit shrinks while the slices before it
// hold their share.
func TestAStreamPrefixSampleNeverLumpsDistinctValuesForGood(t *testing.T) {
	var population []series
	for index := 0; index < 24; index++ {
		population = append(population, series{"host": fmt.Sprintf("host-%02d", index)})
	}
	for room := 4; room <= 9; room++ {
		t.Run(fmt.Sprint(room), func(t *testing.T) {
			tree, err := inputslice.New([]string{"host"}, 1, "host")
			if err != nil {
				t.Fatal(err)
			}
			tr := &tracker{tree: tree}
			for reads := 0; len(tree.Pending()) > 0; reads++ {
				if reads > 2*inputslice.MaxLeaves-1 {
					t.Fatalf("still reading after %d slices: %s", reads, describe(tr.leaves()))
				}
				leaf := tree.Pending()[0]
				in := members(t, leaf.Slice, population)
				if len(in) <= room {
					tr.land(t, leaf)
					continue
				}
				if _, err := tree.Split(leaf.ID, sampleOf([]string{"host"}, in[:room+1])); err != nil {
					t.Fatalf("split of %d series: %v", len(in), err)
				}
				assertPartition(t, tr.leaves(), population)
			}
			assertPartition(t, tr.leaves(), population)
		})
	}
}

// TestTheMostEvenDimensionIsCutFirst is a Query Group whose first dimension
// is nearly one value: 95 series of cloud "0" and 5 of "1", over 100 hosts
// host-00 to host-99. Cut on cloud, [0] and the rest, the larger part holds
// 95 of the 100 series; cut on host by its last digit - ten series a digit,
// the classes [0-2], [3-4], [5-7] and the complement [8-9] by the cumulative
// count - the largest holds 30. The root is cut on host, every child holding
// 30 series at most.
func TestTheMostEvenDimensionIsCutFirst(t *testing.T) {
	dimensions := []string{"cloud", "host"}
	var population []series
	for index := 0; index < 100; index++ {
		cloud := "0"
		if index%20 == 0 {
			cloud = "1"
		}
		population = append(population, series{"cloud": cloud, "host": fmt.Sprintf("host-%02d", index)})
	}
	tree, err := inputslice.New(dimensions, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	children, err := tree.Split(tree.Pending()[0].ID, sampleOf(dimensions, population))
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		if conditions := child.Slice.Conditions; len(conditions) != 1 || conditions[0].Dimension != "host" {
			t.Fatalf("child is %s, want a single host condition", describe([]execution.InputSlice{child.Slice}))
		}
		if got := len(members(t, child.Slice, population)); got > 30 {
			t.Fatalf("a child holds %d series, want 30 at most", got)
		}
	}
	if got := tree.CutOn(); got != "host" {
		t.Fatalf("cut on %q, want host", got)
	}
	assertPartition(t, (&tracker{tree: tree}).leaves(), append(population, series{}, series{"cloud": "0"}))
}

// TestASliceOfOneValueIsCutOnAnEarlierDimension is a tree started on its
// second dimension, host, by the fixed alphabet, whose [0-8] slice holds 40
// series of host "h1" over zones z1 to z4, ten each. On host the slice can
// only be refined to [1] and the rest of [0-8], the first holding all 40;
// on zone, the first dimension, by its last digit, each part holds 10. The
// slice is cut on zone, its host condition kept, and the tree built back
// from its shape - the split kept - has the same leaves.
func TestASliceOfOneValueIsCutOnAnEarlierDimension(t *testing.T) {
	dimensions := []string{"zone", "host"}
	var population []series
	for index := 0; index < 40; index++ {
		population = append(population, series{"zone": fmt.Sprintf("z%d", 1+index%4), "host": "h1"})
	}
	probe := append(append([]series(nil), population...),
		series{"zone": "z9", "host": "h2"}, series{"host": "h1"}, series{"zone": "z1"}, series{}, series{"zone": "z1", "host": "hx"})
	tree, err := inputslice.New(dimensions, 2, "host")
	if err != nil {
		t.Fatal(err)
	}
	var leaf *inputslice.Leaf
	for _, pending := range tree.Pending() {
		if len(members(t, pending.Slice, population)) == len(population) {
			leaf = &pending
		}
	}
	if leaf == nil {
		t.Fatalf("no starting slice holds every h1 series: %s", describe(leavesOf(tree)))
	}
	hostCondition := leaf.Slice.Conditions[0]
	children, err := tree.SplitKept(leaf.ID, sampleOf(dimensions, population))
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		conditions := child.Slice.Conditions
		if len(conditions) != 2 || conditions[0] != hostCondition || conditions[1].Dimension != "zone" {
			t.Fatalf("child is %s, want %+v and a zone condition", describe([]execution.InputSlice{child.Slice}), hostCondition)
		}
		if got := len(members(t, child.Slice, population)); got > 10 {
			t.Fatalf("a child holds %d series, want 10 at most", got)
		}
	}
	assertPartition(t, leavesOf(tree), probe)
	built, err := inputslice.FromShape(dimensions, tree.Shape())
	if err != nil {
		t.Fatalf("from its shape: %v", err)
	}
	if got, want := built.Leaves(), tree.Leaves(); got != want {
		t.Fatalf("built from its shape %d leaves, want %d", got, want)
	}
	assertPartition(t, leavesOf(built), probe)
	if got := built.CutOn(); got != "host" {
		t.Fatalf("built tree cut on %q, want host", got)
	}
}

// A tree starts on the dimension it is given, in the leaves asked, and
// whole with none: no read chose one. Best is the dimension a sample of the
// whole Query Group cuts first, none when the sample tells nothing apart.
func TestATreeStartsOnTheDimensionChosenOrWhole(t *testing.T) {
	dimensions := []string{"cloud", "host"}
	whole, err := inputslice.New(dimensions, 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if pending := whole.Pending(); len(pending) != 1 || !pending[0].Slice.IsZero() || whole.CutOn() != "" {
		t.Fatalf("a tree on no dimension: pending %+v, cut on %q, want the zero slice alone", pending, whole.CutOn())
	}
	started, err := inputslice.New(dimensions, 4, "host")
	if err != nil {
		t.Fatal(err)
	}
	if started.Leaves() < 4 || started.CutOn() != "host" {
		t.Fatalf("a tree on host: %d leaves, cut on %q, want 4 at least on host", started.Leaves(), started.CutOn())
	}
	for _, leaf := range started.Pending() {
		for _, condition := range leaf.Slice.Conditions {
			if condition.Dimension != "host" {
				t.Fatalf("a starting slice on %s, want host", describe([]execution.InputSlice{leaf.Slice}))
			}
		}
	}
	if _, err := inputslice.New(dimensions, 4, "pod"); err == nil {
		t.Fatal("started on a dimension not sliced on")
	}
	for _, test := range []struct {
		name   string
		sample inputslice.Sample
		want   string
	}{
		{"cloud of one value", inputslice.SampleOf(map[string][]string{"cloud": {"0", "0", "0", "0"}, "host": {"a1", "b2", "c3", "d4"}}), "host"},
		{"host of one value", inputslice.SampleOf(map[string][]string{"cloud": {"1", "2", "3", "4"}, "host": {"h", "h", "h", "h"}}), "cloud"},
		{"one value on both", inputslice.SampleOf(map[string][]string{"cloud": {"0", "0"}, "host": {"h", "h"}}), ""},
		{"no sample", nil, ""},
	} {
		cut, ok := inputslice.BestCut(dimensions, test.sample)
		got := cut.Dimension
		if got != test.want || ok != (test.want != "") {
			t.Errorf("%s: best %q %t, want %q", test.name, got, ok, test.want)
		}
	}
}

// TestTheEvenestCutIsJudgedOnSeries is a dimension of many values one of
// which most series carry: ten cloud ids, "0" on 91 of 100 series and the
// nine others one each; 100 hosts. By its values cloud would cut as evenly
// as host - ten values over ten last digits, three of ten in the largest
// part, as host's 30 of 100 - and, the leaf's own dimension first among
// equals, be taken. By the series the sample counts, cut on cloud the part
// of "0" holds 91 series and more - the classes go by the series each
// character carries - and on host the largest part holds 30: the root is
// cut on host, every child holding 30 series at most.
func TestTheEvenestCutIsJudgedOnSeries(t *testing.T) {
	dimensions := []string{"cloud", "host"}
	var population []series
	for index := 0; index < 100; index++ {
		cloud := "0"
		if index >= 91 {
			cloud = fmt.Sprint(index - 90)
		}
		population = append(population, series{"cloud": cloud, "host": fmt.Sprintf("host-%02d", index)})
	}
	tree, err := inputslice.New(dimensions, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	children, err := tree.Split(tree.Pending()[0].ID, sampleOf(dimensions, population))
	if err != nil {
		t.Fatal(err)
	}
	largest := 0
	for _, child := range children {
		if conditions := child.Slice.Conditions; len(conditions) != 1 || conditions[0].Dimension != "host" {
			t.Fatalf("child is %s, want a single host condition", describe([]execution.InputSlice{child.Slice}))
		}
		largest = max(largest, len(members(t, child.Slice, population)))
	}
	if largest > 30 {
		t.Fatalf("the largest child holds %d series, want 30 at most", largest)
	}
}

// TestAStartOnTheSampledCutIsEvenWhereValuesEndInDigits is the window
// measurement's shape: cloud "0" throughout, device names device-%025d,
// 20,000 series. Their last two digits are each spread evenly over 0 to 9.
// The fixed alphabet puts 0 to 8 in one class: a start of 21 leaves, two
// levels deep, holds 0.9 x 0.9 = 81% of the series in one slice. The cut the
// sample makes (BestCut) is on device, its last digits split by the series
// they carry into [0-2], [3-4], [5-7] and the rest [8-9]; a start on it
// (NewCut), the same classes at both levels, holds at most 0.3 x 0.3 = 9%,
// 1,800 series, in a slice.
func TestAStartOnTheSampledCutIsEvenWhereValuesEndInDigits(t *testing.T) {
	dimensions := []string{"cloud", "device"}
	var population []series
	for index := 0; index < 20_000; index++ {
		population = append(population, series{"cloud": "0", "device": fmt.Sprintf("device-%025d", index)})
	}
	cut, ok := inputslice.BestCut(dimensions, sampleOf(dimensions, population))
	if !ok || cut.Dimension != "device" {
		t.Fatalf("cut %+v %t, want one on device", cut, ok)
	}
	tree, err := inputslice.NewCut(dimensions, 21, cut)
	if err != nil {
		t.Fatal(err)
	}
	largest := 0
	for _, leaf := range tree.Pending() {
		largest = max(largest, len(members(t, leaf.Slice, population)))
	}
	if tree.Leaves() < 21 || largest > 1_800 {
		t.Fatalf("%d leaves, the largest holding %d series; want 21 at least, 1,800 at most", tree.Leaves(), largest)
	}
	if got := tree.RootCut(); got.Dimension != "device" {
		t.Fatalf("root cut on %q, want device", got.Dimension)
	}
	assertPartition(t, leavesOf(tree), population[:2_000])
}
