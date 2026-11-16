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
	"fmt"
	"slices"
)

// splitKind is why a tree split a node: to start in the leaves asked
// (New), for a slice past its limit (Split), or for a slice the Query
// Group's next Slot is to be read split too (SplitKept).
type splitKind int

const (
	splitStarting splitKind = iota
	splitRead
	splitKept
)

// treeSplit is one split of a tree: the node split, the nodes made of it in
// reading order, and why.
type treeSplit struct {
	parent   int
	children []int
	kind     splitKind
}

// Shape is the leaves a Slot's tree was read in that its Query Group's next
// Slot starts in: the splits that made them, from the root, each with the
// segments of the node split and of its children, so the tree built from it
// is cut further as any tree is. It holds every starting split and every
// kept split (SplitKept), and a split for a slice past its limit only where
// a kept split was made under it: a slice split for its size is the round's,
// and is asked again from its bytes.
//
// A Shape is a value of this process alone, never persisted.
type Shape struct {
	dimensions []string
	splits     []shapeSplit
}

type shapeSplit struct {
	parent   []segment
	children [][]segment
	kind     splitKind
}

// IsZero reports whether the shape holds no kept split: a tree with none is
// started in the leaves asked, by New.
func (shape Shape) IsZero() bool {
	for _, split := range shape.splits {
		if split.kind == splitKept {
			return false
		}
	}
	return true
}

// Leaves is how many leaves the shape has.
func (shape Shape) Leaves() int {
	leaves := 1
	for _, split := range shape.splits {
		leaves += len(split.children) - 1
	}
	return leaves
}

// Dimensions is what the shape cuts on, in order.
func (shape Shape) Dimensions() []string { return slices.Clone(shape.dimensions) }

// Shape is the tree's leaves as its Query Group's next Slot starts in them,
// the zero Shape when the tree made no kept split.
func (tree *Tree) Shape() Shape {
	madeBy := map[int]int{}
	for index, split := range tree.splits {
		for _, child := range split.children {
			madeBy[child] = index
		}
	}
	// A split for a slice's size under a kept split is in the shape: the
	// kept split's node is one of its leaves.
	under := map[int]bool{}
	for _, split := range tree.splits {
		if split.kind != splitKept {
			continue
		}
		for node := split.parent; ; {
			index, made := madeBy[node]
			if !made {
				break
			}
			under[index] = true
			node = tree.splits[index].parent
		}
	}
	shape := Shape{dimensions: slices.Clone(tree.dimensions)}
	for index, split := range tree.splits {
		if split.kind == splitRead && !under[index] {
			continue
		}
		children := make([][]segment, 0, len(split.children))
		for _, child := range split.children {
			children = append(children, slices.Clone(tree.nodes[child].segments))
		}
		shape.splits = append(shape.splits, shapeSplit{
			parent: slices.Clone(tree.nodes[split.parent].segments), children: children, kind: split.kind,
		})
	}
	if shape.IsZero() {
		return Shape{}
	}
	return shape
}

// Reasons a ShapeRefusedError names, closed.
const (
	// ShapeRefusedDimensions is a shape cut on other dimensions than the
	// tree is asked for.
	ShapeRefusedDimensions = "dimensions"
	// ShapeRefusedNotALeaf is a split of a node the splits before it did not
	// leave a leaf: split twice, or never made.
	ShapeRefusedNotALeaf = "not_a_leaf"
	// ShapeRefusedNotAPartition is a split whose children do not partition
	// the node split: a value in none of them, or in two.
	ShapeRefusedNotAPartition = "not_a_partition"
	// ShapeRefusedMaxLeaves is a shape of more leaves than a tree may have.
	ShapeRefusedMaxLeaves = "max_leaves"
)

// ShapeRefusedReasons is every reason, in this order.
var ShapeRefusedReasons = [4]string{ShapeRefusedDimensions, ShapeRefusedNotALeaf, ShapeRefusedNotAPartition, ShapeRefusedMaxLeaves}

// ShapeRefusedError is a shape no tree is built from, and why.
type ShapeRefusedError struct {
	Reason string
}

func (err *ShapeRefusedError) Error() string {
	return fmt.Sprintf("alarmd inputslice: shape refused: %s", err.Reason)
}

