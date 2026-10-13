// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// ControlFacts carries a control instance's observations independently of
// Worker snapshots. The lease term and process incarnation bind these facts
// to the current leader, including when control runs without a Worker.
type ControlFacts struct {
	Replica          string                 `json:"replica"`
	Incarnation      string                 `json:"incarnation"`
	ControlEpoch     uint64                 `json:"control_epoch"`
	TakenAt          time.Time              `json:"taken_at"`
	Source           *SourceFacts           `json:"source,omitempty"`
	Activation       *ActivationFacts       `json:"activation,omitempty"`
	ActivationHeader *ActivationHeaderFacts `json:"activation_header,omitempty"`
	ControlSource    *ControlSourceFacts    `json:"control_source,omitempty"`
	Rebalance        *RebalanceFacts        `json:"rebalance,omitempty"`
	AssignmentScope  *AssignmentScopeFacts  `json:"assignment_scope,omitempty"`
	AssignmentSweep  *AssignmentSweepFacts  `json:"assignment_sweep,omitempty"`
	LeaderRound      *LeaderRoundFacts      `json:"leader_round,omitempty"`
	ViewStream       *ViewStreamFacts       `json:"view_stream,omitempty"`

	// CandidacyClass is the class the Leader that wrote the record stands
	// in: preferred, or fallback while a Worker leads because no control
	// process is there. Empty in records written before candidacy existed.
	CandidacyClass string `json:"candidacy_class,omitempty"`
}

func (facts ControlFacts) Validate() error {
	if facts.Replica == "" || facts.Incarnation == "" || facts.ControlEpoch == 0 || facts.TakenAt.IsZero() {
		return errors.New("alarmd fleet: control facts require replica, incarnation, epoch and capture time")
	}
	if facts.ViewStream != nil && facts.ViewStream.Leading && facts.ViewStream.ControlEpoch != facts.ControlEpoch {
		return errors.New("alarmd fleet: control stream epoch differs from control facts epoch")
	}
	return nil
}

// controlKey is the one fleet control record. One Control Leader holds the
// lease at a time, so the record is not keyed by replica: after a takeover the
// previous term's record stays readable under the same name until the new
// Leader's first write replaces it, and the reader tells the two apart by term.
func (store *RedisStore) controlKey() string {
	return store.prefix + ":fleet-control"
}

// Errors PublishControl refuses a record with before sending it.
var (
	ErrControlFactsInvalid   = errors.New("alarmd fleet: control facts are incomplete")
	ErrControlFactsOversized = errors.New("alarmd fleet: control facts exceed byte budget")
)

// ControlPublishResult names a PublishControl outcome for its counter:
// written, invalid, oversized, or failed when the store did not take it.
func ControlPublishResult(err error) string {
	switch {
	case err == nil:
		return "written"
	case errors.Is(err, ErrControlFactsInvalid):
		return "invalid"
	case errors.Is(err, ErrControlFactsOversized):
		return "oversized"
	default:
		return "failed"
	}
}

// PublishControl uses the snapshot retention and byte budget, on a key that
// no Worker coverage or capacity reader consults. An oversized record is
// refused as a whole so control facts never silently lose part of a round.
//
// A plain SET: during a takeover the previous Leader may still write once or
// twice before it learns it lost the lease. Its record carries its own, older
// epoch, so a reader takes it as the previous term's and never attaches it to
// the current one.
func (store *RedisStore) PublishControl(ctx context.Context, facts ControlFacts) error {
	if err := facts.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrControlFactsInvalid, err)
	}
	payload, err := json.Marshal(facts)
	if err != nil {
		return fmt.Errorf("%w: encode: %v", ErrControlFactsInvalid, err)
	}
	if len(payload) > store.maxAnomalyBytes {
		return ErrControlFactsOversized
	}
	if err := store.client.Set(ctx, store.controlKey(), payload, store.ttl).Err(); err != nil {
		return fmt.Errorf("alarmd fleet: publish control facts: %w", err)
	}
	return nil
}

