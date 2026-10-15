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
	ctx      context.Context
	meter    *topologyEncodeMeter
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
	if err := b.meter.wirePart(len(part)); err != nil {
		return err
	}
	need := len(b.work) + len(part)
	if need > cap(b.work) {
		next := max(topologyChunkTargetBytes, cap(b.work)*2)
		for next < need {
			next *= 2
		}
		if b.meter != nil {
			b.meter.working = int64(next + cap(b.work))
			if err := b.meter.reserve(); err != nil {
				return err
			}
		}
		owned := make([]byte, len(b.work), next)
		copy(owned, b.work)
		b.work = owned
		if b.meter != nil {
			b.meter.working = int64(cap(b.work))
		}
	}
	b.size += len(part)
	b.work = append(b.work, part...)
	// Stable row boundaries let unchanged sections align after inserted/removed rows.
	// These sizes switch encoding fragments; they never limit the answer.
	if len(b.work) >= topologyChunkTargetBytes || (len(b.work) >= topologyChunkMinBytes && boundary&127 == 0) {
		return b.flush()
	}
	return nil
}

func (b *topologySnapshotChunkBuilder) flush() error {
	if len(b.work) == 0 {
		return nil
	}
	if b.meter != nil {
		b.meter.container += 3 * 24
		if err := b.meter.reserve(); err != nil {
			return err
		}
	}
	hash := xxhash.Sum64(b.work)
	for _, owned := range b.byHash[hash] {
		if bytes.Equal(owned, b.work) {
			b.hitBytes += len(owned)
			b.parts = append(b.parts, owned)
			b.work = b.work[:0]
			return nil
		}
	}
	// Copy the used bytes: retaining a full working-buffer capacity per small block
	// would increase memory for changing snapshots. Cached blocks are immutable.
	if b.meter != nil {
		b.meter.container += 128
		if err := b.meter.retain(int64(len(b.work))); err != nil {
			return err
		}
	}
	owned := make([]byte, len(b.work))
	copy(owned, b.work)
	b.byHash[hash] = append(b.byHash[hash], owned)
	b.parts = append(b.parts, owned)
	b.work = b.work[:0]
	return nil
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
	b := topologySnapshotChunkBuilder{ctx: ctx, meter: topologyMeter(ctx), byHash: make(map[uint64][][]byte)}
	nodes := make(map[uint64]topologyCachedNode)
	edges := make(map[cmdb.SharedTopologyEdge]topologyCachedEdge)
	raw := func(text string) error { return b.append([]byte(text), 1) }
	fallback := false
	for frame, snapshot := range snapshots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fallback && b.meter == nil {
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
		header, err := b.meter.marshal(topologySnapshotHeader{snapshot.Timestamp})
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
			if err := b.flush(); err != nil {
				return nil, err
			}
			for index, node := range snapshot.Nodes {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				cached, exists := nodes[node.ID]
				if !exists || cached.node.ResourceType != node.ResourceType || (cached.node.Dimensions == nil) != (node.Dimensions == nil) || !maps.Equal(cached.node.Dimensions, node.Dimensions) {
					encoded, e := b.meter.marshal(node)
					if e != nil {
						return nil, e
					}
					charge := int64(cap(encoded) - cap(cached.encoded))
					if !exists {
						charge += 128
					}
					if e = b.meter.cache(charge); e != nil {
						return nil, e
					}
					cached = topologyCachedNode{node: node, encoded: encoded, boundary: xxhash.Sum64(encoded)}
					nodes[node.ID] = cached
				}
				if err = b.append(cached.encoded, cached.boundary); err != nil {
					return nil, err
				}
				if index+1 < len(snapshot.Nodes) {
					if err = b.append([]byte(","), 1); err != nil {
						return nil, err
					}
				}
			}
			if err = raw("]"); err != nil {
				return nil, err
			}
		}
		if err := b.flush(); err != nil {
			return nil, err
		}
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
					encoded, e := b.meter.marshal(edge)
					if e != nil {
						return nil, e
					}
					if e = b.meter.cache(int64(cap(encoded)) + 192); e != nil {
						return nil, e
					}
					cached = topologyCachedEdge{encoded: encoded, boundary: xxhash.Sum64(encoded)}
					edges[edge] = cached
				}
				if err = b.append(cached.encoded, cached.boundary); err != nil {
					return nil, err
				}
				if index+1 < len(snapshot.Edges) {
					if err = b.append([]byte(","), 1); err != nil {
						return nil, err
					}
				}
			}
			if err = raw("]"); err != nil {
				return nil, err
			}
		}
		trailer, err := b.meter.marshal(topologySnapshotTrailer{snapshot.Partial, snapshot.PartialReason})
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
		if err := b.flush(); err != nil {
			return nil, err
		}
		if consume {
			snapshots[frame].Nodes, snapshots[frame].Edges = nil, nil
		}
		// Low reuse can make per-row encoding more expensive than the original
		// per-frame encoder. Check every frame so stable-to-changing input adapts.
		if b.meter == nil && frame > 0 && b.hitBytes-hitsStart < (b.size-frameStart)/8 {
			fallback = true
			b.byHash, b.work, nodes, edges = nil, nil, nil, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.parts, nil
}