// FromShape is the tree of shape's leaves, every one pending, cut on
// dimensions. It replays the shape's splits from the root and holds each to
// split a leaf into children that partition it - every value of the node in
// exactly one child, judged on the segments, the classes of each path, not on
// the conditions written - so the leaves are mutually exclusive and together
// complete, as the leaves of a tree split by Split are. A shape that does not
// hold is refused (ShapeRefusedError), and the caller starts its tree as New
// does.
func FromShape(dimensions []string, shape Shape) (*Tree, error) {
	if !slices.Equal(dimensions, shape.dimensions) {
		return nil, &ShapeRefusedError{Reason: ShapeRefusedDimensions}
	}
	if shape.Leaves() > MaxLeaves {
		return nil, &ShapeRefusedError{Reason: ShapeRefusedMaxLeaves}
	}
	// The root is the dimension the first split cut on from it, whichever it
	// was: a root constrains nothing, and only says what it is cut on.
	root := []segment{{dimension: 0}}
	if len(shape.splits) > 0 && len(shape.splits[0].parent) == 1 && !shape.splits[0].parent[0].writesCondition() {
		if root[0] = shape.splits[0].parent[0]; root[0].dimension < 0 || root[0].dimension >= len(dimensions) {
			return nil, &ShapeRefusedError{Reason: ShapeRefusedNotALeaf}
		}
	}
	tree := &Tree{dimensions: slices.Clone(dimensions), maxLeaves: MaxLeaves}
	tree.add(root, -1)
	for _, split := range shape.splits {
		var target *node
		for _, candidate := range tree.nodes {
			if candidate.state == statePending && slices.EqualFunc(candidate.segments, split.parent, segment.equal) {
				target = candidate
				break
			}
		}
		if target == nil {
			return nil, &ShapeRefusedError{Reason: ShapeRefusedNotALeaf}
		}
		if !partitions(target.segments, split.children, len(dimensions)) {
			return nil, &ShapeRefusedError{Reason: ShapeRefusedNotAPartition}
		}
		children := make([][]segment, 0, len(split.children))
		for _, child := range split.children {
			children = append(children, slices.Clone(child))
		}
		// The leaves are bounded above, and every split of a shape was made
		// by splitInto on these very segments, within ConditionBudget: what
		// it refuses here is the leaves the splits come to.
		if _, err := tree.splitInto(target, children, target.segments[len(target.segments)-1].dimension, split.kind); err != nil {
			return nil, &ShapeRefusedError{Reason: ShapeRefusedMaxLeaves}
		}
	}
	return tree, nil
}

// Merged is the shape with its last kept split taken back: that split's
// children - leaves of the shape, as nothing was split after it - are one
// leaf again, the node they were split from. A split for a slice's size
// that was in the shape only for that split goes with it. With no kept split
// left it is the zero Shape.
func (shape Shape) Merged() Shape {
	last := -1
	for index, split := range shape.splits {
		if split.kind == splitKept {
			last = index
		}
	}
	if last < 0 {
		return Shape{}
	}
	kept := Shape{dimensions: shape.dimensions}
	kept.splits = append(append(kept.splits, shape.splits[:last]...), shape.splits[last+1:]...)
	tree, err := FromShape(shape.dimensions, kept)
	if err != nil {
		return Shape{}
	}
	return tree.Shape()
}

// LastKept is the leaves the tree's last kept split made, by ID, nil when
// it made none: the leaves Merged would make one again.
func (tree *Tree) LastKept() []int {
	for index := len(tree.splits) - 1; index >= 0; index-- {
		if tree.splits[index].kind == splitKept {
			return slices.Clone(tree.splits[index].children)
		}
	}
	return nil
}

func (s segment) equal(other segment) bool {
	return s.dimension == other.dimension && s.exhausted == other.exhausted &&
		slices.EqualFunc(s.path, other.path, class.equal)
}

func (c class) equal(other class) bool {
	return c.negated == other.negated && slices.Equal(c.set, other.set)
}

