// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// SnapshotChunkBytes bounds one Snapshot message. A view larger than it is
// sent in chunks that share the version and are installed together. Well
// under gRPC's default 4 MiB message bound; on the deployment measured a
// whole view is a few hundred kilobytes and travels as one chunk.
const SnapshotChunkBytes = 1 << 20

func versionToWire(version Version) *pb.Version {
	return &pb.Version{ControlEpoch: version.ControlEpoch, Revision: version.Revision, Digest: version.Digest}
}

func versionFromWire(version *pb.Version) Version {
	if version == nil {
		return Version{}
	}
	return Version{ControlEpoch: version.ControlEpoch, Revision: version.Revision, Digest: version.Digest}
}

func publicationToWire(publication Publication) *pb.Publication {
	return &pb.Publication{SnapshotRevision: string(publication.SnapshotRevision), PublicationEpoch: publication.PublicationEpoch,
		ActivationRecordRevision: publication.ActivationRecordRevision}
}

func publicationFromWire(publication *pb.Publication) Publication {
	if publication == nil {
		return Publication{}
	}
	return Publication{SnapshotRevision: execution.SnapshotRevision(publication.SnapshotRevision), PublicationEpoch: publication.PublicationEpoch,
		ActivationRecordRevision: publication.ActivationRecordRevision}
}

func entryToWire(entry Entry) *pb.Entry {
	wire := &pb.Entry{QueryGroup: string(entry.QueryGroup), Assignment: &pb.Assignment{
		DesiredWorkerId: entry.Assignment.DesiredWorkerID, Revision: entry.Assignment.Revision,
		ContentScope: entry.Assignment.ContentScope, PendingContentScope: entry.Assignment.PendingContentScope,
		EffectiveAtMs: entry.Assignment.EffectiveAtMs,
	}}
	if entry.Content != nil {
		content := &pb.Content{ObjectDigest: string(entry.Content.ObjectDigest)}
		for _, ref := range entry.Content.OutputContexts {
			content.OutputContexts = append(content.OutputContexts, &pb.OutputContextRef{
				Plan:   &pb.PlanIdentity{TenantId: ref.Plan.TenantID, BusinessId: ref.Plan.BusinessID, StrategyId: ref.Plan.StrategyID},
				Digest: string(ref.Digest),
			})
		}
		wire.Content = content
	}
	return wire
}

func entryFromWire(wire *pb.Entry) (Entry, error) {
	if wire == nil || wire.QueryGroup == "" {
		return Entry{}, errors.New("alarmd viewstream: entry without a Query Group")
	}
	entry := Entry{QueryGroup: execution.QueryGroupIdentity(wire.QueryGroup)}
	if wire.Assignment != nil {
		entry.Assignment = Assignment{
			DesiredWorkerID: wire.Assignment.DesiredWorkerId, Revision: wire.Assignment.Revision,
			ContentScope: wire.Assignment.ContentScope, PendingContentScope: wire.Assignment.PendingContentScope,
			EffectiveAtMs: wire.Assignment.EffectiveAtMs,
		}
	}
	if wire.Content != nil {
		content := Content{ObjectDigest: execution.ObjectDigest(wire.Content.ObjectDigest), OutputContexts: make([]OutputContextRef, 0, len(wire.Content.OutputContexts))}
		for _, ref := range wire.Content.OutputContexts {
			if ref == nil || ref.Plan == nil {
				return Entry{}, fmt.Errorf("alarmd viewstream: entry %s names an output context without a Plan", wire.QueryGroup)
			}
			content.OutputContexts = append(content.OutputContexts, OutputContextRef{
				Plan:   execution.PlanIdentity{TenantID: ref.Plan.TenantId, BusinessID: ref.Plan.BusinessId, StrategyID: ref.Plan.StrategyId},
				Digest: execution.OutputContextDigest(ref.Digest),
			})
		}
		entry.Content = &content
	}
	return entry, nil
}

// SnapshotChunks encodes a view as one or more Snapshot messages of at most
// maxBytes each (SnapshotChunkBytes when zero), every one carrying the
// version and its place in the sequence. An empty view is one chunk with no
// entries: "you have nothing" is a snapshot too.
func SnapshotChunks(view View, maxBytes int) []*pb.Snapshot {
	if maxBytes <= 0 {
		maxBytes = SnapshotChunkBytes
	}
	var chunks []*pb.Snapshot
	open := func() *pb.Snapshot {
		return &pb.Snapshot{Version: versionToWire(view.Version), Publication: publicationToWire(view.Publication)}
	}
	current := open()
	size := proto.Size(current)
	for _, entry := range view.Entries {
		wire := entryToWire(entry)
		wireSize := proto.Size(wire) + 4
		if len(current.Entries) > 0 && size+wireSize > maxBytes {
			chunks = append(chunks, current)
			current = open()
			size = proto.Size(current)
		}
		current.Entries = append(current.Entries, wire)
		size += wireSize
	}
	chunks = append(chunks, current)
	for index, chunk := range chunks {
		chunk.Chunk, chunk.Chunks = uint32(index+1), uint32(len(chunks))
	}
	return chunks
}

