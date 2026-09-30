// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Publisher is the Leader's side of the contract for one term: it holds the
// latest desired set projected per Worker, the projection one step before
// it, and the ledger of the current version. A new term is a new Publisher;
// a Worker that connects with a view from another term gets a snapshot.
//
// One revision counter serves every Worker. A publication that changes the
// projection of any Worker is a new revision for all of them; a Worker whose
// projection did not change receives an empty delta and installs the
// revision by receipt. That is simpler than a revision per Worker and costs
// a few bytes per unaffected Worker per publication.
type Publisher struct {
	mu          sync.Mutex
	epoch       uint64
	revision    uint64
	publication Publication
	current     map[string]View
	previous    map[string]View
	ledger      *Ledger
	// fingerprint is Desired.Fingerprint of the last set projected, so a
	// round that hands over the same set again costs one hash and no
	// projection.
	fingerprint uint64
}

func NewPublisher(controlEpoch uint64, now func() time.Time) (*Publisher, error) {
	if controlEpoch == 0 {
		return nil, errors.New("alarmd viewstream: publisher needs the leader's control epoch")
	}
	return &Publisher{epoch: controlEpoch, current: map[string]View{}, ledger: NewLedger(now)}, nil
}

// Published is what one Publish did.
type Published struct {
	Version Key
	// Changed is false when no Worker's projection moved: the revision did
	// not advance and nothing is sent.
	Changed bool
	// Affected lists the Workers whose projection changed, sorted; every
	// other Worker with a view receives an empty delta.
	Affected []string
	// Closed are the versions this publication pushed out of the ledger.
	Closed []Outcome
}

// Publish takes the desired set of one reconcile round. It projects it for
// every Worker the set names and every Worker that had a view before -- a
// Worker whose last Query Group moved away has to receive that removal --
// and advances the revision if any projection changed.
func (publisher *Publisher) Publish(desired Desired) (Published, error) {
	if desired.ControlEpoch != publisher.epoch {
		return Published{}, fmt.Errorf("alarmd viewstream: desired set is of term %d, publisher of term %d", desired.ControlEpoch, publisher.epoch)
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	fingerprint := desired.Fingerprint()
	if publisher.revision > 0 && fingerprint == publisher.fingerprint {
		return Published{Version: Key{ControlEpoch: publisher.epoch, Revision: publisher.revision}, Changed: false}, nil
	}
	// Every Worker the set names, and every Worker that still holds
	// something: a Worker whose last Query Group moved away is projected
	// once more, to an empty view, and not after.
	workers := desired.Workers()
	for worker, view := range publisher.current {
		if _, named := indexOf(workers, worker); !named && len(view.Entries) > 0 {
			workers = append(workers, worker)
		}
	}
	next := make(map[string]View, len(workers))
	var affected []string
	for _, worker := range workers {
		view, err := desired.Project(worker)
		if err != nil {
			return Published{}, err
		}
		if before, had := publisher.current[worker]; !had || before.Version.Digest != view.Version.Digest {
			affected = append(affected, worker)
		}
		next[worker] = view
	}
	publisher.fingerprint = fingerprint
	if len(affected) == 0 && publisher.revision > 0 {
		return Published{Version: Key{ControlEpoch: publisher.epoch, Revision: publisher.revision}, Changed: false}, nil
	}
	publisher.revision++
	digests := make(map[string]string, len(next))
	for worker, view := range next {
		view.Version.Revision = publisher.revision
		next[worker] = view
		digests[worker] = view.Version.Digest
	}
	publisher.previous, publisher.current, publisher.publication = publisher.current, next, desired.Publication
	sort.Strings(affected)
	version := Key{ControlEpoch: publisher.epoch, Revision: publisher.revision}
	closed := publisher.ledger.Open(version, digests)
	return Published{Version: version, Changed: true, Affected: affected, Closed: closed}, nil
}

// Revision is the current transport revision of the term, zero before the
// first publication.
func (publisher *Publisher) Revision() uint64 {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return publisher.revision
}

// Snapshot is the current view of a Worker. A Worker the desired set does
// not name gets an empty view at the current revision -- "you have
// nothing" is a view too -- and false only before the first publication.
func (publisher *Publisher) Snapshot(workerID string) (View, bool) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if publisher.revision == 0 {
		return View{}, false
	}
	if view, ok := publisher.current[workerID]; ok {
		return view, true
	}
	digest, err := DigestOf(publisher.publication, nil)
	if err != nil {
		return View{}, false
	}
	return View{WorkerID: workerID, Version: Version{ControlEpoch: publisher.epoch, Revision: publisher.revision, Digest: digest},
		Publication: publisher.publication, Entries: []Entry{}}, true
}

// Step is the delta that takes a Worker from the version it has installed
// to the current one, when the installed version is exactly the previous
// projection of that Worker; false when it is not, and the Worker needs a
// snapshot. Nothing is kept beyond one step, so a Worker two behind gets a
// snapshot rather than two deltas.
func (publisher *Publisher) Step(workerID string, installed Version) (Delta, bool) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	current, ok := publisher.current[workerID]
	if !ok {
		return Delta{}, false
	}
	previous, had := publisher.previous[workerID]
	if !had || previous.Version != installed {
		return Delta{}, false
	}
	delta, err := Diff(previous, current)
	if err != nil {
		return Delta{}, false
	}
	return delta, true
}

// Holds reports whether version is a view this publisher gave workerID:
// its current projection or the one before. A Worker claiming any other
// version of this term -- a digest that is not its own, a revision no
// longer kept -- is holding something the publisher cannot vouch for and
// is sent a snapshot.
func (publisher *Publisher) Holds(workerID string, version Version) bool {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if current, ok := publisher.current[workerID]; ok && current.Version == version {
		return true
	}
	previous, ok := publisher.previous[workerID]
	return ok && previous.Version == version
}

// Ledger is the four numbers of the current version and the one before.
func (publisher *Publisher) Ledger() *Ledger {
	return publisher.ledger
}

func indexOf(list []string, item string) (int, bool) {
	for index, candidate := range list {
		if candidate == item {
			return index, true
		}
	}
	return -1, false
}
