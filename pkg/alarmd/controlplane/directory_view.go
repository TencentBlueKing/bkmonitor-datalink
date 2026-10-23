// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// DirectoryView answers the strategy directory from what the control Leader
// already holds: the catalog it published (the strategy index its rounds
// build), the activation its repository has parsed for the version it runs,
// and whatever content of older publications the activation round already
// holds. It keeps no copy of its own, refreshes nothing, and writes nothing
// the Slots read from: a request reads the activation header, and the
// objects of the rows it returns, and no manifest.
//
// Only the Leader answers. A follower, a Leader before its first completed
// round, and a former Leader after it stepped down hold no publication of
// their own: Available is false there, and the reader is sent to the
// Leader rather than answered from a copy every replica would have to keep.
//
// Where a row's content comes from. An activation record sits on the
// publication its Query Group's Segment opened on, and stays there while
// the Segment does - through cutovers that change the Plan's detection and
// not its schedule - while the content the Plan runs follows every cutover,
// through its Query Group's content scope. So a Plan runs the activation's
// current publication's content, wherever its record sits. An activation
// holds one record per Plan (validateActivationState refuses a key twice);
// a draining Query Group is named in the activation's Draining list, not by
// a second record. A PENDING record, which nothing produces today, would run
// the publication it sits on. The content of the publication the
// Leader made is the catalog: a Plan whose content is that publication's is
// the catalog's row, carrying its activation. Another publication's content
// is what the activation round holds: from there, the revisions from its
// object; otherwise the activation alone, marked ContentNotHeld - the Plan,
// its publication and selection, nothing guessed about the rest. The
// published catalog never stands in for another publication's content: a
// Plan current on one publication while a later one is published runs the
// current one's, and the later one can differ from it under every field an
// activation record carries.
type DirectoryView struct {
	reconciler *SourceReconciler
	repository *RedisCatalogRepository
	timeout    time.Duration
}

// NewDirectoryView builds the view over one process's control plane. The
// timeout bounds one request: its reads and the rows it builds.
func NewDirectoryView(reconciler *SourceReconciler, repository *RedisCatalogRepository, timeout time.Duration) (*DirectoryView, error) {
	if reconciler == nil || repository == nil || timeout <= 0 {
		return nil, errors.New("alarmd controlplane: a directory view needs the reconciler, the repository and a timeout")
	}
	return &DirectoryView{reconciler: reconciler, repository: repository, timeout: timeout}, nil
}

// Available says this process holds a publication of its own to answer
// from: it leads and has completed a round.
func (view *DirectoryView) Available() bool {
	return view != nil && view.reconciler.publishedIndex() != nil
}

// directoryAnswer is what one request reads, once, and the indexes every row
// of it is built from.
type directoryAnswer struct {
	snapshot StrategyDirectorySnapshot
	index    *strategyIndex
	// records is every Plan the activation carries, by identity; current the
	// activation's current publication; carried the publications other than
	// the published one whose content a row may be named from.
	records map[execution.PlanIdentity][]PlanActivationRecord
	current SnapshotPublicationRef
	carried []SnapshotPublicationRef
	// contexts is the published content's output context naming, and held
	// the carried publications whose content the activation round holds.
	contexts map[execution.PlanIdentity]execution.OutputContextDigest
	held     map[SnapshotPublicationRef]heldPublication
	// objects are the Query Group objects read for this answer's rows.
	objects map[execution.ObjectDigest]QueryGroupObject
}

// heldPublication is a carried publication's content as a page reads it: by
// Plan identity, where each Plan sits, and its output contexts.
type heldPublication struct {
	plans    map[execution.PlanIdentity][]heldPlan
	contexts map[execution.PlanIdentity]execution.OutputContextDigest
}

type heldPlan struct {
	key    execution.PlanKey
	group  execution.QueryGroupIdentity
	digest execution.ObjectDigest
}

func holdPublication(content PublishedContent) heldPublication {
	held := heldPublication{plans: map[execution.PlanIdentity][]heldPlan{}, contexts: contextRefsOf(content)}
	for group, entry := range content.Groups {
		for _, key := range entry.Plans {
			held.plans[key.PlanIdentity] = append(held.plans[key.PlanIdentity], heldPlan{key: key, group: group, digest: entry.Digest})
		}
	}
	return held
}

