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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
)

// shapePopulation is hosts whose values end in every character the fixed
// alphabet divides and some it does not, a missing label and an empty one
// among them.
func shapePopulation() []series {
	population := []series{{}, {"host": ""}}
	for index := 0; index < 400; index++ {
		population = append(population, series{"host": fmt.Sprintf("host-%d%c", index, "0a9z-_X"[index%7])})
	}
	return population
}

func leavesOf(tree *inputslice.Tree) []execution.InputSlice {
	var leaves []execution.InputSlice
	for _, leaf := range tree.Pending() {
		leaves = append(leaves, leaf.Slice)
	}
	return leaves
}

// A tree with a slice split for its time keeps that split in its Shape, and
// the tree built from the Shape has the same leaves, every one pending: every
// series in exactly one of them, read either way. A slice split for its size
// is not in the Shape unless a kept split was made under it.
func TestATreeBuiltFromItsShapeHasItsLeaves(t *testing.T) {
	dimensions := []string{"host"}
	tree, err := inputslice.New(dimensions, 4, dimensions[0])
	if err != nil {
		t.Fatal(err)
	}
	if !tree.Shape().IsZero() {
		t.Fatal("a tree with no kept split has a shape")
	}
	sample := inputslice.SampleOf(map[string][]string{"host": {"abcd", "efgh", "ijkl", "mnop", "qrst"}})
	pending := tree.Pending()
	// One slice split for its size, one for its time under another, and one
	// for its time under a slice split for its size.
	sizeOnly, err := tree.Split(pending[0].ID, sample)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.SplitKept(pending[1].ID, sample); err != nil {
		t.Fatal(err)
	}
	bySize, err := tree.Split(pending[2].ID, sample)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.SplitKept(bySize[0].ID, sample); err != nil {
		t.Fatal(err)
	}
	shape := tree.Shape()
	if shape.IsZero() {
		t.Fatal("a tree with kept splits has no shape")
	}
	built, err := inputslice.FromShape(dimensions, shape)
	if err != nil {
		t.Fatalf("from its own shape: %v", err)
	}
	assertPartition(t, leavesOf(built), shapePopulation())
	// The split for its size alone is not in it: its leaves are one.
	if got, want := built.Leaves(), tree.Leaves()-len(sizeOnly)+1; got != want || shape.Leaves() != want {
		t.Fatalf("built from its shape %d leaves (shape %d), want the tree's %d less the split for size alone", got, shape.Leaves(), want)
	}
	// The leaves under the kept splits are the tree's own.
	own := map[string]bool{}
	for _, leaf := range leavesOf(tree) {
		own[leaf.Digest()] = true
	}
	kept := 0
	for _, leaf := range leavesOf(built) {
		if own[leaf.Digest()] {
			kept++
		}
	}
	if kept != built.Leaves()-1 {
		t.Fatalf("%d of %d leaves built are the tree's, want every one but the slice split for size alone", kept, built.Leaves())
	}
}

// A tree is built from a shape only on the dimensions it was cut on.
func TestAShapeIsRefusedOnOtherDimensions(t *testing.T) {
	tree, err := inputslice.New([]string{"host"}, 4, "host")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.SplitKept(tree.Pending()[1].ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = inputslice.FromShape([]string{"host", "instance"}, tree.Shape())
	var refused *inputslice.ShapeRefusedError
	if !errors.As(err, &refused) || refused.Reason != inputslice.ShapeRefusedDimensions {
		t.Fatalf("err = %v, want refused for its dimensions", err)
	}
}

// Merging takes the last kept split back: its leaves are one again and the
// rest of the shape kept, a partition still; merging the last kept split
// back leaves the zero Shape, and a split for size that was there only for a
// kept split under it goes with it.
func TestAShapeMergedTakesItsLastKeptSplitBack(t *testing.T) {
	dimensions := []string{"host"}
	tree, err := inputslice.New(dimensions, 4, dimensions[0])
	if err != nil {
		t.Fatal(err)
	}
	sample := inputslice.SampleOf(map[string][]string{"host": {"abcd", "efgh", "ijkl", "mnop", "qrst"}})
	pending := tree.Pending()
	first, err := tree.SplitKept(pending[0].ID, sample)
	if err != nil {
		t.Fatal(err)
	}
	bySize, err := tree.Split(pending[1].ID, sample)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tree.SplitKept(bySize[0].ID, sample)
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.LastKept(); len(got) != len(second) || got[0] != second[0].ID {
		t.Fatalf("last kept %v, want the leaves of the second kept split", got)
	}
	shape := tree.Shape()
	merged := shape.Merged()
	if want := shape.Leaves() - (len(second) - 1) - (len(bySize) - 1); merged.Leaves() != want {
		t.Fatalf("merged %d leaves, want %d: the second kept split and the split for size it was under taken back", merged.Leaves(), want)
	}
	built, err := inputslice.FromShape(dimensions, merged)
	if err != nil {
		t.Fatalf("from the merged shape: %v", err)
	}
	assertPartition(t, leavesOf(built), shapePopulation())
	if got := built.LastKept(); len(got) != len(first) {
		t.Fatalf("last kept after the merge %v, want the first kept split's %d leaves", got, len(first))
	}
	if !merged.Merged().IsZero() {
		t.Fatal("merging every kept split back left a shape")
	}
}
