// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Desired is the Leader's whole desired set at one moment: everything a
// reconcile round knows once it has read the activation, that publication's
// manifest and the Assignment records it wrote. Projecting it per Worker is
// this package's job; producing it is the round's.
type Desired struct {
	ControlEpoch uint64
	Publication  Publication
	// Content is what the current publication carries, by Query Group. A
	// Query Group assigned but absent here is draining.
	Content map[execution.QueryGroupIdentity]Content
	// Assignments is every Assignment record the Leader holds, by Query
	// Group, including draining ones.
	Assignments map[execution.QueryGroupIdentity]Assignment
}

// Project is the view of one Worker: every Query Group whose Assignment
// names it, with the content the publication carries for it. The revision
// and digest are filled by the caller that publishes it; Project computes
// the digest so the caller cannot name a different one.
//
// A Query Group is in exactly the view of the Worker its record names. A
// Worker still holding a lease on a Query Group whose record moved away
// does not see it: the record is the authority, the view a preview of it,
// and the Worker learns the move the way it always did, from its renewal.
func (desired Desired) Project(workerID string) (View, error) {
	if workerID == "" {
		return View{}, errors.New("alarmd viewstream: projection needs a worker")
	}
	entries := make([]Entry, 0)
	for queryGroup, assignment := range desired.Assignments {
		if assignment.DesiredWorkerID != workerID {
			continue
		}
		entry := Entry{QueryGroup: queryGroup, Assignment: assignment}
		if content, published := desired.Content[queryGroup]; published {
			copied := content
			copied.OutputContexts = append([]OutputContextRef(nil), content.OutputContexts...)
			entry.Content = &copied
		}
		entries = append(entries, entry)
	}
	digest, err := DigestOf(desired.Publication, entries)
	if err != nil {
		return View{}, err
	}
	return View{
		WorkerID: workerID, Version: Version{ControlEpoch: desired.ControlEpoch, Digest: digest},
		Publication: desired.Publication, Entries: entries,
	}, nil
}

// Workers lists every Worker the desired set names, sorted: the receivers a
// publication of it expects.
func (desired Desired) Workers() []string {
	seen := make(map[string]struct{})
	for _, assignment := range desired.Assignments {
		if assignment.DesiredWorkerID != "" {
			seen[assignment.DesiredWorkerID] = struct{}{}
		}
	}
	workers := make([]string, 0, len(seen))
	for worker := range seen {
		workers = append(workers, worker)
	}
	sort.Strings(workers)
	return workers
}

// Fingerprint is a cheap identity of everything a projection depends on:
// the publication and, per Query Group in sorted order, its assignment and
// its content references. Two desired sets with one fingerprint project to
// the same views for every Worker, so the publisher, which is handed the
// desired set every reconcile round whether or not anything moved, can
// skip projecting and hashing the round that changed nothing. FNV over a
// few hundred kilobytes, not canonical JSON and SHA-256 over the same.
func (desired Desired) Fingerprint() uint64 {
	hash := fnv.New64a()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = hash.Write([]byte(part))
			_, _ = hash.Write([]byte{0})
		}
	}
	writeUint := func(value uint64) {
		var buffer [8]byte
		binary.LittleEndian.PutUint64(buffer[:], value)
		_, _ = hash.Write(buffer[:])
	}
	writeUint(desired.ControlEpoch)
	write(string(desired.Publication.SnapshotRevision))
	writeUint(desired.Publication.PublicationEpoch)
	writeUint(desired.Publication.ActivationRecordRevision)
	identities := make([]execution.QueryGroupIdentity, 0, len(desired.Assignments)+len(desired.Content))
	seen := make(map[execution.QueryGroupIdentity]struct{}, cap(identities))
	for identity := range desired.Assignments {
		identities = append(identities, identity)
		seen[identity] = struct{}{}
	}
	for identity := range desired.Content {
		if _, dup := seen[identity]; !dup {
			identities = append(identities, identity)
		}
	}
	sort.Slice(identities, func(left, right int) bool { return identities[left] < identities[right] })
	for _, identity := range identities {
		write("qg", string(identity))
		if assignment, ok := desired.Assignments[identity]; ok {
			write("a", assignment.DesiredWorkerID, assignment.ContentScope, assignment.PendingContentScope)
			writeUint(assignment.Revision)
			writeUint(uint64(assignment.EffectiveAtMs))
		}
		if content, ok := desired.Content[identity]; ok {
			write("c", string(content.ObjectDigest))
			// In Plan order, like the digest: a caller that hands the refs
			// over in another order each round must not make every round
			// look changed, or the skip this exists for never happens.
			refs := content.OutputContexts
			if len(refs) > 1 {
				refs = append([]OutputContextRef(nil), refs...)
				sort.Slice(refs, func(left, right int) bool { return planLess(refs[left].Plan, refs[right].Plan) })
			}
			for _, ref := range refs {
				write(ref.Plan.TenantID, ref.Plan.BusinessID, ref.Plan.StrategyID, string(ref.Digest))
			}
		}
	}
	return hash.Sum64()
}