// partitions reports whether children are a partition of parent: each the
// parent's segments with its last one cut, in one of the three ways a tree
// cuts a node (childSegments) -
//
//   - deeper: the last segment's path one class longer, the classes a
//     partition of every rune, and the child of values exactly as long;
//   - refined: one class of the last segment's path replaced by parts that
//     partition that class;
//   - on another dimension the parent is not cut on: a segment of that
//     dimension ANDed after the parent's, its first class a partition of
//     every rune, and the child of the empty value and the missing label.
func partitions(parent []segment, children [][]segment, dimensions int) bool {
	if len(parent) == 0 || len(children) < 2 {
		return false
	}
	last := parent[len(parent)-1]
	kept := parent[:len(parent)-1]
	if deeperOf(kept, last, children) || refinedOf(kept, last, children) {
		return true
	}
	// On another dimension, the parent's segments kept; a root that
	// constrains nothing is not carried.
	before := parent
	if !last.writesCondition() {
		before = kept
	}
	next := children[0][len(children[0])-1].dimension
	if next == last.dimension || next < 0 || next >= dimensions || cutOn(before, next) {
		return false
	}
	return deeperOf(before, segment{dimension: next}, children)
}

// deeperOf reports whether children are deeper(kept, parent, classes) for
// classes that partition every rune.
func deeperOf(kept []segment, parent segment, children [][]segment) bool {
	if parent.exhausted {
		return false
	}
	var classes []class
	exhausted := 0
	for _, child := range children {
		if len(child) != len(kept)+1 || !slices.EqualFunc(child[:len(kept)], kept, segment.equal) {
			return false
		}
		cut := child[len(kept)]
		switch {
		case cut.dimension != parent.dimension:
			return false
		case cut.exhausted:
			if !slices.EqualFunc(cut.path, parent.path, class.equal) {
				return false
			}
			exhausted++
		default:
			if len(cut.path) != len(parent.path)+1 || !slices.EqualFunc(cut.path[:len(parent.path)], parent.path, class.equal) {
				return false
			}
			classes = append(classes, cut.path[len(parent.path)])
		}
	}
	return exhausted == 1 && partitionOf(class{negated: true}, classes)
}

// refinedOf reports whether children are parent with one class of its path,
// the same in every child, replaced by parts that partition that class.
func refinedOf(kept []segment, parent segment, children [][]segment) bool {
	level := -1
	parts := make([]class, 0, len(children))
	for _, child := range children {
		if len(child) != len(kept)+1 || !slices.EqualFunc(child[:len(kept)], kept, segment.equal) {
			return false
		}
		cut := child[len(kept)]
		if cut.dimension != parent.dimension || cut.exhausted != parent.exhausted || len(cut.path) != len(parent.path) {
			return false
		}
		differs := -1
		for index := range cut.path {
			if !cut.path[index].equal(parent.path[index]) {
				if differs >= 0 {
					return false
				}
				differs = index
			}
		}
		if differs < 0 || level >= 0 && differs != level {
			return false
		}
		level = differs
		parts = append(parts, cut.path[differs])
	}
	return level >= 0 && partitionOf(parent.path[level], parts)
}

// partitionOf reports whether parts partition whole: pairwise disjoint, and
// together every rune of whole and none other. Two negated classes always
// share the runes neither lists, and explicit classes never cover a negated
// one, so a negated whole takes exactly one negated part.
func partitionOf(whole class, parts []class) bool {
	var explicit []rune
	negated := -1
	for index, part := range parts {
		if part.negated {
			if negated >= 0 {
				return false
			}
			negated = index
			continue
		}
		if len(part.set) == 0 {
			return false
		}
		explicit = append(explicit, part.set...)
	}
	slices.Sort(explicit)
	if len(slices.Compact(slices.Clone(explicit))) != len(explicit) {
		// An explicit rune in two parts.
		return false
	}
	if !whole.negated {
		return negated < 0 && slices.Equal(explicit, whole.set)
	}
	if negated < 0 {
		return false
	}
	// whole is every rune outside W, the negated part every rune outside U,
	// the explicit parts E. Disjoint: E inside U, and E outside W. Together
	// whole: U less E is W.
	excluded := parts[negated].set
	for _, r := range explicit {
		if _, inU := slices.BinarySearch(excluded, r); !inU {
			return false
		}
		if _, inW := slices.BinarySearch(whole.set, r); inW {
			return false
		}
	}
	var rest []rune
	for _, r := range excluded {
		if _, inE := slices.BinarySearch(explicit, r); !inE {
			rest = append(rest, r)
		}
	}
	return slices.Equal(rest, whole.set)
}