// answer reads what every row of this request is built from: the activation
// header and nothing else. It fails only when the view has nothing to answer
// with.
func (view *DirectoryView) answer(ctx context.Context, at time.Time) (*directoryAnswer, error) {
	index := view.reconciler.publishedIndex()
	if index == nil {
		return nil, ErrSnapshotUnavailable
	}
	a := &directoryAnswer{index: index, records: map[execution.PlanIdentity][]PlanActivationRecord{},
		held: map[SnapshotPublicationRef]heldPublication{}, objects: map[execution.ObjectDigest]QueryGroupObject{}}
	s := &a.snapshot
	s.ObservedAt, s.Published, s.SourceObservation = at, index.publication, index.observation
	s.GroupsTotal, s.GroupsKnown, s.Rows = len(index.groups), len(index.groups), []StrategyDirectoryRow{}
	activation, err := view.repository.LoadActivation(ctx)
	if err != nil {
		view.fail(s, "activation", "", nil, err)
		return a, nil
	}
	if validateActivationState(activation) != nil {
		s.Reason = "INVALID_ACTIVATION"
		return a, nil
	}
	s.Current, s.ActivationRevision = activation.Current, activation.RecordRevision
	a.current = activation.Current
	s.Revision = fmt.Sprintf("%s:%d:%d", index.publication.SnapshotRevision, index.publication.PublicationEpoch, activation.RecordRevision)
	counts := map[SnapshotPublicationRef]int{}
	for _, record := range activation.Plans {
		a.records[record.Fact.Plan] = append(a.records[record.Fact.Plan], record)
		counts[record.Publication]++
	}
	// The publications a row's content may come from besides the published
	// one: the current one, and each a record sits on (a PENDING one's).
	named := map[SnapshotPublicationRef]bool{index.publication: true}
	for _, publication := range append([]SnapshotPublicationRef{activation.Current}, recordPublications(activation.Plans)...) {
		if !named[publication] && publication.validate() == nil {
			named[publication] = true
			a.carried = append(a.carried, publication)
		}
	}
	s.Complete = true
	a.contexts = view.repository.rememberedContextRefs(index.publication)
	s.Publications = append(s.Publications, DirectoryPublication{Publication: index.publication, Plans: counts[index.publication], Manifest: "memory"})
	for _, publication := range a.carried {
		read := DirectoryPublication{Publication: publication, Plans: counts[publication], Manifest: "not_held"}
		// Only what the activation round already holds. A page reads no
		// manifest and puts nothing in the memo the Slots read from.
		if content, held := view.repository.contentMemo.lookup(publication); held {
			read.Manifest = "memory"
			a.held[publication] = holdPublication(content)
		}
		s.Publications = append(s.Publications, read)
	}
	return a, nil
}

// recordPublications is each publication a record sits on, in record order.
func recordPublications(records []PlanActivationRecord) []SnapshotPublicationRef {
	publications := make([]SnapshotPublicationRef, 0, len(records))
	for _, record := range records {
		publications = append(publications, record.Publication)
	}
	return publications
}

// contentOf is the publication a record's Plan runs the content of: the
// current one, wherever the record sits; a PENDING record's own.
func (a *directoryAnswer) contentOf(record PlanActivationRecord) SnapshotPublicationRef {
	if record.Fact.Selection == execution.ActivationPending {
		return record.Publication
	}
	return a.current
}

// fail records the first read that failed and why.
func (view *DirectoryView) fail(s *StrategyDirectorySnapshot, step, key string, publication *SnapshotPublicationRef, err error) {
	s.Complete = false
	s.Reason = "DEPENDENCY_UNAVAILABLE"
	if s.FailedRead == "" && err != nil {
		s.FailedRead, s.Error, s.FailedKey, s.FailedPublication = step, observability.SanitizeErrorText(err.Error()), key, publication
	}
}

// rowDraft is a row before what only a returned row pays for: the published
// group's object digest, derived from the group, and a held group's
// revisions, read from its object.
type rowDraft struct {
	row    StrategyDirectoryRow
	group  *QueryGroup
	object execution.ObjectDigest
}

