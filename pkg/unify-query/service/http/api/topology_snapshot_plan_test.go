package api

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
	"github.com/gin-gonic/gin"
)

// Literal frame expansion is independent of the encoder's mask traversal.
func snapshotPlanWireFixture() (cmdb.SharedTopologyResponseData, *cmdb.SharedTopologySnapshotPlan) {
	p := &cmdb.SharedTopologySnapshotPlan{Timestamps: make([]int64, 130)}
	for i := range p.Timestamps {
		p.Timestamps[i] = 1700000000000 + int64(i)*1000
	}
	mask := func(frames ...int) cmdb.SharedTopologyTimeMask {
		m := cmdb.SharedTopologyTimeMask{Rest: make([]uint64, 2)}
		for _, i := range frames {
			if i < 64 {
				m.First |= uint64(1) << uint(i)
			} else {
				m.Rest[i/64-1] |= uint64(1) << uint(i%64)
			}
		}
		return m
	}
	ids := []uint64{2, 10, 9007199254740993, ^uint64(0)}
	dims := []cmdb.Matcher{nil, {}, {"name": "中文<&>\u2028\u2029", "bad": string([]byte{0xff, 'x'}), string([]byte{'k', 0xff}): "value"}, {"id": "max", "empty": ""}}
	for i, id := range ids {
		p.Nodes = append(p.Nodes, cmdb.SharedTopologySnapshotPlanNode{Node: cmdb.SharedTopologyNode{ID: id, ResourceType: "node", Dimensions: dims[i]}, Mask: mask(0, 63, 64, 127, 128, 129)})
		p.NodeOccurrences += 6
	}
	// Same ID has a disjoint attribute version. Node array remains numeric ID order.
	other := cmdb.SharedTopologySnapshotPlanNode{Node: cmdb.SharedTopologyNode{ID: 2, ResourceType: "node", Dimensions: cmdb.Matcher{"version": "alternate"}}, Mask: mask(1, 62, 65, 126)}
	p.Nodes = append(p.Nodes[:1], append([]cmdb.SharedTopologySnapshotPlanNode{other}, p.Nodes[1:]...)...)
	p.NodeOccurrences += 4
	for _, direction := range []string{"outbound", "inbound"} {
		for _, metric := range []string{"calls_flow", "other_flow"} {
			p.Edges = append(p.Edges, cmdb.SharedTopologySnapshotPlanEdge{Edge: cmdb.SharedTopologyEdge{Source: 2, Target: ^uint64(0), RelationType: "calls", MetricName: metric, Category: "dynamic", Direction: direction}, Mask: mask(0, 63, 64, 127, 128, 129)})
			p.EdgeOccurrences += 6
		}
	}
	p.Partial = []cmdb.SharedTopologySnapshotPlanPartial{{Index: 0, Reason: ""}, {Index: 64, Reason: "<partial>中文"}, {Index: 128, Reason: string([]byte{0xff})}}
	item := cmdb.SharedTopologyResponseData{Code: 200, StartTime: p.Timestamps[0], EndTime: p.Timestamps[129], Step: "1s", PointCount: 130, Message: "message <&>\u2028", Snapshots: make([]cmdb.SharedTopologySnapshot, 130)}
	for i, ts := range p.Timestamps {
		item.Snapshots[i] = cmdb.SharedTopologySnapshot{Timestamp: ts, Nodes: []cmdb.SharedTopologyNode{}, Edges: []cmdb.SharedTopologyEdge{}}
		active := func(m cmdb.SharedTopologyTimeMask) bool {
			w := m.First
			if i >= 64 {
				w = m.Rest[i/64-1]
			}
			return w&(uint64(1)<<uint(i%64)) != 0
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
	for _, v := range p.Partial {
		item.Snapshots[v.Index].Partial = true
		item.Snapshots[v.Index].PartialReason = v.Reason
	}
	return item, p
}

func TestSnapshotPlanWireMatchesStandardJSON(t *testing.T) {
	for _, empty := range []bool{false, true} {
		item, p := snapshotPlanWireFixture()
		if empty {
			p = &cmdb.SharedTopologySnapshotPlan{}
			item = cmdb.SharedTopologyResponseData{Code: 200, Snapshots: []cmdb.SharedTopologySnapshot{}}
		}
		before, _ := stdjson.Marshal(p)
		want, err := stdjson.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		for repeat := 0; repeat < 2; repeat++ {
			parts, size, err := marshalTopologySnapshotPlan(context.Background(), item, p)
			if err != nil {
				t.Fatal(err)
			}
			got := bytes.Join(parts, nil)
			if size != len(want) || !bytes.Equal(got, want) {
				t.Fatalf("wire mismatch empty=%t len %d/%d got=%s want=%s", empty, size, len(want), got, want)
			}
			after, _ := stdjson.Marshal(p)
			if !bytes.Equal(before, after) {
				t.Fatal("encoding mutated caller plan")
			}
		}
	}
}

type snapshotPlanCancelContext struct {
	context.Context
	cancel     context.CancelFunc
	at, checks int
}

func (c *snapshotPlanCancelContext) Err() error {
	c.checks++
	if c.at > 0 && c.checks == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestSnapshotPlanCancellationDiscardsEncodedParts(t *testing.T) {
	item, p := snapshotPlanWireFixture()
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := &snapshotPlanCancelContext{Context: parent, cancel: cancel}
	_, _, err := marshalTopologySnapshotPlan(count, item, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int{1, count.checks / 2, count.checks} {
		t.Run(fmt.Sprint(at), func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &snapshotPlanCancelContext{Context: parent, cancel: cancel, at: at}
			parts, size, err := marshalTopologySnapshotPlan(ctx, item, p)
			if !errors.Is(err, context.Canceled) || parts != nil || size != 0 {
				t.Fatalf("real cancel checkpoint=%d/%d retained parts=%d size=%d error=%v", at, count.checks, len(parts), size, err)
			}
		})
	}
}

type snapshotPlanShortWriter struct {
	header http.Header
	calls  int
}

func (w *snapshotPlanShortWriter) Header() http.Header         { return w.header }
func (w *snapshotPlanShortWriter) WriteHeader(int)             {}
func (w *snapshotPlanShortWriter) Write(p []byte) (int, error) { w.calls++; return len(p) / 2, nil }
func TestSnapshotPlanMixedResponseAndShortWrite(t *testing.T) {
	item, p := snapshotPlanWireFixture()
	parts, _, err := marshalTopologySnapshotPlan(context.Background(), item, p)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := stdjson.Marshal(item)
	compact := stdjson.RawMessage(`{"code":200,"response_format":"compact-v1","compact":null}`)
	failed := stdjson.RawMessage(`{"code":400,"start_time":0,"end_time":0,"step":"","point_count":0,"snapshots":[],"message":"bad"}`)
	response := &topologyResponse{TraceID: "0123456789abcdef0123456789abcdef", Data: []stdjson.RawMessage{nil, compact, failed}, chunks: [][][]byte{parts, nil, nil}}
	want, _ := stdjson.Marshal(struct {
		TraceID string               `json:"trace_id"`
		Data    []stdjson.RawMessage `json:"data"`
	}{response.TraceID, []stdjson.RawMessage{snap, compact, failed}})
	rec := httptest.NewRecorder()
	g, _ := gin.CreateTestContext(rec)
	g.Request = httptest.NewRequest("POST", "/", nil)
	proxy.TraceJSONEncoding(g)
	proxy.WriteJSON(g.Request.Context(), g, 200, response)
	if err := proxy.ResponseWriteError(g); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, rec.Body.Bytes()) {
		t.Fatal("mixed full response differs")
	}
	w := &snapshotPlanShortWriter{header: http.Header{}}
	g, _ = gin.CreateTestContext(w)
	g.Request = httptest.NewRequest("POST", "/", nil)
	proxy.TraceJSONEncoding(g)
	proxy.WriteJSON(g.Request.Context(), g, 200, response)
	if !errors.Is(proxy.ResponseWriteError(g), io.ErrShortWrite) || w.calls != 1 {
		t.Fatalf("short write classification/stop failed %v calls=%d", proxy.ResponseWriteError(g), w.calls)
	}
}

func TestSnapshotPlanNilAndOwnedEncodedBytes(t *testing.T) {
	item, plan := snapshotPlanWireFixture()
	parts, size, err := marshalTopologySnapshotPlan(context.Background(), item, nil)
	if err == nil || parts != nil || size != 0 {
		t.Fatal("nil plan must fail without retained bytes")
	}
	parts, _, err = marshalTopologySnapshotPlan(context.Background(), item, plan)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Join(parts, nil)
	plan.Nodes[1].Node.Dimensions["version"] = "changed"
	plan.Nodes[0].Mask.Rest[0] = 0
	plan.Timestamps[0] = 0
	_, _, err = marshalTopologySnapshotPlan(context.Background(), item, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, bytes.Join(parts, nil)) {
		t.Fatal("encoded output aliases the plan or subsequent encoding")
	}
}
