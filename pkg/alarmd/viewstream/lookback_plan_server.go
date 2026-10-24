// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// lookbackPlanSets is every Worker's lookback plan set as the Leader sends
// it: the message and its digest, built once when the sets are set, and the
// empty set for a Worker with no entry, which withdraws whatever it holds.
type lookbackPlanSets struct {
	byWorker map[string]plannedSet
	empty    plannedSet
}

type plannedSet struct {
	message *pb.LeaderMessage
	digest  string
}

func plannedSetOf(controlEpoch uint64, plans []LookbackPlan) (plannedSet, error) {
	message := LookbackPlansMessage(controlEpoch, plans)
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return plannedSet{}, fmt.Errorf("alarmd viewstream: encode lookback plans: %w", err)
	}
	if len(encoded) > MessageBytes {
		return plannedSet{}, fmt.Errorf("alarmd viewstream: lookback plans of %d bytes exceed one message", len(encoded))
	}
	digest := sha256.Sum256(encoded)
	return plannedSet{message: message, digest: hex.EncodeToString(digest[:])}, nil
}

// SetLookbackPlans makes byWorker the lookback plan sets of this term, each
// Worker's whole set, sent to it now and on every stream it opens until the
// next call; a Worker not in byWorker is sent the empty set. A set equal to
// the one a stream last carried is not sent again. Nil sends nothing at all
// and leaves every Worker with what it holds, to expire on its own: what a
// Leader that does not plan - a fallback Leader - has to say.
func (server *Server) SetLookbackPlans(byWorker map[string][]LookbackPlan) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	if byWorker == nil || server.publisher == nil {
		server.plans = nil
		return nil
	}
	epoch := server.publisher.epoch
	sets := &lookbackPlanSets{byWorker: make(map[string]plannedSet, len(byWorker))}
	var err error
	if sets.empty, err = plannedSetOf(epoch, nil); err != nil {
		return err
	}
	for worker, plans := range byWorker {
		if sets.byWorker[worker], err = plannedSetOf(epoch, plans); err != nil {
			return err
		}
	}
	server.plans = sets
	for _, sess := range server.sessions {
		sess.poke()
	}
	return nil
}

// lookbackPlansFor is the set to send workerID, false while the term has
// none.
func (server *Server) lookbackPlansFor(workerID string) (plannedSet, bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.plans == nil {
		return plannedSet{}, false
	}
	if set, ok := server.plans.byWorker[workerID]; ok {
		return set, true
	}
	return server.plans.empty, true
}

// sendLookbackPlans sends the Worker its plan set when it is not the one
// this stream last carried: a new stream carries it on its first turn.
func (sess *session) sendLookbackPlans() error {
	set, ok := sess.server.lookbackPlansFor(sess.receiver.WorkerID)
	if !ok {
		return nil
	}
	sess.mu.Lock()
	sent := sess.plansSent
	sess.mu.Unlock()
	if set.digest == sent {
		return nil
	}
	if err := sess.stream.Send(set.message); err != nil {
		return err
	}
	sess.mu.Lock()
	sess.plansSent = set.digest
	sess.mu.Unlock()
	sess.server.count(func(c *serverCounters) { c.lookbackPlanSets++ })
	return nil
}
