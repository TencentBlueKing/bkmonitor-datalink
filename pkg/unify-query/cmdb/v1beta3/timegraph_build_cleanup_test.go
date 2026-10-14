package v1beta3

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// Keep only scalar observations, never contexts or spans. Cancellation after a
// completed apply exercises a partially populated graph, not just an empty ctor.
type buildCleanupObserver struct {
	mu               sync.Mutex
	started, ended   map[string]int
	attributes       map[string]map[string]any
	status           map[string]codes.Code
	applied          int
	cancelAfterApply context.CancelFunc
}

func (o *buildCleanupObserver) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started[span.Name()]++
}

func (o *buildCleanupObserver) OnEnd(span sdktrace.ReadOnlySpan) {
	o.mu.Lock()
	o.ended[span.Name()]++
	o.status[span.Name()] = span.Status().Code
	attrs := make(map[string]any)
	for _, a := range span.Attributes() {
		attrs[string(a.Key)] = a.Value.AsInterface()
	}
	o.attributes[span.Name()] = attrs
	var cancel context.CancelFunc
	if span.Name() == "timegraph-apply-relation-matrix" && span.Status().Code != codes.Error {
		o.applied++
		cancel = o.cancelAfterApply
		o.cancelAfterApply = nil
	}
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (*buildCleanupObserver) Shutdown(context.Context) error   { return nil }
func (*buildCleanupObserver) ForceFlush(context.Context) error { return nil }
func newBuildCleanupObserver(t *testing.T) *buildCleanupObserver {
	t.Helper()
	o := &buildCleanupObserver{started: make(map[string]int), ended: make(map[string]int), attributes: make(map[string]map[string]any), status: make(map[string]codes.Code)}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(o))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })
	return o
}

