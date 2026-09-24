// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// A view survives the wire: chunked to the bound, every chunk under it,
// reassembled to the same view, digest and all, from chunks in any order;
// a missing, duplicated or foreign chunk, or a body that does not hash to
// the version, is refused whole. An empty view is one chunk.
func TestASnapshotSurvivesTheWireInChunksOrNotAtAll(t *testing.T) {
	assign := map[string]string{}
	published := map[string]viewstream.Content{}
	for i := 0; i < 400; i++ {
		name := "qg-" + strconv.Itoa(i)
		assign[name] = "w1"
		published[name] = content("obj-"+name, "s1-"+name, "s2-"+name)
	}
	desired := desiredAt(publicationA, assign, published)
	view, err := desired.Project("w1")
	if err != nil {
		t.Fatal(err)
	}
	view.Version.Revision = 5
	const bound = 16 << 10
	chunks := viewstream.SnapshotChunks(view, bound)
	if len(chunks) < 3 {
		t.Fatalf("400 entries chunked at %d bytes gave %d chunks, want several", bound, len(chunks))
	}
	total := 0
	for index, chunk := range chunks {
		if size := proto.Size(chunk); size > bound {
			t.Fatalf("chunk %d is %d bytes, over the %d bound", index, size, bound)
		}
		if chunk.Chunk != uint32(index+1) || chunk.Chunks != uint32(len(chunks)) || chunk.Version.Digest != view.Version.Digest {
			t.Fatalf("chunk %d envelope = %d/%d %s", index, chunk.Chunk, chunk.Chunks, chunk.Version.Digest)
		}
		total += len(chunk.Entries)
	}
	if total != 400 {
		t.Fatalf("chunks carry %d entries, want 400", total)
	}
	// Serialized and parsed, in reverse order, back to the same view.
	shuffled := make([]*pb.Snapshot, 0, len(chunks))
	for index := len(chunks) - 1; index >= 0; index-- {
		payload, err := proto.Marshal(chunks[index])
		if err != nil {
			t.Fatal(err)
		}
		parsed := &pb.Snapshot{}
		if err := proto.Unmarshal(payload, parsed); err != nil {
			t.Fatal(err)
		}
		shuffled = append(shuffled, parsed)
	}
	assembled, err := viewstream.AssembleSnapshot("w1", shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(assembled, view) {
		t.Fatalf("assembled view differs from the sent one:\n got %+v\nwant %+v", assembled.Version, view.Version)
	}
	// Short one chunk, one twice, a chunk of another version, a tampered
	// body: each refused.
	if _, err := viewstream.AssembleSnapshot("w1", chunks[1:]); err == nil {
		t.Fatal("a snapshot short one chunk must be refused")
	}
	doubled := append(append([]*pb.Snapshot(nil), chunks[:len(chunks)-1]...), chunks[0])
	if _, err := viewstream.AssembleSnapshot("w1", doubled); err == nil {
		t.Fatal("a snapshot with a chunk twice must be refused")
	}
	foreign := proto.Clone(chunks[0]).(*pb.Snapshot)
	foreign.Version.Revision = 6
	if _, err := viewstream.AssembleSnapshot("w1", append([]*pb.Snapshot{foreign}, chunks[1:]...)); err == nil {
		t.Fatal("chunks of two versions must be refused")
	}
	tampered := proto.Clone(chunks[0]).(*pb.Snapshot)
	tampered.Entries[0].Content.ObjectDigest = "obj-forged"
	if _, err := viewstream.AssembleSnapshot("w1", append([]*pb.Snapshot{tampered}, chunks[1:]...)); !errors.Is(err, viewstream.ErrDigestMismatch) {
		t.Fatalf("a tampered body = %v, want ErrDigestMismatch", err)
	}
	// An empty view is one chunk and comes back valid.
	empty, _ := desiredAt(publicationA, nil, nil).Project("w1")
	empty.Version.Revision = 5
	single := viewstream.SnapshotChunks(empty, 0)
	if len(single) != 1 || single[0].Chunks != 1 {
		t.Fatalf("empty view chunks = %d", len(single))
	}
	if back, err := viewstream.AssembleSnapshot("w1", single); err != nil || !reflect.DeepEqual(back, empty) {
		t.Fatalf("empty view back = %+v (%v)", back, err)
	}
	// A revision the publisher never assigned is refused at the wire.
	unpublished := viewstream.SnapshotChunks(viewFrom(desired, "w1", 0), 0)
	if _, err := viewstream.AssembleSnapshot("w1", unpublished); err == nil {
		t.Fatal("a snapshot at revision 0 must be refused")
	}
}

func viewFrom(desired viewstream.Desired, worker string, revision uint64) viewstream.View {
	view, _ := desired.Project(worker)
	view.Version.Revision = revision
	return view
}

// A delta and a receipt survive the wire; a delta whose envelope is not one
// step is refused at the wire, before any view is consulted.
func TestADeltaAndAReceiptSurviveTheWire(t *testing.T) {
	first := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	second := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-3": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1b", "s1"), "qg-3": content("obj-3", "s3")})
	before, after := viewFrom(first, "w1", 1), viewFrom(second, "w1", 2)
	delta, err := viewstream.Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := proto.Marshal(viewstream.DeltaToWire(delta))
	if err != nil {
		t.Fatal(err)
	}
	parsed := &pb.Delta{}
	if err := proto.Unmarshal(payload, parsed); err != nil {
		t.Fatal(err)
	}
	back, err := viewstream.DeltaFromWire("w1", parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, delta) {
		t.Fatalf("delta back = %+v\nwant %+v", back, delta)
	}
	if applied, err := viewstream.Apply(before, back); err != nil || !reflect.DeepEqual(applied, after) {
		t.Fatalf("applying the decoded delta = %+v (%v)", applied, err)
	}
	skipped := proto.Clone(parsed).(*pb.Delta)
	skipped.Target.Revision = 3
	if _, err := viewstream.DeltaFromWire("w1", skipped); err == nil {
		t.Fatal("a two-step delta must be refused at the wire")
	}
	receipt := viewstream.Receipt{Receiver: viewstream.Receiver{WorkerID: "w1", Incarnation: "i1"}, Version: after.Version,
		Acked: true, Installed: true, Failure: "", ObjectsMissing: 3, ObjectsProbed: true}
	payload, err = proto.Marshal(viewstream.ReceiptToWire(receipt))
	if err != nil {
		t.Fatal(err)
	}
	wire := &pb.Receipt{}
	if err := proto.Unmarshal(payload, wire); err != nil {
		t.Fatal(err)
	}
	if got, err := viewstream.ReceiptFromWire("w1", wire); err != nil || got != receipt {
		t.Fatalf("receipt back = %+v (%v), want %+v", got, err, receipt)
	}
}

