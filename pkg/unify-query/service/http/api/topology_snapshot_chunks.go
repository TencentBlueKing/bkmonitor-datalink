package api

import (
	"bytes"
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
	meter  *topologyEncodeMeter
	work   []byte
	parts  [][]byte
	byHash map[uint64][][]byte
	size   int
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
		owned := make([]byte, len(b.work), next)
		copy(owned, b.work)
		b.work = owned
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
	hash := xxhash.Sum64(b.work)
	for _, owned := range b.byHash[hash] {
		if bytes.Equal(owned, b.work) {
			b.parts = append(b.parts, owned)
			b.work = b.work[:0]
			return nil
		}
	}
	// Copy the used bytes: retaining a full working-buffer capacity per small block
	// would increase memory for changing snapshots. Cached blocks are immutable.
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

// topologySnapshotSource 按顺序交出快照帧；共交出声明的帧数。
type topologySnapshotSource func(yield func(cmdb.SharedTopologySnapshot) error) error

func topologySnapshotSlice(snapshots []cmdb.SharedTopologySnapshot) topologySnapshotSource {
	return func(yield func(cmdb.SharedTopologySnapshot) error) error {
		for _, snapshot := range snapshots {
			if err := yield(snapshot); err != nil {
				return err
			}
		}
		return nil
	}
}

func marshalTopologySnapshotFrames(meter *topologyEncodeMeter, snapshots []cmdb.SharedTopologySnapshot) ([][]byte, error) {
	return marshalTopologySnapshotSource(meter, len(snapshots), topologySnapshotSlice(snapshots))
}

// marshalTopologySnapshotSource 逐帧编码：每帧编码完即可丢弃该帧的 DTO，片段与一次性编码全部帧相同。
func marshalTopologySnapshotSource(meter *topologyEncodeMeter, count int, source topologySnapshotSource) ([][]byte, error) {
	ctx := meter.ctx
	b := topologySnapshotChunkBuilder{meter: meter, byHash: make(map[uint64][][]byte)}
	nodes := make(map[uint64]topologyCachedNode)
	edges := make(map[cmdb.SharedTopologyEdge]topologyCachedEdge)
	raw := func(text string) error { return b.append([]byte(text), 1) }
	frame := 0
	err := source(func(snapshot cmdb.SharedTopologySnapshot) (err error) {
		defer func() { frame++ }()
		if frame >= count {
			return fmt.Errorf("topology snapshots exceed the declared %d frames", count)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := b.meter.marshal(topologySnapshotHeader{snapshot.Timestamp})
		if err != nil {
			return err
		}
		if err = b.append(header[:len(header)-1], 1); err != nil {
			return err
		}
		if err = raw(`,"nodes":`); err != nil {
			return err
		}
		if snapshot.Nodes == nil {
			if err = raw("null"); err != nil {
				return err
			}
		} else {
			if err = raw("["); err != nil {
				return err
			}
			// Isolate the changing timestamp from otherwise stable node blocks.
			if err := b.flush(); err != nil {
				return err
			}
			for index, node := range snapshot.Nodes {
				if err := ctx.Err(); err != nil {
					return err
				}
				cached, exists := nodes[node.ID]
				if !exists || cached.node.ResourceType != node.ResourceType || (cached.node.Dimensions == nil) != (node.Dimensions == nil) || !maps.Equal(cached.node.Dimensions, node.Dimensions) {
					encoded, e := b.meter.marshal(node)
					if e != nil {
						return e
					}
					cached = topologyCachedNode{node: node, encoded: encoded, boundary: xxhash.Sum64(encoded)}
					nodes[node.ID] = cached
				}
				if err = b.append(cached.encoded, cached.boundary); err != nil {
					return err
				}
				if index+1 < len(snapshot.Nodes) {
					if err = b.append([]byte(","), 1); err != nil {
						return err
					}
				}
			}
			if err = raw("]"); err != nil {
				return err
			}
		}
		if err := b.flush(); err != nil {
			return err
		}
		if err = raw(`,"edges":`); err != nil {
			return err
		}
		if snapshot.Edges == nil {
			if err = raw("null"); err != nil {
				return err
			}
		} else {
			if err = raw("["); err != nil {
				return err
			}
			for index, edge := range snapshot.Edges {
				if err := ctx.Err(); err != nil {
					return err
				}
				cached, exists := edges[edge]
				if !exists {
					encoded, e := b.meter.marshal(edge)
					if e != nil {
						return e
					}
					cached = topologyCachedEdge{encoded: encoded, boundary: xxhash.Sum64(encoded)}
					edges[edge] = cached
				}
				if err = b.append(cached.encoded, cached.boundary); err != nil {
					return err
				}
				if index+1 < len(snapshot.Edges) {
					if err = b.append([]byte(","), 1); err != nil {
						return err
					}
				}
			}
			if err = raw("]"); err != nil {
				return err
			}
		}
		trailer, err := b.meter.marshal(topologySnapshotTrailer{snapshot.Partial, snapshot.PartialReason})
		if err != nil {
			return err
		}
		if err = raw(","); err != nil {
			return err
		}
		if err = b.append(trailer[1:], 1); err != nil {
			return err
		}
		if frame+1 < count {
			if err = raw(","); err != nil {
				return err
			}
		}
		return b.flush()
	})
	if err != nil {
		return nil, err
	}
	if frame != count {
		return nil, fmt.Errorf("topology snapshots ended after %d of %d frames", frame, count)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.parts, nil
}
