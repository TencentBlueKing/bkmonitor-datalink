// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package viewstream is the direct control protocol of decision-016: the
// Control Leader keeps one desired set -- which publication the fleet
// executes, what content each Query Group is published with, and who each
// Query Group is assigned to -- and pushes each Worker its own projection of
// it over one stream, as a snapshot and then as one-step deltas. The Worker
// installs what it receives atomically and reports back per version.
//
// What travels is references, not content: an entry names a Query Group's
// object and output contexts by digest and the Worker reads the bytes
// through the content-addressed catalog it already has, where a cache hit is
// the common path. On the deployment measured for the design that makes a
// Worker's snapshot a few hundred kilobytes of references over some six
// megabytes of objects it mostly already holds.
//
// Nothing here is a lease. Assignment facts in a view are a preview of what
// the Leader wrote to the Assignment record; the authority a Worker executes
// under is what its lease renewal reads back from that record, and this
// package neither extends nor grants it.
package viewstream

import (
	"errors"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ProtocolVersion is the wire contract a Hello declares. A Leader refuses a
// Worker on another one rather than guessing what its messages mean.
const ProtocolVersion uint32 = 1

// viewDigestDomain is the digest domain of a View's canonical encoding.
const viewDigestDomain = "alarmd-view-v1"

// Publication is the activation a View is projected from: the publication
// the fleet executes and the revision of the activation record that named
// it, which is the version header the Worker's hot path probes today.
type Publication struct {
	SnapshotRevision         execution.SnapshotRevision `json:"snapshot_revision"`
	PublicationEpoch         uint64                     `json:"publication_epoch"`
	ActivationRecordRevision uint64                     `json:"activation_record_revision"`
}

// Content is what a Query Group is published with: the digest of its
// execution object and of the output context of each of its Plans, the same
// references its open Segment carries.
type Content struct {
	ObjectDigest   execution.ObjectDigest `json:"object_digest"`
	OutputContexts []OutputContextRef     `json:"output_contexts"`
}

type OutputContextRef struct {
	Plan   execution.PlanIdentity        `json:"plan"`
	Digest execution.OutputContextDigest `json:"digest"`
}

// Assignment is the Leader's decision for one Query Group as it stands in
// the Assignment record: who it is assigned to, at which record revision,
// and the content scope the record carries -- current, and pending with the
// Redis instant it takes effect at. A preview only; see the package comment.
type Assignment struct {
	DesiredWorkerID     string `json:"desired_worker_id"`
	Revision            uint64 `json:"revision"`
	ContentScope        string `json:"content_scope,omitempty"`
	PendingContentScope string `json:"pending_content_scope,omitempty"`
	EffectiveAtMs       int64  `json:"effective_at_ms,omitempty"`
}

// Entry is one Query Group in a Worker's view. Content is nil for a Query
// Group the Worker is assigned but the current publication no longer
// carries: it is draining, and there is nothing current to execute.
type Entry struct {
	QueryGroup execution.QueryGroupIdentity `json:"query_group"`
	Content    *Content                     `json:"content,omitempty"`
	Assignment Assignment                   `json:"assignment"`
}

// Version names one published view: the Leader's term, the transport
// revision within it, and the digest of the projected entries. The three
// are distinct on purpose: control_epoch is the Leader's term, owner_epoch
// (not here) is a Query Group's ownership generation, and the revision is a
// counter of the term's publications. The digest is the Worker's own -- two
// Workers at one revision hold different views -- so a revision of the term
// is named by the first two alone (Key).
type Version struct {
	ControlEpoch uint64 `json:"control_epoch"`
	Revision     uint64 `json:"revision"`
	Digest       string `json:"digest"`
}

// Key is a revision of a term, without any Worker's digest.
type Key struct {
	ControlEpoch uint64
	Revision     uint64
}

func (version Version) Key() Key {
	return Key{ControlEpoch: version.ControlEpoch, Revision: version.Revision}
}

// View is one Worker's projection of the desired set at one Version.
// Entries are sorted by Query Group; the digest is over Publication and
// Entries and nothing else, so two Workers with the same entries at
// different revisions -- an empty delta -- have the same digest.
type View struct {
	WorkerID    string      `json:"worker_id"`
	Version     Version     `json:"version"`
	Publication Publication `json:"publication"`
	Entries     []Entry     `json:"entries"`
}

// viewBody is what the digest covers.
type viewBody struct {
	Publication Publication `json:"publication"`
	Entries     []Entry     `json:"entries"`
}

// DigestOf computes the digest of a view's body. It sorts the entries first
// so the caller cannot produce two digests for one set.
func DigestOf(publication Publication, entries []Entry) (string, error) {
	if entries == nil {
		entries = []Entry{}
	}
	sortEntries(entries)
	digest, err := contract.DeriveCanonicalDigestV2(viewDigestDomain, viewBody{Publication: publication, Entries: entries})
	if err != nil {
		return "", fmt.Errorf("alarmd viewstream: view digest: %w", err)
	}
	return digest, nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(left, right int) bool { return entries[left].QueryGroup < entries[right].QueryGroup })
	for index := range entries {
		if entries[index].Content != nil {
			refs := entries[index].Content.OutputContexts
			sort.Slice(refs, func(left, right int) bool { return planLess(refs[left].Plan, refs[right].Plan) })
		}
	}
}

func planLess(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

// Validate checks a view is well formed: a Worker, a term, a digest that
// matches its body, and no Query Group twice. The revision is the
// publisher's to assign and is checked where a view is received.
func (view View) Validate() error {
	if view.WorkerID == "" || view.Version.ControlEpoch == 0 {
		return errors.New("alarmd viewstream: view needs a worker and a control epoch")
	}
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(view.Entries))
	for _, entry := range view.Entries {
		if entry.QueryGroup == "" {
			return errors.New("alarmd viewstream: view entry without a Query Group")
		}
		if _, dup := seen[entry.QueryGroup]; dup {
			return fmt.Errorf("alarmd viewstream: view names %s twice", entry.QueryGroup)
		}
		seen[entry.QueryGroup] = struct{}{}
	}
	entries := append([]Entry(nil), view.Entries...)
	digest, err := DigestOf(view.Publication, entries)
	if err != nil {
		return err
	}
	if digest != view.Version.Digest {
		return fmt.Errorf("%w: computed %s, named %s", ErrDigestMismatch, digest, view.Version.Digest)
	}
	return nil
}

// ErrDigestMismatch reports a view or delta whose named digest is not the
// digest of its body. The Worker refuses it and asks for a snapshot.
var ErrDigestMismatch = errors.New("alarmd viewstream: digest does not match the body")
