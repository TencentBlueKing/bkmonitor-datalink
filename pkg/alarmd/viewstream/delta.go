// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Delta takes one Worker from the view at Base to the view at Target:
// the entries that are new or changed, and the Query Groups that left.
// It is diff(project(vN, w), project(vN+1, w)) -- the projections are
// diffed, not the desired sets -- so a Query Group newly assigned to the
// Worker arrives with its content and one taken away arrives as a removal.
// It spans exactly one step; a Worker installed at any other revision asks
// for a snapshot instead.
type Delta struct {
	WorkerID    string                         `json:"worker_id"`
	Base        Version                        `json:"base"`
	Target      Version                        `json:"target"`
	Publication Publication                    `json:"publication"`
	Upserts     []Entry                        `json:"upserts"`
	Removed     []execution.QueryGroupIdentity `json:"removed"`
}

// Empty reports a delta that changes nothing for its Worker: the desired
// set moved, this Worker's projection did not, and the Worker installs the
// new revision by receipt alone.
func (delta Delta) Empty() bool {
	return len(delta.Upserts) == 0 && len(delta.Removed) == 0 && delta.Base.Digest == delta.Target.Digest
}

// Diff computes the delta from one view of a Worker to the next. Both must
// be the same Worker's; the next must be exactly one revision past the
// previous in the same term.
func Diff(previous, next View) (Delta, error) {
	if previous.WorkerID == "" || previous.WorkerID != next.WorkerID {
		return Delta{}, errors.New("alarmd viewstream: a delta is between two views of one worker")
	}
	if next.Version.ControlEpoch != previous.Version.ControlEpoch || next.Version.Revision != previous.Version.Revision+1 {
		return Delta{}, fmt.Errorf("alarmd viewstream: a delta spans one step of one term, not %d/%d -> %d/%d",
			previous.Version.ControlEpoch, previous.Version.Revision, next.Version.ControlEpoch, next.Version.Revision)
	}
	before := make(map[execution.QueryGroupIdentity]Entry, len(previous.Entries))
	for _, entry := range previous.Entries {
		before[entry.QueryGroup] = entry
	}
	delta := Delta{WorkerID: next.WorkerID, Base: previous.Version, Target: next.Version, Publication: next.Publication}
	after := make(map[execution.QueryGroupIdentity]struct{}, len(next.Entries))
	for _, entry := range next.Entries {
		after[entry.QueryGroup] = struct{}{}
		if was, ok := before[entry.QueryGroup]; ok && reflect.DeepEqual(was, entry) {
			continue
		}
		delta.Upserts = append(delta.Upserts, entry)
	}
	for _, entry := range previous.Entries {
		if _, kept := after[entry.QueryGroup]; !kept {
			delta.Removed = append(delta.Removed, entry.QueryGroup)
		}
	}
	sortEntries(delta.Upserts)
	sortIdentities(delta.Removed)
	return delta, nil
}

// ErrDeltaBaseMismatch reports a delta whose base is not the view the
// Worker has installed. The Worker asks for a snapshot; it never applies a
// delta to a different base and never skips a step.
var ErrDeltaBaseMismatch = errors.New("alarmd viewstream: delta base is not the installed view")

// Apply produces the view a delta leads to from the view it starts from,
// or refuses: the installed view must be exactly the delta's base by
// revision and digest, and the result must hash to the delta's target
// digest. A delta that fails either check is not applied at all.
func Apply(installed View, delta Delta) (View, error) {
	if installed.WorkerID != delta.WorkerID {
		return View{}, errors.New("alarmd viewstream: delta is for another worker")
	}
	if installed.Version != delta.Base {
		return View{}, fmt.Errorf("%w: installed %d/%d %s, delta base %d/%d %s", ErrDeltaBaseMismatch,
			installed.Version.ControlEpoch, installed.Version.Revision, installed.Version.Digest,
			delta.Base.ControlEpoch, delta.Base.Revision, delta.Base.Digest)
	}
	if delta.Target.ControlEpoch != delta.Base.ControlEpoch || delta.Target.Revision != delta.Base.Revision+1 {
		return View{}, errors.New("alarmd viewstream: delta target is not one step past its base")
	}
	merged := make(map[execution.QueryGroupIdentity]Entry, len(installed.Entries)+len(delta.Upserts))
	for _, entry := range installed.Entries {
		merged[entry.QueryGroup] = entry
	}
	for _, entry := range delta.Upserts {
		merged[entry.QueryGroup] = entry
	}
	for _, queryGroup := range delta.Removed {
		delete(merged, queryGroup)
	}
	entries := make([]Entry, 0, len(merged))
	for _, entry := range merged {
		entries = append(entries, entry)
	}
	digest, err := DigestOf(delta.Publication, entries)
	if err != nil {
		return View{}, err
	}
	if digest != delta.Target.Digest {
		return View{}, fmt.Errorf("%w: applied %s, target names %s", ErrDigestMismatch, digest, delta.Target.Digest)
	}
	return View{WorkerID: installed.WorkerID, Version: delta.Target, Publication: delta.Publication, Entries: entries}, nil
}

func sortIdentities(identities []execution.QueryGroupIdentity) {
	for i := 1; i < len(identities); i++ {
		for j := i; j > 0 && identities[j] < identities[j-1]; j-- {
			identities[j], identities[j-1] = identities[j-1], identities[j]
		}
	}
}
