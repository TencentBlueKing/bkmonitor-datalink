// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package inputslice

import (
	"errors"
	"slices"
	"testing"
)

// keptShape is a shape of a starting tree of four leaves or more with one
// slice split for its time by the sample, and the index of that split.
func keptShape(t *testing.T) (Shape, int) {
	t.Helper()
	tree, err := New([]string{"host"}, 4, "host")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.SplitKept(tree.Pending()[1].ID, SampleOf(map[string][]string{"host": {"abcd", "efgh", "ijkl", "mnop"}})); err != nil {
		t.Fatal(err)
	}
	shape := tree.Shape()
	return shape, len(shape.splits) - 1
}

// cloneShape is a copy of shape whose splits can be changed apart from it.
func cloneShape(shape Shape) Shape {
	copied := Shape{dimensions: slices.Clone(shape.dimensions)}
	for _, split := range shape.splits {
		children := make([][]segment, 0, len(split.children))
		for _, child := range split.children {
			children = append(children, slices.Clone(child))
		}
		copied.splits = append(copied.splits, shapeSplit{parent: slices.Clone(split.parent), children: children, kind: split.kind})
	}
	return copied
}

// A shape whose leaves are not a partition of every value is refused, by
// why, whichever way it fails: a child missing (a value in no leaf), a
// child twice or two children sharing values (a value in two), a split of a
// node that is no leaf, more leaves than a tree may have. The shape as made
// is not.
func TestAShapeWhoseLeavesAreNoPartitionIsRefused(t *testing.T) {
	shape, kept := keptShape(t)
	if _, err := FromShape([]string{"host"}, shape); err != nil {
		t.Fatalf("the shape as made: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*Shape)
		want   string
	}{
		{"a child missing", func(shape *Shape) {
			split := &shape.splits[kept]
			split.children = split.children[1:]
		}, ShapeRefusedNotAPartition},
		{"the exhausted child missing", func(shape *Shape) {
			split := &shape.splits[0]
			split.children = split.children[:len(split.children)-1]
		}, ShapeRefusedNotAPartition},
		{"a child twice", func(shape *Shape) {
			split := &shape.splits[kept]
			split.children = append(split.children, slices.Clone(split.children[0]))
		}, ShapeRefusedNotAPartition},
		{"two children sharing values", func(shape *Shape) {
			// The first child's last class takes the second's runes too:
			// every rune still in a child, some in two.
			split := &shape.splits[0]
			first, second := split.children[0], split.children[1]
			last, other := first[len(first)-1], second[len(second)-1]
			path := slices.Clone(last.path)
			widened := slices.Clone(path[len(path)-1].set)
			widened = append(widened, other.path[len(other.path)-1].set...)
			slices.Sort(widened)
			path[len(path)-1] = class{set: widened}
			first[len(first)-1] = segment{dimension: last.dimension, path: path, exhausted: last.exhausted}
		}, ShapeRefusedNotAPartition},
		{"a split of no leaf", func(shape *Shape) {
			shape.splits = append(shape.splits, shape.splits[kept])
		}, ShapeRefusedNotALeaf},
		{"more leaves than a tree may have", func(shape *Shape) {
			split := shape.splits[kept]
			for shape.Leaves() <= MaxLeaves {
				shape.splits = append(shape.splits, split)
			}
		}, ShapeRefusedMaxLeaves},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneShape(shape)
			test.change(&changed)
			_, err := FromShape([]string{"host"}, changed)
			var refused *ShapeRefusedError
			if !errors.As(err, &refused) || refused.Reason != test.want {
				t.Fatalf("err = %v, want refused %s", err, test.want)
			}
		})
	}
}

// partitionOf holds parts to be disjoint and to make up the class whole,
// explicit or negated.
func TestPartitionOfHoldsDisjointPartsThatMakeUpTheWhole(t *testing.T) {
	every := class{negated: true}
	for _, test := range []struct {
		name  string
		whole class
		parts []class
		want  bool
	}{
		{"every rune", every, []class{{set: []rune("ab")}, {set: []rune("c")}, {set: []rune("abc"), negated: true}}, true},
		{"every rune, a rune in none", every, []class{{set: []rune("a")}, {set: []rune("c")}, {set: []rune("abc"), negated: true}}, false},
		{"every rune, a rune in two", every, []class{{set: []rune("ab")}, {set: []rune("bc")}, {set: []rune("abc"), negated: true}}, false},
		{"every rune, two complements", every, []class{{set: []rune("a"), negated: true}, {set: []rune("b"), negated: true}}, false},
		{"every rune, no complement", every, []class{{set: []rune("ab")}}, false},
		{"explicit", class{set: []rune("abcd")}, []class{{set: []rune("ab")}, {set: []rune("cd")}}, true},
		{"explicit, a rune short", class{set: []rune("abcd")}, []class{{set: []rune("ab")}, {set: []rune("c")}}, false},
		{"explicit, a rune beyond", class{set: []rune("abc")}, []class{{set: []rune("ab")}, {set: []rune("cd")}}, false},
		{"negated", class{set: []rune("xy"), negated: true}, []class{{set: []rune("a")}, {set: []rune("axy"), negated: true}}, true},
		{"negated, a part outside it", class{set: []rune("xy"), negated: true}, []class{{set: []rune("ax")}, {set: []rune("axy"), negated: true}}, false},
		{"negated, a rune in none", class{set: []rune("xy"), negated: true}, []class{{set: []rune("a")}, {set: []rune("abxy"), negated: true}}, false},
	} {
		if got := partitionOf(test.whole, test.parts); got != test.want {
			t.Errorf("%s: partition %t, want %t", test.name, got, test.want)
		}
	}
}