// AssembleSnapshot puts the chunks of one snapshot back into a view for
// workerID and validates it: every chunk present once, all of one version,
// and the body hashing to the version's digest. Anything short of that is
// refused whole; no partial view exists.
func AssembleSnapshot(workerID string, chunks []*pb.Snapshot) (View, error) {
	if workerID == "" || len(chunks) == 0 {
		return View{}, errors.New("alarmd viewstream: a snapshot needs a worker and at least one chunk")
	}
	first := chunks[0]
	if first == nil || first.Version == nil || first.Chunks == 0 || int(first.Chunks) != len(chunks) {
		return View{}, fmt.Errorf("alarmd viewstream: snapshot has %d chunks, %d given", chunkCount(first), len(chunks))
	}
	version := versionFromWire(first.Version)
	view := View{WorkerID: workerID, Version: version, Publication: publicationFromWire(first.Publication), Entries: []Entry{}}
	seen := make(map[uint32]struct{}, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil || versionFromWire(chunk.Version) != version || publicationFromWire(chunk.Publication) != view.Publication {
			return View{}, errors.New("alarmd viewstream: snapshot chunks are not all of one version")
		}
		if chunk.Chunk == 0 || chunk.Chunk > first.Chunks {
			return View{}, fmt.Errorf("alarmd viewstream: snapshot chunk %d of %d is out of range", chunk.Chunk, first.Chunks)
		}
		if _, dup := seen[chunk.Chunk]; dup {
			return View{}, fmt.Errorf("alarmd viewstream: snapshot chunk %d given twice", chunk.Chunk)
		}
		seen[chunk.Chunk] = struct{}{}
		for _, wire := range chunk.Entries {
			entry, err := entryFromWire(wire)
			if err != nil {
				return View{}, err
			}
			view.Entries = append(view.Entries, entry)
		}
	}
	sortEntries(view.Entries)
	if err := view.Validate(); err != nil {
		return View{}, err
	}
	if view.Version.Revision == 0 {
		return View{}, errors.New("alarmd viewstream: snapshot without a revision")
	}
	return view, nil
}

func chunkCount(chunk *pb.Snapshot) int {
	if chunk == nil {
		return 0
	}
	return int(chunk.Chunks)
}

// DeltaToWire encodes a delta.
func DeltaToWire(delta Delta) *pb.Delta {
	wire := &pb.Delta{Base: versionToWire(delta.Base), Target: versionToWire(delta.Target), Publication: publicationToWire(delta.Publication)}
	for _, entry := range delta.Upserts {
		wire.Upserts = append(wire.Upserts, entryToWire(entry))
	}
	for _, queryGroup := range delta.Removed {
		wire.Removed = append(wire.Removed, string(queryGroup))
	}
	return wire
}

// DeltaFromWire decodes a delta for workerID. It checks the envelope --
// target one step past base in one term -- and leaves whether it applies
// to Apply, which knows the installed view.
func DeltaFromWire(workerID string, wire *pb.Delta) (Delta, error) {
	if workerID == "" || wire == nil || wire.Base == nil || wire.Target == nil {
		return Delta{}, errors.New("alarmd viewstream: delta without base or target")
	}
	delta := Delta{WorkerID: workerID, Base: versionFromWire(wire.Base), Target: versionFromWire(wire.Target), Publication: publicationFromWire(wire.Publication)}
	if delta.Target.ControlEpoch != delta.Base.ControlEpoch || delta.Target.Revision != delta.Base.Revision+1 || delta.Base.Revision == 0 {
		return Delta{}, errors.New("alarmd viewstream: delta target is not one step past its base")
	}
	for _, entry := range wire.Upserts {
		decoded, err := entryFromWire(entry)
		if err != nil {
			return Delta{}, err
		}
		delta.Upserts = append(delta.Upserts, decoded)
	}
	for _, queryGroup := range wire.Removed {
		if queryGroup == "" {
			return Delta{}, errors.New("alarmd viewstream: delta removes an unnamed Query Group")
		}
		delta.Removed = append(delta.Removed, execution.QueryGroupIdentity(queryGroup))
	}
	sortEntries(delta.Upserts)
	sortIdentities(delta.Removed)
	return delta, nil
}

// ReceiptToWire encodes a receipt; ReceiptFromWire decodes one for the
// Worker the stream belongs to, which is not on the wire: the stream is.
func ReceiptToWire(receipt Receipt) *pb.Receipt {
	return &pb.Receipt{
		Incarnation: receipt.Receiver.Incarnation, Version: versionToWire(receipt.Version),
		Acked: receipt.Acked, Installed: receipt.Installed, Switched: receipt.Switched,
		Failure: receipt.Failure, ObjectsMissing: uint32(receipt.ObjectsMissing), ObjectsProbed: receipt.ObjectsProbed,
	}
}

func ReceiptFromWire(workerID string, wire *pb.Receipt) (Receipt, error) {
	if workerID == "" || wire == nil || wire.Version == nil {
		return Receipt{}, errors.New("alarmd viewstream: receipt without a version")
	}
	return Receipt{
		Receiver: Receiver{WorkerID: workerID, Incarnation: wire.Incarnation}, Version: versionFromWire(wire.Version),
		Acked: wire.Acked, Installed: wire.Installed, Switched: wire.Switched,
		Failure: wire.Failure, ObjectsMissing: int(wire.ObjectsMissing), ObjectsProbed: wire.ObjectsProbed,
	}, nil
}