// drafts is every row of one Plan identity, sorted as a page reads them - by
// Query Group, then by publication, then by shard. Nothing is read.
func (a *directoryAnswer) drafts(identity execution.PlanIdentity) []rowDraft {
	var drafts []rowDraft
	published := a.index.publication
	records := a.records[identity]
	// A record whose content is the published catalog's is that catalog
	// row's activation, not a row of its own.
	onCatalog := make([]bool, len(records))
	for _, at := range a.index.plans[identity.StrategyID] {
		group := &a.index.groups[at.group]
		plan := &group.Plans[at.plan]
		if plan.Identity != identity {
			continue
		}
		row := StrategyDirectoryRow{Identity: plan.Identity, QueryGroup: group.Identity, Publication: published, Role: "PUBLISHED",
			QueryRevision: group.QueryPlan.QueryRevision, ScheduleRevision: group.ScheduleRevision, OutputContext: a.contexts[plan.Identity]}
		for index, record := range records {
			if onCatalog[index] || record.Fact.Key() != plan.Key() || a.contentOf(record) != published {
				continue
			}
			onCatalog[index] = true
			fact := record.Fact
			row.Activation, row.Role = &fact, string(fact.Selection)
			if record.Publication != published {
				activatedOn := record.Publication
				row.ActivatedOn = &activatedOn
			}
			break
		}
		drafts = append(drafts, rowDraft{row: row, group: group})
	}
	for index, record := range records {
		if onCatalog[index] {
			continue
		}
		fact := record.Fact
		content := a.contentOf(record)
		row := StrategyDirectoryRow{Identity: fact.Plan, Publication: content, Role: string(fact.Selection), Activation: &fact}
		if record.Publication != content {
			activatedOn := record.Publication
			row.ActivatedOn = &activatedOn
		}
		if held, holds := a.held[content]; holds {
			if plan, found := held.find(fact.Key()); found {
				row.QueryGroup, row.ObjectDigest, row.OutputContext = plan.group, plan.digest, held.contexts[fact.Plan]
				drafts = append(drafts, rowDraft{row: row, object: plan.digest})
				continue
			}
		}
		row.ContentNotHeld = true
		drafts = append(drafts, rowDraft{row: row})
	}
	sort.SliceStable(drafts, func(i, j int) bool {
		left, right := drafts[i].row, drafts[j].row
		if left.QueryGroup != right.QueryGroup {
			return left.QueryGroup < right.QueryGroup
		}
		if left.Publication.PublicationEpoch != right.Publication.PublicationEpoch {
			return left.Publication.PublicationEpoch < right.Publication.PublicationEpoch
		}
		return shardOf(left) < shardOf(right)
	})
	return drafts
}

func (held heldPublication) find(key execution.PlanKey) (heldPlan, bool) {
	for _, plan := range held.plans[key.PlanIdentity] {
		if plan.key == key {
			return plan, true
		}
	}
	return heldPlan{}, false
}

// finish builds a returned row: the published group's digest, a held
// group's revisions from its object.
func (view *DirectoryView) finish(ctx context.Context, a *directoryAnswer, s *StrategyDirectorySnapshot, draft rowDraft) StrategyDirectoryRow {
	row := draft.row
	if draft.group != nil {
		if digest, err := DeriveQueryGroupObjectDigest(*draft.group); err == nil {
			row.ObjectDigest = digest
		}
	}
	if draft.object != "" {
		object, err := view.object(ctx, a, draft.object)
		if err != nil {
			// The row is named; the revisions only its object holds are left
			// empty rather than guessed, and the answer says so.
			failed := row.Publication
			view.fail(s, "group_object", view.repository.queryGroupObjectKey(draft.object), &failed, err)
		} else {
			row.QueryRevision, row.ScheduleRevision = object.QueryPlan.QueryRevision, object.ScheduleRevision
		}
	}
	return row
}

func shardOf(row StrategyDirectoryRow) int {
	if row.Activation == nil {
		return 0
	}
	return row.Activation.Key().ShardIndex
}

// contextRefsOf is a publication's Plan -> output context naming.
func contextRefsOf(content PublishedContent) map[execution.PlanIdentity]execution.OutputContextDigest {
	contexts := make(map[execution.PlanIdentity]execution.OutputContextDigest)
	for _, entry := range content.Groups {
		for _, ref := range entry.Refs {
			contexts[ref.Plan] = ref.Digest
		}
	}
	return contexts
}

// object reads one Query Group object once per answer.
func (view *DirectoryView) object(ctx context.Context, a *directoryAnswer, digest execution.ObjectDigest) (QueryGroupObject, error) {
	if object, read := a.objects[digest]; read {
		return object, nil
	}
	object, err := view.repository.LoadQueryGroupObject(ctx, digest)
	if err != nil {
		return QueryGroupObject{}, err
	}
	a.objects[digest] = object
	return object, nil
}

