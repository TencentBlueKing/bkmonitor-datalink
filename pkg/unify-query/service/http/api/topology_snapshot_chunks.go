package api

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"fmt"
	"maps"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/cespare/xxhash/v2"
)

const (
	topologyChunkMinBytes    = 8 * 1024
	topologyChunkTargetBytes = 64 * 1024
)

type topologySnapshotChunkBuilder struct {
	work     []byte
	parts    [][]byte
	byHash   map[uint64][][]byte
	hitBytes int
	size     int
}

func (b *topologySnapshotChunkBuilder) append(part []byte, boundary uint64) error {
	if len(part) > int(^uint(0)>>1)-b.size {
		return fmt.Errorf("topology response size overflows int")
	}
	b.size += len(part)
	b.work = append(b.work, part...)
	// Stable row boundaries let unchanged sections align after inserted/removed rows.
	// These sizes switch encoding fragments; they never limit the answer.
	if len(b.work) >= topologyChunkTargetBytes || (len(b.work) >= topologyChunkMinBytes && boundary&127 == 0) {
		b.flush()
	}
	return nil
}

func (b *topologySnapshotChunkBuilder) flush() {
	if len(b.work) == 0 {
		return
	}
	hash := xxhash.Sum64(b.work)
	for _, owned := range b.byHash[hash] {
		if bytes.Equal(owned, b.work) {
			b.hitBytes += len(owned)
			b.parts = append(b.parts, owned)
			b.work = b.work[:0]
			return
		}
	}
	// Copy the used bytes: retaining a full working-buffer capacity per small block
	// would increase memory for changing snapshots. Cached blocks are immutable.
	owned := append([]byte(nil), b.work...)
	b.byHash[hash] = append(b.byHash[hash], owned)
	b.parts = append(b.parts, owned)
	b.work = b.work[:0]
}

type topologyCachedNode struct {
	node     cmdb.SharedTopologyNode
	encoded  []byte
	boundary uint64
}

type topologyCachedEdge struct {
	encoded  []byte
	boundary uint64
}

// marshalRepeatedTopologySnapshots validates/encodes all values before writing.
// Each request owns its dictionaries. Public snapshot maps remain independent.
func marshalRepeatedTopologySnapshotsContext(ctx context.Context, snapshots []cmdb.SharedTopologySnapshot) ([][]byte, error) {
	return marshalTopologySnapshotFrames(ctx, snapshots, false)
}

func marshalTopologySnapshotFrames(ctx context.Context, snapshots []cmdb.SharedTopologySnapshot, consume bool) ([][]byte, error) {
	b := topologySnapshotChunkBuilder{work: make([]byte, 0, topologyChunkTargetBytes), byHash: make(map[uint64][][]byte)}
	nodes := make(map[uint64]topologyCachedNode)
	edges := make(map[cmdb.SharedTopologyEdge]topologyCachedEdge)
	raw := func(text string) error { return b.append([]byte(text), 1) }
	fallback := false
	for frame, snapshot := range snapshots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fallback {
			encoded, err := stdjson.Marshal(snapshot)
			if err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if frame+1 < len(snapshots) {
				encoded = append(encoded, ',')
			}
			if len(encoded) > int(^uint(0)>>1)-b.size {
				return nil, fmt.Errorf("topology response size overflows int")
			}
			b.size += len(encoded)
			b.parts = append(b.parts, encoded)
			if consume {
				snapshots[frame].Nodes, snapshots[frame].Edges = nil, nil
			}
			continue
		}
		frameStart, hitsStart := b.size, b.hitBytes
		header, err := stdjson.Marshal(struct {
			Timestamp int64 `json:"timestamp"`
		}{snapshot.Timestamp})
		if err != nil {
			return nil, err
		}
		if err = b.append(header[:len(header)-1], 1); err != nil {
			return nil, err
		}
		if err = raw(`,"nodes":`); err != nil {
			return nil, err
		}
		if snapshot.Nodes == nil {
			if err = raw("null"); err != nil {
				return nil, err
			}
		} else {
			if err = raw("["); err != nil {
				return nil, err
			}
			// Isolate the changing timestamp from otherwise stable node blocks.
			b.flush()
			for index, node := range snapshot.Nodes {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				cached, exists := nodes[node.ID]
				if !exists || cached.node.ResourceType != node.ResourceType || (cached.node.Dimensions == nil) != (node.Dimensions == nil) || !maps.Equal(cached.node.Dimensions, node.Dimensions) {
					encoded, e := stdjson.Marshal(node)
					if e != nil {
						return nil, e
					}
					encoded = append(encoded, ',')
					cached = topologyCachedNode{node: node, encoded: encoded, boundary: xxhash.Sum64(encoded)}
					nodes[node.ID] = cached
				}
				part := cached.encoded
				if index+1 == len(snapshot.Nodes) {
					part = part[:len(part)-1]
				}
				if err = b.append(part, cached.boundary); err != nil {
					return nil, err
				}
			}
			if err = raw("]"); err != nil {
				return nil, err
			}
		}
		b.flush()
		if err = raw(`,"edges":`); err != nil {
			return nil, err
		}
		if snapshot.Edges == nil {
			if err = raw("null"); err != nil {
				return nil, err
			}
		} else {
			if err = raw("["); err != nil {
				return nil, err
			}
			for index, edge := range snapshot.Edges {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				cached, exists := edges[edge]
				if !exists {
					encoded, e := stdjson.Marshal(edge)
					if e != nil {
						return nil, e
					}
					encoded = append(encoded, ',')
					cached = topologyCachedEdge{encoded: encoded, boundary: xxhash.Sum64(encoded)}
					edges[edge] = cached
				}
				part := cached.encoded
				if index+1 == len(snapshot.Edges) {
					part = part[:len(part)-1]
				}
				if err = b.append(part, cached.boundary); err != nil {
					return nil, err
				}
			}
			if err = raw("]"); err != nil {
				return nil, err
			}
		}
		trailer, err := stdjson.Marshal(struct {
			Partial bool   `json:"partial"`
			Reason  string `json:"partial_reason,omitempty"`
		}{snapshot.Partial, snapshot.PartialReason})
		if err != nil {
			return nil, err
		}
		if err = raw(","); err != nil {
			return nil, err
		}
		if err = b.append(trailer[1:], 1); err != nil {
			return nil, err
		}
		if frame+1 < len(snapshots) {
			if err = raw(","); err != nil {
				return nil, err
			}
		}
		b.flush()
		if consume {
			snapshots[frame].Nodes, snapshots[frame].Edges = nil, nil
		}
		// Low reuse can make per-row encoding more expensive than the original
		// per-frame encoder. Check every frame so stable-to-changing input adapts.
		if frame > 0 && b.hitBytes-hitsStart < (b.size-frameStart)/8 {
			fallback = true
			b.byHash, b.work, nodes, edges = nil, nil, nil, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.parts, nil
}