// The record's timeline revision rides on the assignment across the wire and
// into the view's digest: two views that differ only in it are two versions,
// which is what lets the Worker learn a timeline moved from a delta rather
// than from probing. The receipt's switched count and the heartbeat's costs
// come back field for field, and a cost that names no Query Group is not a
// reading.
func TestTheTimelineRevisionTheSwitchedCountAndTheCostsSurviveTheWire(t *testing.T) {
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	plain, err := desired.Project("w1")
	if err != nil {
		t.Fatal(err)
	}
	assignment := desired.Assignments["qg-1"]
	assignment.TimelineRecordRevision = 41
	desired.Assignments["qg-1"] = assignment
	revised, err := desired.Project("w1")
	if err != nil {
		t.Fatal(err)
	}
	if revised.Version.Digest == plain.Version.Digest {
		t.Fatal("a view whose only change is the timeline revision has the same digest, so the Worker would never be told")
	}
	revised.Version.Revision = 1
	chunks := viewstream.SnapshotChunks(revised, 0)
	payload, err := proto.Marshal(chunks[0])
	if err != nil {
		t.Fatal(err)
	}
	wire := &pb.Snapshot{}
	if err := proto.Unmarshal(payload, wire); err != nil {
		t.Fatal(err)
	}
	back, err := viewstream.AssembleSnapshot("w1", []*pb.Snapshot{wire})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 1 || back.Entries[0].Assignment.TimelineRecordRevision != 41 {
		t.Fatalf("entries back = %+v, want the timeline revision 41 on the assignment", back.Entries)
	}

	receipt := viewstream.Receipt{Receiver: viewstream.Receiver{WorkerID: "w1", Incarnation: "i1"}, Version: revised.Version,
		Acked: true, Installed: true, Switched: false, ObjectsProbed: true, SwitchedQueryGroups: 597}
	receiptPayload, err := proto.Marshal(viewstream.ReceiptToWire(receipt))
	if err != nil {
		t.Fatal(err)
	}
	receiptWire := &pb.Receipt{}
	if err := proto.Unmarshal(receiptPayload, receiptWire); err != nil {
		t.Fatal(err)
	}
	if got, err := viewstream.ReceiptFromWire("w1", receiptWire); err != nil || got != receipt {
		t.Fatalf("receipt back = %+v (%v), want %+v", got, err, receipt)
	}

	costs := []viewstream.QueryGroupCost{{QueryGroup: "qg-1", RetainedBytesPeak: 404 << 20, CostPerSecondMilli: 1330}}
	wireCosts := viewstream.CostsToWire(costs)
	wireCosts = append(wireCosts, &pb.QueryGroupCost{RetainedBytesPeak: 7})
	if got := viewstream.CostsFromWire(wireCosts); !reflect.DeepEqual(got, costs) {
		t.Fatalf("costs back = %+v, want %+v with the unnamed one dropped", got, costs)
	}
	if viewstream.CostsToWire(nil) != nil || viewstream.CostsFromWire(nil) != nil {
		t.Fatal("no costs is nil both ways, so a heartbeat without them carries nothing")
	}
}