// LoadControl reads the fleet control record, whichever term wrote it.
func (store *RedisStore) LoadControl(ctx context.Context) (ControlFacts, bool, error) {
	var facts ControlFacts
	found := false
	key := store.controlKey()
	_, err := store.read(ctx, []string{key}, func(string) string { return key }, nil, func(_ int, text string) error {
		if len(text) > store.maxAnomalyBytes {
			return errors.New("alarmd fleet: stored control facts exceed byte budget")
		}
		if err := json.Unmarshal([]byte(text), &facts); err != nil {
			return fmt.Errorf("alarmd fleet: decode control facts: %w", err)
		}
		if err := facts.Validate(); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return ControlFacts{}, false, err
	}
	return facts, found, nil
}

// ControlRegistry reads a live control lease and the process it names.
type ControlRegistry interface {
	ReadActiveControlLeader(context.Context) (ownership.QueryGroupOwner, bool, error)
	ReadInstance(context.Context, string) (ownership.InstanceRegistration, bool, error)
}

type ControlFactsReader interface {
	LoadControl(context.Context) (ControlFacts, bool, error)
}

// ControlTerm says which lease term wrote a control record.
type ControlTerm string

const (
	// ControlTermCurrent is a record of the active lease's own term, written
	// by the process the lease and its instance record name.
	ControlTermCurrent ControlTerm = "current"
	// ControlTermPrevious is a record of an earlier term: the new Leader has
	// not written yet. It is bounded by the record's own retention.
	ControlTermPrevious ControlTerm = "previous"
)

// ControlFactsReading is a control record and the term that wrote it.
type ControlFactsReading struct {
	Facts ControlFacts
	Term  ControlTerm
}

// ControlFactsSource returns the control record as it stands against the
// active lease. false with no error means a leader registered by an older
// binary, which writes no record; its Worker snapshot carries the facts. An
// error means the record could not be read or attributed; the view then falls
// back to the facts embedded in the snapshots.
type ControlFactsSource interface {
	ControlFacts(context.Context, time.Time) (ControlFactsReading, bool, error)
}

type registeredControlFacts struct {
	registry ControlRegistry
	reader   ControlFactsReader
}

func NewControlFactsSource(registry ControlRegistry, reader ControlFactsReader) (ControlFactsSource, error) {
	if registry == nil || reader == nil {
		return nil, errors.New("alarmd fleet: control source requires registry and facts reader")
	}
	return registeredControlFacts{registry: registry, reader: reader}, nil
}

func (source registeredControlFacts) ControlFacts(ctx context.Context, at time.Time) (ControlFactsReading, bool, error) {
	leader, found, err := source.registry.ReadActiveControlLeader(ctx)
	if err != nil {
		return ControlFactsReading{}, false, err
	}
	if !found {
		return ControlFactsReading{}, false, errors.New("alarmd fleet: active control leader is absent")
	}
	instance, found, err := source.registry.ReadInstance(ctx, leader.OwnerID)
	if err != nil {
		return ControlFactsReading{}, false, err
	}
	if !found {
		return ControlFactsReading{}, false, nil
	}
	if err := instance.Validate(); err != nil {
		return ControlFactsReading{}, false, err
	}
	if instance.InstanceID != leader.OwnerID || !instance.ExpiresAt.After(at) || !instance.ControlCapable() {
		return ControlFactsReading{}, false, errors.New("alarmd fleet: control leader instance is expired or not a control instance")
	}
	facts, found, err := source.reader.LoadControl(ctx)
	if err != nil {
		return ControlFactsReading{}, false, err
	}
	if !found {
		return ControlFactsReading{}, false, errors.New("alarmd fleet: control leader facts are absent")
	}
	if err := facts.Validate(); err != nil {
		return ControlFactsReading{}, false, err
	}
	reading := ControlFactsReading{Facts: facts}
	switch {
	case facts.ControlEpoch == leader.OwnerEpoch:
		if facts.Replica != leader.OwnerID || facts.Incarnation != instance.Incarnation {
			return ControlFactsReading{}, false, errors.New("alarmd fleet: control facts of this term do not match the leader or its instance incarnation")
		}
		reading.Term = ControlTermCurrent
	case facts.ControlEpoch < leader.OwnerEpoch:
		// The new Leader has not written yet: the record is the term
		// before's, read as that and never as this term's.
		reading.Term = ControlTermPrevious
	default:
		return ControlFactsReading{}, false, errors.New("alarmd fleet: control facts carry a later term than the active lease")
	}
	// The read may straddle a takeover. Judge its answer against the lease
	// again so an old owner's record is never attached to a new term.
	current, found, err := source.registry.ReadActiveControlLeader(ctx)
	if err != nil {
		return ControlFactsReading{}, false, err
	}
	if !found || current.OwnerID != leader.OwnerID || current.OwnerEpoch != leader.OwnerEpoch {
		return ControlFactsReading{}, false, errors.New("alarmd fleet: control leader changed while reading facts")
	}
	return reading, true, nil
}

// GapControlFactsUnavailable says neither the control record nor any
// replica's snapshot could supply the Control Leader's facts. It affects the
// verdict without changing any Worker count, execution coverage or capacity
// figure.
const GapControlFactsUnavailable GapKind = "CONTROL_FACTS_UNAVAILABLE"

// Where a view's control facts came from (ControlFactsStanding.Source).
const (
	ControlFactsFromRecord       = "record"
	ControlFactsFromPreviousTerm = "previous_term"
	ControlFactsFromEmbedded     = "embedded"
	ControlFactsFromNone         = "none"
)

// ControlFactsStanding says which source a view's control facts came from,
// and why the current term's record was not it. Absent where no control
// record source is wired.
type ControlFactsStanding struct {
	Source string `json:"source"`
	// Replica is the record's writer, for record and previous_term.
	Replica string `json:"replica,omitempty"`
	// Unavailable is why the current term's record was not used.
	Unavailable string `json:"unavailable,omitempty"`
	// LeaderClass is the class of the Leader that wrote the record used,
	// for record and previous_term: preferred, or fallback while a Worker
	// leads because no control process is there. Empty when the record
	// does not say.
	LeaderClass string `json:"leader_class,omitempty"`
}

type controlRead struct {
	// wired says a control record source is configured; without one the view
	// reads the embedded facts as it always did and says nothing more.
	wired    bool
	current  *ControlFacts
	previous *ControlFacts
	// legacy is a leader registered by an older binary, which writes no record.
	legacy bool
	err    error
}

// SetControlFactsSource is wired once before the service is served. Omitting
// it preserves the embedded control observations of older deployments.
func (service *Service) SetControlFactsSource(source ControlFactsSource) {
	service.control = source
}

func (service *Service) readControl(ctx context.Context, at time.Time) controlRead {
	if service.control == nil {
		return controlRead{}
	}
	reading, found, err := service.control.ControlFacts(ctx, at)
	read := controlRead{wired: true, err: err}
	if err != nil {
		return read
	}
	if !found {
		read.legacy = true
		return read
	}
	facts := reading.Facts
	switch {
	case facts.Validate() != nil:
		read.err = facts.Validate()
	case reading.Term == ControlTermPrevious:
		read.previous = &facts
	case at.Sub(facts.TakenAt) > service.freshness:
		read.err = errors.New("alarmd fleet: control facts are stale")
	default:
		read.current = &facts
	}
	return read
}

// unavailable is why the current term's record was not used, for the view.
func (read controlRead) unavailable() string {
	switch {
	case read.err != nil:
		return gapDetail(read.err)
	case read.previous != nil:
		return "the Control Leader of this term has not written its record yet"
	case read.legacy:
		return "the Control Leader runs a build that writes no separate record"
	}
	return ""
}

// carriesLeaderControlFacts says a snapshot's embedded facts include what only
// a Control Leader fills: the source round, the reconcile round and its
// placement and sweep, or a leading view stream. A follower's own view of the
// control source or its activation does not count, so a split deployment's
// Worker snapshots never stand in for the Leader.
func carriesLeaderControlFacts(facts ControlFacts) bool {
	return facts.Source != nil || facts.Rebalance != nil || facts.AssignmentScope != nil ||
		facts.AssignmentSweep != nil || facts.LeaderRound != nil || (facts.ViewStream != nil && facts.ViewStream.Leading)
}

func controlFactsOf(snapshot Snapshot) ControlFacts {
	return ControlFacts{Replica: snapshot.Replica, TakenAt: snapshot.TakenAt,
		Source: snapshot.Source, Activation: snapshot.Activation, ActivationHeader: snapshot.ActivationHeader, ControlSource: snapshot.ControlSource,
		Rebalance: snapshot.Rebalance, AssignmentScope: snapshot.AssignmentScope, AssignmentSweep: snapshot.AssignmentSweep,
		LeaderRound: snapshot.LeaderRound, ViewStream: snapshot.ViewStream}
}

// mergeControlFacts is shared by embedded legacy facts and independent control
// records, before source standing and health are decided by the aggregate.
func mergeControlFacts(view *View, snapshot ControlFacts) {
	replica := snapshot.Replica
	if snapshot.ControlSource != nil {
		if snapshot.ControlSource.StaleBeyondBound {
			view.Degradations = append(view.Degradations, Degradation{Kind: DegradationControlSourceStale, Replica: replica,
				Stage: snapshot.ControlSource.LastFailureExit, Text: snapshot.ControlSource.LastFailure})
		}
		if snapshot.ControlSource.LeaderAbsentBeyondBound {
			view.Degradations = append(view.Degradations, Degradation{Kind: DegradationControlLeaderAbsent, Replica: replica})
		}
	}
	if snapshot.Activation != nil {
		view.Activation, view.ActivationReplica = snapshot.Activation, replica
		if snapshot.Activation.BehindBeyondBound {
			view.Degradations = append(view.Degradations, Degradation{Kind: DegradationActivationBehind, Replica: replica})
		}
		if snapshot.Activation.BlockedQueryGroups > 0 {
			view.Degradations = append(view.Degradations, Degradation{Kind: DegradationActivationBlocked, Replica: replica,
				Text: fmt.Sprintf("%d held back (%s): %s; timeline keys to delete: %s", snapshot.Activation.BlockedQueryGroups,
					snapshot.Activation.BlockedReasons, snapshot.Activation.BlockedSamples, snapshot.Activation.BlockedKeys)})
		}
	}
	if header := snapshot.ActivationHeader; header != nil {
		age := header.MissingSeconds
		view.Degradations = append(view.Degradations, Degradation{Kind: DegradationActivationHeaderMissing, Replica: replica,
			Text: "last rebuild: " + header.LastRebuild, AgeSeconds: &age})
	}
	if snapshot.Rebalance != nil && (view.Rebalance == nil || snapshot.Rebalance.PlannedAt.After(view.Rebalance.PlannedAt)) {
		facts := *snapshot.Rebalance
		view.Rebalance, view.RebalanceReplica = &facts, replica
	}
	if snapshot.AssignmentScope != nil && (view.AssignmentScope == nil || snapshot.AssignmentScope.At.After(view.AssignmentScope.At)) {
		facts := *snapshot.AssignmentScope
		view.AssignmentScope, view.AssignmentScopeReplica = &facts, replica
	}
	if snapshot.AssignmentSweep != nil && (view.AssignmentSweep == nil || snapshot.AssignmentSweep.At.After(view.AssignmentSweep.At)) {
		facts := *snapshot.AssignmentSweep
		view.AssignmentSweep, view.AssignmentSweepReplica = &facts, replica
	}
	if snapshot.LeaderRound != nil && (view.LeaderRound == nil || snapshot.LeaderRound.At.After(view.LeaderRound.At)) {
		facts := *snapshot.LeaderRound
		view.LeaderRound, view.LeaderRoundReplica = &facts, replica
	}
	if snapshot.ViewStream != nil && snapshot.ViewStream.Leading {
		if snapshot.ViewStream.PublishFailingBeyondBound {
			view.Degradations = append(view.Degradations, Degradation{Kind: DegradationViewPublishFailing, Replica: replica,
				Text: snapshot.ViewStream.PublishFailureReason})
		}
		if snapshot.ViewStream.NoSessionsBeyondBound {
			view.Degradations = append(view.Degradations, Degradation{Kind: DegradationViewStreamNoSessions, Replica: replica})
		}
	}
	if snapshot.ViewStream != nil && viewStreamPreferred(view.ViewStream, snapshot.ViewStream) {
		facts := *snapshot.ViewStream
		facts.Lagging = append(make([]ViewStreamLagging, 0, len(snapshot.ViewStream.Lagging)), snapshot.ViewStream.Lagging...)
		view.ViewStream, view.ViewStreamReplica = &facts, replica
	}
	if snapshot.Source != nil && (view.Source == nil || snapshot.Source.At.After(view.Source.At)) {
		facts := *snapshot.Source
		view.Source, view.SourceReplica = &facts, replica
	}
}
