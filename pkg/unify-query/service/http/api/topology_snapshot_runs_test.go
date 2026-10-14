package api

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func expandedSnapshotRunsFixture(repeat int) (cmdb.SharedTopologyResponseData, *cmdb.SharedTopologySnapshotPlan) {
	item, p := snapshotPlanWireFixture()
	nodes, edges := p.Nodes, p.Edges
	p.Nodes = nil
	p.Edges = nil
	for _, n := range nodes {
		for j := 0; j < repeat; j++ {
			p.Nodes = append(p.Nodes, n)
		}
	}
	for _, e := range edges {
		for j := 0; j < repeat; j++ {
			p.Edges = append(p.Edges, e)
		}
	}
	for i := range item.Snapshots {
		item.Snapshots[i].Nodes = []cmdb.SharedTopologyNode{}
		item.Snapshots[i].Edges = []cmdb.SharedTopologyEdge{}
		active := func(mask cmdb.SharedTopologyTimeMask) bool {
			word := mask.First
			if i >= 64 {
				word = mask.Rest[i/64-1]
			}
			return word&(uint64(1)<<uint(i%64)) != 0
		}
		for _, n := range p.Nodes {
			if active(n.Mask) {
				item.Snapshots[i].Nodes = append(item.Snapshots[i].Nodes, n.Node)
			}
		}
		for _, e := range p.Edges {
			if active(e.Mask) {
				item.Snapshots[i].Edges = append(item.Snapshots[i].Edges, e.Edge)
			}
		}
	}
	return item, p
}

func TestSnapshotMaskRunsKeepExactWireAndOwnedInput(t *testing.T) {
	for _, repeat := range []int{1, 16, 64} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			item, p := expandedSnapshotRunsFixture(repeat)
			before, _ := stdjson.Marshal(p)
			want, err := stdjson.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			for _, encode := range []func(context.Context, cmdb.SharedTopologyResponseData, *cmdb.SharedTopologySnapshotPlan) ([][]byte, int, error){marshalTopologySnapshotPlan, marshalTopologySnapshotRuns, marshalTopologySnapshotPlanIndexed} {
				parts, size, err := encode(context.Background(), item, p)
				if err != nil {
					t.Fatal(err)
				}
				if size != len(want) || !bytes.Equal(bytes.Join(parts, nil), want) {
					t.Fatal("run encoding changed exact JSON bytes")
				}
				after, _ := stdjson.Marshal(p)
				if !bytes.Equal(before, after) {
					t.Fatal("run encoding mutated owned plan")
				}
			}
		})
	}
}

func TestSnapshotMaskRunCancellationDropsEveryPart(t *testing.T) {
	item, p := expandedSnapshotRunsFixture(64)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := &snapshotPlanCancelContext{Context: parent, cancel: cancel}
	if _, _, err := marshalTopologySnapshotPlan(count, item, p); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int{1, count.checks / 2, count.checks} {
		parent, cancel := context.WithCancel(context.Background())
		ctx := &snapshotPlanCancelContext{Context: parent, cancel: cancel, at: at}
		parts, size, err := marshalTopologySnapshotPlan(ctx, item, p)
		cancel()
		if !errors.Is(err, context.Canceled) || parts != nil || size != 0 {
			t.Fatalf("cancel at%d retained parts=%d bytes=%d err=%v", at, len(parts), size, err)
		}
	}
}

func TestSnapshotRunEdgeSuffixKeepsEscapesEmptyAndInvalidUTF8(t *testing.T) {
	for _, label := range []string{"", "中文<&>\u2028\u2029", "quote\"\\\n", string([]byte{0xff, 'x'})} {
		item, p := expandedSnapshotRunsFixture(64)
		for i := range p.Edges {
			p.Edges[i].Edge.RelationType = label
			p.Edges[i].Edge.MetricName = label
			p.Edges[i].Edge.Category = label
			p.Edges[i].Edge.Direction = label
		}
		for i := range item.Snapshots {
			for j := range item.Snapshots[i].Edges {
				item.Snapshots[i].Edges[j].RelationType = label
				item.Snapshots[i].Edges[j].MetricName = label
				item.Snapshots[i].Edges[j].Category = label
				item.Snapshots[i].Edges[j].Direction = label
			}
		}
		want, err := stdjson.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		parts, size, err := marshalTopologySnapshotRuns(context.Background(), item, p)
		if err != nil {
			t.Fatal(err)
		}
		if size != len(want) || !bytes.Equal(bytes.Join(parts, nil), want) {
			t.Fatal("suffix cache changed exact edge wire")
		}
	}
}