// identities is the Plan identities this answer lists: every one of a
// strategy, or every one the catalog and the activation name, in the order
// a page reads them.
func (a *directoryAnswer) identities(strategy string) []execution.PlanIdentity {
	seen := map[execution.PlanIdentity]bool{}
	var out []execution.PlanIdentity
	add := func(identity execution.PlanIdentity) {
		if !seen[identity] {
			seen[identity] = true
			out = append(out, identity)
		}
	}
	if strategy != "" {
		for _, at := range a.index.plans[strategy] {
			add(a.index.groups[at.group].Plans[at.plan].Identity)
		}
	} else {
		for _, identity := range a.index.allIdentities() {
			add(identity)
		}
	}
	// A Plan active only on a carried publication - its strategy left the
	// catalog's current Query Group, or the catalog no longer has it - is
	// still a row.
	for identity, records := range a.records {
		if strategy != "" && identity.StrategyID != strategy {
			continue
		}
		for _, record := range records {
			if record.Publication != a.index.publication {
				add(identity)
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return lessPlanIdentity(out[i], out[j]) })
	return out
}

// Page answers one page of the directory: the rows of one strategy, or of
// every strategy, from offset, at most limit of them. The rows before the
// page cost a walk of the indexes and nothing else; the page's own rows
// derive their digests and read their objects. The source dispositions of the
// strategy asked for come with it; they carry no tenant and are not joined to
// a Plan. A page that runs past the request's time says so and stops.
func (view *DirectoryView) Page(ctx context.Context, at time.Time, tenant, business, strategy string, offset, limit int) StrategyDirectorySnapshot {
	ctx, cancel := context.WithTimeout(redisfailure.WithCaller(ctx, redisfailure.CallerDirectoryRead), view.timeout)
	defer cancel()
	a, err := view.answer(ctx, at)
	if err != nil {
		return StrategyDirectorySnapshot{ObservedAt: at, Reason: "LEADER_CATALOG_NOT_READY", Rows: []StrategyDirectoryRow{}}
	}
	s := a.snapshot
	s.SourceComplete = true
	for _, disposition := range a.index.dispositions[strategy] {
		if strategy == "" {
			break
		}
		s.SourceMatchedTotal++
		if len(s.Unattributed) < limit {
			s.Unattributed = append(s.Unattributed, disposition)
		}
	}
	s.SourceTruncated = s.SourceMatchedTotal > len(s.Unattributed)
	for _, identity := range a.identities(strategy) {
		if ctx.Err() != nil {
			s.Complete, s.Reason = false, "DEADLINE"
			return s
		}
		if tenant != "" && identity.TenantID != tenant || business != "" && identity.BusinessID != business {
			continue
		}
		for _, draft := range a.drafts(identity) {
			if offset > 0 {
				offset--
				continue
			}
			if len(s.Rows) >= limit {
				return s
			}
			s.Rows = append(s.Rows, view.finish(ctx, a, &s, draft))
		}
	}
	return s
}

// ResolveCurrent is the one row of a strategy running under the current
// activation, narrowed by tenant, business and Query Group where given. A
// strategy whose Plans name two identities, or with two current rows, is
// ambiguous; one with none is unavailable - as is one whose current row's
// content is not held, which names no Query Group. expectedRevision, when
// given, holds the answer to the revision a page was read at.
func (view *DirectoryView) ResolveCurrent(ctx context.Context, at time.Time, tenant, business, strategy, group string,
	expectedRevision ...string) (StrategyDirectoryRow, error) {
	ctx, cancel := context.WithTimeout(redisfailure.WithCaller(ctx, redisfailure.CallerDirectoryRead), view.timeout)
	defer cancel()
	a, err := view.answer(ctx, at)
	if err != nil {
		return StrategyDirectoryRow{}, err
	}
	if len(expectedRevision) > 0 && expectedRevision[0] != a.snapshot.Revision {
		return StrategyDirectoryRow{}, ErrObservationChanged
	}
	if a.snapshot.Revision == "" {
		return StrategyDirectoryRow{}, ErrSnapshotUnavailable
	}
	var identity execution.PlanIdentity
	var selected *rowDraft
	for _, candidate := range a.identities(strategy) {
		if tenant != "" && candidate.TenantID != tenant || business != "" && candidate.BusinessID != business {
			continue
		}
		drafts := a.drafts(candidate)
		for index := range drafts {
			row := drafts[index].row
			if identity != (execution.PlanIdentity{}) && identity != row.Identity {
				return StrategyDirectoryRow{}, ErrObservationAmbiguous
			}
			identity = row.Identity
			if group != "" && string(row.QueryGroup) != group || row.Role != string(execution.ActivationCurrent) || row.QueryGroup == "" {
				continue
			}
			if selected != nil {
				return StrategyDirectoryRow{}, ErrObservationAmbiguous
			}
			selected = &drafts[index]
		}
	}
	if selected == nil {
		return StrategyDirectoryRow{}, ErrSnapshotUnavailable
	}
	return view.finish(ctx, a, &a.snapshot, *selected), nil
}

// EffectivePlan is the Plan a row names, read from its Query Group object:
// the process's object cache first, the store otherwise. The object must be
// the row's Query Group and carry the Plan under the revisions the row's
// activation selected.
func (view *DirectoryView) EffectivePlan(ctx context.Context, row StrategyDirectoryRow) (QueryGroupPlanObject, error) {
	ctx, cancel := context.WithTimeout(redisfailure.WithCaller(ctx, redisfailure.CallerDirectoryRead), view.timeout)
	defer cancel()
	object, err := view.repository.LoadQueryGroupObject(ctx, row.ObjectDigest)
	if err != nil {
		return QueryGroupPlanObject{}, err
	}
	if object.Identity != row.QueryGroup {
		return QueryGroupPlanObject{}, ErrCatalogObjectCorrupt
	}
	for _, plan := range object.Plans {
		if plan.Identity == row.Identity {
			// Older objects omit the generation and let activation compilation
			// derive it. Never invent that field in the stored configuration.
			if row.Activation == nil || plan.StateGeneration != "" && plan.StateGeneration != row.Activation.Selected.StateGeneration ||
				plan.ScheduleRevision != row.Activation.Selected.ScheduleRevision {
				return QueryGroupPlanObject{}, ErrCatalogObjectCorrupt
			}
			return plan, nil
		}
	}
	return QueryGroupPlanObject{}, ErrCatalogObjectUnavailable
}

// EffectiveOutput reads the output context a row names and says what the
// Plan publishes as: one immutable object, verified against the digest that
// names it, from the object cache first. A row with no digest is answered
// as not retained.
func (view *DirectoryView) EffectiveOutput(ctx context.Context, row StrategyDirectoryRow) OutputFormatFacts {
	if row.OutputContext == "" {
		return OutputFormatFacts{Reason: OutputContextRefNotRetained}
	}
	ctx, cancel := context.WithTimeout(redisfailure.WithCaller(ctx, redisfailure.CallerDirectoryRead), view.timeout)
	defer cancel()
	contexts, err := view.repository.loadOutputContexts(ctx, []execution.OutputContextRef{{Plan: row.Identity, Digest: row.OutputContext}})
	switch {
	case errors.Is(err, ErrCatalogObjectUnavailable):
		return OutputFormatFacts{Reason: OutputContextUnavailable, OutputContextDigest: row.OutputContext}
	case errors.Is(err, ErrCatalogObjectCorrupt) || errors.Is(err, ErrCatalogObjectContractNewer):
		return OutputFormatFacts{Reason: OutputContextCorrupt, OutputContextDigest: row.OutputContext}
	case err != nil:
		return OutputFormatFacts{Reason: OutputContextDependency, OutputContextDigest: row.OutputContext}
	}
	object := contexts[row.OutputContext]
	if object.Identity != row.Identity {
		return OutputFormatFacts{Reason: OutputContextCorrupt, OutputContextDigest: row.OutputContext}
	}
	format, decidedBy := EffectiveWireFormat(object.WireFormat, object.StrategyRef.SnapshotRevision)
	return OutputFormatFacts{
		Known: true, OutputContextDigest: row.OutputContext,
		WireFormat: object.WireFormat, EffectiveWireFormat: format, DecidedBy: decidedBy,
		SnapshotRevision: object.StrategyRef.SnapshotRevision, SignalType: object.SignalType,
		CompatibilityContext: object.LegacyOutput != nil,
	}
}