func TestBuildTimeGraphCleansFailedStorage(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, mode := range []string{"backend-first", "backend-after-first", "cancel-backend", "cancel-after-first", "ordinary-graph-budget", "success"} {
			storage := "legacy"
			if shared {
				storage = "shared"
			}
			t.Run(storage+"/"+mode, func(t *testing.T) {
				oldYolo, oldEdges := yoloMode, MaxGraphEdges
				t.Cleanup(func() { yoloMode, MaxGraphEdges = oldYolo, oldEdges })
				yoloMode = mode != "ordinary-graph-budget"
				if !yoloMode {
					MaxGraphEdges = 1
				}
				observer := newBuildCleanupObserver(t)
				parent, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
				defer cancel()
				if mode == "cancel-after-first" {
					observer.cancelAfterApply = cancel
				}
				sentinel := errors.New("build cleanup backend failure")
				calls := 0
				const stamp int64 = 1700000000000
				model := sharedTopologyQueryModel(map[string]pl.Matrix{"source_middle_flow": contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, stamp), "middle_target_flow": contractMatrix(map[string]string{"middle_id": "b", "target_id": "c"}, stamp)})
				backend := model.timeGraphVMQuery
				model.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
					calls++
					if mode == "backend-first" || (mode == "backend-after-first" && calls == 2) {
						return nil, sentinel
					}
					if mode == "cancel-backend" {
						cancel()
						return nil, parent.Err()
					}
					return backend(ctx, q, expr, instant, start, end, step)
				}
				relations := []cmdb.Relation{{V: []cmdb.Resource{"source", "middle"}, RelationType: "source_to_middle", MetricName: "source_middle_flow", Category: "static", Direction: "outbound"}, {V: []cmdb.Resource{"middle", "target"}, RelationType: "middle_to_target", MetricName: "middle_target_flow", Category: "static", Direction: "outbound"}}
				var grid *TopologyGrid
				if shared {
					g, err := NewTopologyGrid([]int64{stamp})
					require.NoError(t, err)
					grid = &g
				}
				start := time.UnixMilli(stamp)
				graph, err := model.buildTimeGraph(parent, "space", start, start, time.Second, "source", nil, nil, nil, relations, "1m", nil, grid)
				require.Equal(t, 1, observer.started["timegraph-create-storage"])
				require.Equal(t, 1, observer.ended["timegraph-create-storage"])
				require.NotEqual(t, codes.Error, observer.status["timegraph-create-storage"], "storage construction must succeed")
				require.Equal(t, shared, observer.attributes["timegraph-create-storage"]["shared"])
				if mode == "success" {
					require.NoError(t, err)
					require.NotNil(t, graph)
					// Ownership transfers to the caller only on success. A builder defer must
					// not clear the graph early or clean it a second time.
					require.Zero(t, observer.started["timegraph-clean"])
					require.Zero(t, observer.ended["timegraph-clean"])
					require.Equal(t, 3, graph.nodeBuilder.Length())
					require.Equal(t, 2, graph.edgeCount)
					graph.Clean(parent)
					require.Zero(t, graph.nodeBuilder.Length())
					require.Zero(t, graph.edgeCount)
					require.Zero(t, graph.nodeInfoCount)
					require.Empty(t, graph.timeGraph)
					require.Empty(t, graph.nodeInfos)
					require.Empty(t, graph.topologyNodes)
					require.Empty(t, graph.topologyEdges)
					if shared {
						require.Empty(t, graph.shared.nodeBits)
						require.Empty(t, graph.shared.nodeInfos)
						require.Empty(t, graph.shared.edgeBits)
						require.Empty(t, graph.shared.compactEdges)
						require.Empty(t, graph.shared.wordEdges)
						require.Empty(t, graph.shared.relationIDs)
						require.Empty(t, graph.shared.relations)
					}
				} else {
					require.Nil(t, graph)
					switch mode {
					case "backend-first", "backend-after-first":
						require.ErrorIs(t, err, sentinel)
					case "cancel-backend", "cancel-after-first":
						require.ErrorIs(t, err, context.Canceled)
						require.ErrorIs(t, parent.Err(), context.Canceled)
					case "ordinary-graph-budget":
						var limit *ResultLimitError
						require.ErrorAs(t, err, &limit)
						require.Equal(t, "max_graph_edges", limit.Reason)
					}
					require.Equal(t, codes.Error, observer.status["build-time-graph-from-relations"])
				}
				t.Logf("created=%d clean_started=%d clean_ended=%d completed_applies=%d backend_calls=%d", observer.ended["timegraph-create-storage"], observer.started["timegraph-clean"], observer.ended["timegraph-clean"], observer.applied, calls)
				if mode == "backend-after-first" || mode == "cancel-after-first" || mode == "ordinary-graph-budget" {
					require.Equal(t, 1, observer.applied, "must fail after one matrix was actually applied")
					// Build diagnostics must be captured before cleanup clears the graph.
					require.GreaterOrEqual(t, observer.attributes["build-time-graph-from-relations"]["graph-node-count"].(int64), int64(2))
					require.Equal(t, int64(1), observer.attributes["build-time-graph-from-relations"]["graph-edge-count"])
				}
				// All error cases need explicit cleanup as well as success. GC reachability
				// is not the contract here: Clean recycles matchers and emits cleanup spans.
				require.Equal(t, 1, observer.started["timegraph-clean"])
				require.Equal(t, 1, observer.ended["timegraph-clean"])
				require.NotEqual(t, codes.Error, observer.status["timegraph-clean"], "cleanup must run even with a canceled parent")
				require.Equal(t, observer.started, observer.ended, "all opened stages must finish")
			})
		}
	}
}

func TestBuildTimeGraphConstructorFailureDoesNotClean(t *testing.T) {
	observer := newBuildCleanupObserver(t)
	ctx := initTimeGraphQueryTestEnvironment()
	start := time.Unix(1700000000, 0)
	// The invalid grid is rejected before any shared storage is constructed.
	invalid := TopologyGrid{Timestamps: []int64{start.UnixMilli(), start.UnixMilli()}}
	graph, err := sharedTopologyQueryModel(nil).buildTimeGraph(ctx, "space", start, start, time.Second, "source", nil, nil, nil, nil, "1m", nil, &invalid)
	require.Error(t, err)
	require.Nil(t, graph)
	require.Equal(t, 1, observer.started["timegraph-create-storage"])
	require.Equal(t, 1, observer.ended["timegraph-create-storage"])
	require.Equal(t, codes.Error, observer.status["timegraph-create-storage"])
	require.Zero(t, observer.started["timegraph-clean"])
	require.Zero(t, observer.ended["timegraph-clean"])
	require.Equal(t, observer.started, observer.ended)
}
