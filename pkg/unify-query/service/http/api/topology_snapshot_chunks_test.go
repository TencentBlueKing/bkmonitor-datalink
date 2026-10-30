package api

import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"
)

func changingTopologySnapshots(changing bool) []cmdb.SharedTopologySnapshot {
	frames := make([]cmdb.SharedTopologySnapshot, 130)
	for k := range frames {
		f := cmdb.SharedTopologySnapshot{Timestamp: int64(k), Partial: k%7 == 0}
		if f.Partial {
			f.PartialReason = "中文<&>\u2028"
		}
		for i := 0; i < 48; i++ {
			version := 0
			if changing {
				version = k
			}
			dimensions := cmdb.Matcher{"id": fmt.Sprint(i), "version": fmt.Sprint(version), "escaped": "<>&\u2028\u2029", "invalid": string([]byte{0xff})}
			if i == 0 {
				dimensions = nil
			}
			if i == 1 {
				dimensions = cmdb.Matcher{}
			}
			if i == 2 {
				dimensions["large"] = strings.Repeat("λ", 40000)
			}
			f.Nodes = append(f.Nodes, cmdb.SharedTopologyNode{ID: uint64(i), ResourceType: "entity", Dimensions: dimensions})
			f.Edges = append(f.Edges, cmdb.SharedTopologyEdge{Source: uint64(i), Target: ^uint64(0), RelationType: "calls", MetricName: "metric<&>", Category: "dynamic", Direction: "outbound"})
		}
		// Insertions, removals and reorderings must not reuse the wrong row or comma.
		if changing && k%3 == 0 {
			f.Nodes = f.Nodes[:47]
			f.Edges = f.Edges[1:]
		}
		if changing && k%5 == 0 {
			f.Nodes[0], f.Nodes[1] = f.Nodes[1], f.Nodes[0]
		}
		frames[k] = f
	}
	frames[64].Nodes = nil
	frames[64].Edges = nil
	frames[65].Nodes = []cmdb.SharedTopologyNode{}
	frames[65].Edges = []cmdb.SharedTopologyEdge{}
	return frames
}

func TestRepeatedTopologySnapshotChunksMatchStandardJSON(t *testing.T) {
	for _, changing := range []bool{false, true} {
		t.Run(fmt.Sprint(changing), func(t *testing.T) {
			item := cmdb.SharedTopologyResponseData{Code: 200, PointCount: 130, Step: "1s", Message: "<&>", Snapshots: changingTopologySnapshots(changing)}
			want, err := stdjson.Marshal(item)
			require.NoError(t, err)
			parts, size, err := marshalLargeTopologySnapshots(item)
			require.NoError(t, err)
			require.Equal(t, len(want), size)
			require.Equal(t, want, bytes.Join(parts, nil))
			if !changing {
				seen := make(map[*byte]bool)
				reused := false
				for _, part := range parts {
					if len(part) >= topologyChunkMinBytes {
						reused = reused || seen[&part[0]]
						seen[&part[0]] = true
					}
				}
				require.True(t, reused, "repeated frames must reuse owned encoded blocks")
			}
		})
	}
}

func TestTopologyChunkCacheOwnsBytesAndVerifiesCollisions(t *testing.T) {
	correct := []byte("same complete block")
	hash := xxhash.Sum64(correct)
	b := topologySnapshotChunkBuilder{work: make([]byte, 0, topologyChunkTargetBytes), byHash: map[uint64][][]byte{hash: {[]byte("different collision")}}}
	require.NoError(t, b.append(correct, 1))
	b.flush()
	require.Equal(t, correct, b.parts[0])
	require.Len(t, b.byHash[hash], 2)
	owned := b.parts[0]
	require.NoError(t, b.append([]byte("overwrite working memory"), 1))
	b.flush()
	require.Equal(t, correct, owned)
	require.NoError(t, b.append(correct, 1))
	b.flush()
	require.Equal(t, &owned[0], &b.parts[2][0], "exact blocks should share immutable owned bytes")
	require.Less(t, cap(owned), topologyChunkMinBytes, "small used blocks must not retain the whole working buffer")
	b.size = int(^uint(0) >> 1)
	require.ErrorContains(t, b.append([]byte("x"), 1), "overflows")
}

func BenchmarkRepeatedTopologySnapshotChunks(b *testing.B) {
	for _, changing := range []bool{false, true} {
		frames := changingTopologySnapshots(changing)
		// Benchmark stable versus changing rows separately from empty-frame tests.
		for _, index := range []int{64, 65} {
			frames[index].Nodes, frames[index].Edges = frames[index-1].Nodes, frames[index-1].Edges
		}
		for _, cached := range []bool{false, true} {
			b.Run(fmt.Sprintf("changing=%t/cached=%t", changing, cached), func(b *testing.B) {
				b.ReportAllocs()
				for n := 0; n < b.N; n++ {
					if cached {
						if _, err := marshalRepeatedTopologySnapshots(frames); err != nil {
							b.Fatal(err)
						}
					} else {
						parts := make([][]byte, len(frames))
						for k := range frames {
							var err error
							parts[k], err = stdjson.Marshal(frames[k])
							if err != nil {
								b.Fatal(err)
							}
						}
						if len(parts) == 0 {
							b.Fatal("missing frames")
						}
					}
				}
			})
		}
	}
}

func TestSnapshotChunkEncodingAdaptsToChangingWorkload(t *testing.T) {
	stable, changing := changingTopologySnapshots(false), changingTopologySnapshots(true)
	for _, kind := range []string{"stable-to-changing", "empty-to-changing", "changing-to-stable"} {
		frames := append([]cmdb.SharedTopologySnapshot(nil), stable...)
		switch kind {
		case "stable-to-changing":
			copy(frames[2:], changing[2:])
		case "empty-to-changing":
			copy(frames, changing)
			frames[0] = cmdb.SharedTopologySnapshot{}
			frames[1] = cmdb.SharedTopologySnapshot{Nodes: []cmdb.SharedTopologyNode{}, Edges: []cmdb.SharedTopologyEdge{}}
		case "changing-to-stable":
			copy(frames[:2], changing[:2])
		}
		t.Run(kind, func(t *testing.T) {
			want, err := stdjson.Marshal(frames)
			require.NoError(t, err)
			parts, err := marshalRepeatedTopologySnapshots(frames)
			require.NoError(t, err)
			actual := append([]byte{'['}, bytes.Join(parts, nil)...)
			actual = append(actual, ']')
			require.Equal(t, want, actual)
		})
	}
}
