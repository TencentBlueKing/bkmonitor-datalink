package main

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisbatch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// readHoldTimeline is what the holds read of a group's own schedule
// timeline.
type readHoldTimeline interface {
	ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
}

// readHoldCatalog is what the holds read of the catalog: the group's object,
// for its route and delay, and the links its moved Plans left.
type readHoldCatalog interface {
	LoadQueryGroupObject(context.Context, execution.ObjectDigest) (controlplane.QueryGroupObject, error)
	ReadHoldPredecessors(context.Context, execution.FrozenQueryGroupSchedule) ([]controlplane.ReadHoldPredecessor, map[string]int, error)
}

// Read holds share the ownership store and schedule timeline. Nothing here
// changes a Plan's state generation or the window its Slot reads.
type productionReadHolds struct {
	controller  *readhold.Controller
	cfg         config.Config
	repository  readHoldCatalog
	catalog     readHoldTimeline
	progress    productionPhaseTwoProgressReader
	now         func() time.Time
	logger      *observability.Logger
	mu          sync.Mutex
	groups      map[execution.QueryGroupIdentity]*productionReadHoldGroup
	nextRenew   time.Time
	transitions atomic.Uint64
	overtaken   atomic.Uint64
	// links counts the predecessor links a prepare skipped, by why
	// (self_link, invalid_link, expired); retireCloseFailed the retired
	// groups whose closing failed and that retired all the same.
	linksMu           sync.Mutex
	links             map[string]uint64
	retireCloseFailed atomic.Uint64
	// closeSkipped is the previous Segments a prepare could not close
	// because the record is already past them without their closing facts.
	closeSkipped atomic.Uint64
	// degraded counts the Slots frozen with the hold their group last read,
	// by what failed (readhold.DegradedReasons).
	degradedMu sync.Mutex
	degraded   map[string]uint64
}

type productionReadHoldGroup struct {
	mu             sync.Mutex
	session        *ownership.Session
	prepared       execution.ScheduleSegmentFact
	lastTransition execution.EvaluationTime
	predecessors   []execution.QueryGroupIdentity
	queryRoute     string
	queryDelay     time.Duration
	// queryStep is the query's data step, the one the lookback aligns its
	// suggestion to, and settlingWait the newest prepared Segment's spec's,
	// cached with the delay for the time_delay a measured hold suggests
	// (fleetFacts); settled says a Segment was prepared.
	queryStep time.Duration
	// queryUnaligned is a query read from where its request starts, a Plan
	// detected more often than it aggregates; delayUnit is what its delay
	// was rounded to, the data step or, for such a query, its Plans'
	// shortest step when shorter.
	queryUnaligned bool
	delayUnit      time.Duration
	settlingWait   time.Duration
	settled        bool
	degradedLogged readHoldDegradedLine
}

// readHoldDegradedLine is the Segment and reason a group's degraded hold was
// last logged for.
type readHoldDegradedLine struct {
	start  execution.EvaluationTime
	reason string
}

func newProductionReadHolds(cfg config.Config, control readhold.Control, repository readHoldCatalog,
	catalog readHoldTimeline, progress productionPhaseTwoProgressReader, now func() time.Time,
	logger *observability.Logger) (*productionReadHolds, error) {
	holds := &productionReadHolds{cfg: cfg, repository: repository, catalog: catalog, progress: progress,
		now: now, logger: logger, groups: make(map[execution.QueryGroupIdentity]*productionReadHoldGroup), links: make(map[string]uint64),
		degraded: make(map[string]uint64)}
	var err error
	holds.controller, err = readhold.NewController(readhold.Options{Control: control,
		Prefix: productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"), MaxHold: cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration(),
		Now: now, Owner: holds.owner})
	return holds, err
}

func (holds *productionReadHolds) owner(qg execution.QueryGroupIdentity) (readhold.Owner, error) {
	holds.mu.Lock()
	group := holds.groups[qg]
	var session *ownership.Session
	if group != nil {
		session = group.session
	}
	holds.mu.Unlock()
	lease, accepting := session.Current()
	if !accepting || !lease.Deadline.After(holds.now()) {
		return readhold.Owner{}, ownership.ErrStaleFence
	}
	return readhold.Owner{Fence: lease.Fence, ContentScope: lease.ContentScope}, nil
}

func (holds *productionReadHolds) bind(qg execution.QueryGroupIdentity, session *ownership.Session) {
	holds.mu.Lock()
	defer holds.mu.Unlock()
	holds.groups[qg] = &productionReadHoldGroup{session: session}
}

func (holds *productionReadHolds) forget(qg execution.QueryGroupIdentity) {
	holds.mu.Lock()
	delete(holds.groups, qg)
	holds.mu.Unlock()
	holds.controller.Forget(qg)
}

// An inherited bridge is already in the owned fenced record. Foreign
// snapshots are only needed while seeding and have no runner to release them.
func (holds *productionReadHolds) releasePredecessors(group *productionReadHoldGroup) {
	holds.mu.Lock()
	defer holds.mu.Unlock()
	for _, qg := range group.predecessors {
		if holds.groups[qg] == nil {
			holds.controller.Forget(qg)
		}
	}
	group.predecessors = nil
}

// Restore only new or invalidated entries. One bad answer leaves that group
// retryable without discarding successfully loaded siblings.
func (holds *productionReadHolds) restore(ctx context.Context, groups []execution.QueryGroupIdentity) error {
	var missing []execution.QueryGroupIdentity
	for _, qg := range groups {
		if !holds.controller.Inspect(qg).Loaded {
			missing = append(missing, qg)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return holds.controller.RestoreBatch(ctx, missing)
}

func readHoldRoute(facts execution.QueryPlanFacts) (string, error) {
	facts.QueryDelaySeconds, facts.QueryRevision = 0, ""
	return contract.DeriveCanonicalDigestV2("alarmd-read-hold-route-v1", facts)
}

// Cache only the immutable source route and delay, not another query body.
// Both are in the Query Group object the Segment names, read alone: the
// Plans' output contexts and the publication's manifest are what a Slot
// renders with, and a Segment whose output contexts were revised outlives
// its first contexts and its first manifest. Reading them here refused every
// Slot of such a Segment once they had passed their retention.
func (holds *productionReadHolds) queryBasis(ctx context.Context, schedule execution.FrozenQueryGroupSchedule) (string, time.Duration, time.Duration, error) {
	qg := schedule.Segment.QueryGroup
	holds.mu.Lock()
	owned := holds.groups[qg]
	var route string
	var delay, step time.Duration
	if owned != nil {
		route, delay, step = owned.queryRoute, owned.queryDelay, owned.queryStep
	}
	holds.mu.Unlock()
	if route != "" {
		return route, delay, step, nil
	}
	// A Segment without the object -- one named by no digest, or pruned --
	// gives no route: the hold is degraded (spec_unreadable), not stopped.
	err := controlplane.ErrCatalogObjectUnavailable
	var object controlplane.QueryGroupObject
	if schedule.Segment.ObjectDigest != "" {
		object, err = holds.repository.LoadQueryGroupObject(ctx, schedule.Segment.ObjectDigest)
		if err == nil && object.Identity != qg {
			err = errors.New("alarmd readhold: Query Group object belongs to another Query Group")
		}
	}
	if err != nil {
		return "", 0, 0, err
	}
	route, err = readHoldRoute(object.QueryPlan)
	if err != nil {
		return "", 0, 0, err
	}
	delay = time.Duration(object.QueryPlan.QueryDelaySeconds) * time.Second
	step = time.Duration(object.QueryPlan.StepMillis) * time.Millisecond
	holds.mu.Lock()
	if owned != nil && holds.groups[qg] == owned {
		owned.queryRoute, owned.queryDelay, owned.queryStep = route, delay, step
		owned.queryUnaligned = object.QueryPlan.NotTimeAlign
	}
	holds.mu.Unlock()
	return route, delay, step, nil
}

func (holds *productionReadHolds) spec(ctx context.Context, schedule execution.FrozenQueryGroupSchedule) (readhold.GroupSpec, error) {
	route, delay, step, err := holds.queryBasis(ctx, schedule)
	if err != nil {
		return readhold.GroupSpec{}, err
	}
	// The step a hold is lowered by is the query's data step, the one the
	// early read lowers by: the two compare their candidates and drop the
	// evidence when they differ, so a Plan detected more often than it
	// aggregates, whose schedule is shorter than its data step, would never
	// have its hold lowered if this were the schedule's.
	spec := readhold.GroupSpec{QueryGroup: schedule.Segment.QueryGroup, Delay: delay, HoldLimit: holds.holdLimit(schedule), Step: step}
	for _, plan := range schedule.Plans {
		spec.Plans = append(spec.Plans, readhold.PlanRef{Key: plan.Key(), Route: route})
		offset := time.Duration(plan.Spec.CompletionOffsetSeconds()) * time.Second
		wait := execution.SettlingWaitWithinQueryBudget(offset-holds.cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration(), holds.cfg.PhaseTwo.Access.MinReadyDelay.Duration())
		if len(spec.Plans) == 1 || wait < spec.SettlingWait {
			spec.SettlingWait = wait
		}
	}
	return spec, nil
}

// holdLimit is the most a Slot of the schedule may be held: the replay age,
// within what each Plan's Snapshot retention leaves. It reads nothing.
func (holds *productionReadHolds) holdLimit(schedule execution.FrozenQueryGroupSchedule) time.Duration {
	limit := holds.cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration()
	for _, plan := range schedule.Plans {
		offset := time.Duration(plan.Spec.CompletionOffsetSeconds()) * time.Second
		margin := phaseTwoObjectRetentionLimit(holds.cfg) - phaseTwoSnapshotMinimumRetention(holds.cfg, offset)
		limit = min(limit, max(margin, 0))
	}
	return limit
}

// readHoldDegradation is a hold its group could not prepare or write for a
// reason of the hold's own. It is scheduler.ErrReadHoldDegraded, so the
// Slot is not refused for it, and still the cause, so the retirement and
// the lookback paths, which do not freeze a Slot, see what failed.
type readHoldDegradation struct {
	reason string
	err    error
}

func (degradation *readHoldDegradation) Error() string {
	return "alarmd readhold: degraded (" + degradation.reason + "): " + degradation.err.Error()
}

func (degradation *readHoldDegradation) Unwrap() []error {
	return []error{scheduler.ErrReadHoldDegraded, degradation.err}
}

// degraded is err as a degradation of the hold for reason, unless it says
// the group is not this worker's or its record is unread: those refuse the
// Slot as before -- a lease lost, or one read of the record on the Redis the
// Slot needs all the same, retried at the next Slot.
func degraded(reason string, err error) error {
	if err == nil || errors.Is(err, ownership.ErrStaleFence) || errors.Is(err, ownership.ErrContentScopeMoved) ||
		errors.Is(err, readhold.ErrNotRestored) || errors.Is(err, scheduler.ErrReadHoldDegraded) {
		return err
	}
	return &readHoldDegradation{reason: reason, err: err}
}

// PrepareSchedule is also called for unfinished Slots: their h is reused,
// but a closed segment still must fix its Plan bridge before the successor.
// What fails here for a reason of the hold's own is a degradation (degraded):
// the Slot is frozen with the hold the group last read, never refused.
func (holds *productionReadHolds) PrepareSchedule(ctx context.Context, schedule execution.FrozenQueryGroupSchedule, fence execution.OwnerFence) (prepareErr error) {
	qg := schedule.Segment.QueryGroup
	holds.mu.Lock()
	group := holds.groups[qg]
	holds.mu.Unlock()
	if group == nil {
		return ownership.ErrStaleFence
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if lease, accepting := group.session.Current(); accepting && lease.TimelineRecordRevision > 0 {
		ctx = controlplane.WithTimelineRevisionHint(ctx, lease.TimelineRecordRevision)
	}
	defer func() {
		if prepareErr != nil {
			holds.releasePredecessors(group)
		}
	}()
	restoreErr := holds.restore(ctx, []execution.QueryGroupIdentity{qg})
	answered := redisbatch.Answered(restoreErr)
	if !answered {
		holds.report("restore_failed", qg, restoreErr)
	}
	if !holds.controller.Inspect(qg).Loaded {
		if answered {
			// Redis answered the group's record with an error of its own
			// (WRONGTYPE, LOADING, ...), and answers every read of it so
			// while that lasts: refused, the group stopped as long. The Slot
			// goes on with the hold last read; the record is read again at
			// the next Slot, and the degradation logs once (countDegraded).
			return degraded(readhold.DegradedRecordUnreadable, restoreErr)
		}
		return readhold.ErrNotRestored
	}
	if reflect.DeepEqual(group.prepared, schedule.Segment) {
		return nil
	}
	// The route and delay are the Query Group's, whichever Segment: taken
	// from this Segment first, they are cached for the previous one, whose
	// content is then never read -- content past its retention or corrupt
	// would otherwise pause the group until a content edit pruned it.
	spec, err := holds.spec(ctx, schedule)
	if err != nil {
		return degraded(readhold.DegradedSpecUnreadable, err)
	}
	// Close our previous segment first, even when the QG still carries other
	// Plans. A cutover may reach this path without a final old-Slot execution.
	if schedule.Segment.Start > 1 && schedule.Segment.Start > group.prepared.Start {
		old, err := holds.catalog.ReadFrozenSchedule(ctx, qg, schedule.Segment.Start-1)
		if err == nil && old.Segment.End != nil {
			oldSpec, err := holds.spec(ctx, old)
			if err != nil {
				return degraded(readhold.DegradedSpecUnreadable, err)
			}
			if err := holds.controller.Configure(oldSpec); err != nil {
				return degraded(readhold.DegradedSpecRejected, err)
			}
			if err := holds.controller.CloseSchedule(ctx, old, fence); errors.Is(err, readhold.ErrSegmentStale) {
				// The record is past this Segment without its closing facts:
				// a departed Plan's closure the record dropped after a week,
				// while the Segment is still retained. Nothing can rebuild
				// them, and waiting for them waited until a content edit
				// pruned the Segment -- days. A successor reads that boundary
				// as open, which is the hold bound; the group goes on.
				holds.closeSkipped.Add(1)
				holds.report("close_previous_stale", qg, err)
			} else if err != nil {
				return degraded(readhold.DegradedCloseFailed, err)
			}
		} else if err != nil && !errors.Is(err, controlplane.ErrScheduleUnavailable) {
			return degraded(readhold.DegradedPreviousUnreadable, err)
		}
	}
	// Late observations of an older closed segment cannot reconfigure a
	// newer live group with the old segment's delay or Plans.
	if schedule.Segment.Start < group.prepared.Start {
		err := readhold.ErrSegmentStale
		if schedule.Segment.End != nil {
			err = holds.controller.CloseSchedule(ctx, schedule, fence)
		}
		if errors.Is(err, readhold.ErrSegmentStale) {
			return degraded(readhold.DegradedStaleSegment, err)
		}
		return degraded(readhold.DegradedCloseFailed, err)
	}
	links, skipped, err := holds.repository.ReadHoldPredecessors(ctx, schedule)
	if err != nil {
		return degraded(readhold.DegradedPredecessorsUnreadable, err)
	}
	holds.countLinks(skipped)
	// Nothing about a predecessor can stop this group: the controller reads
	// only the old group's record (previousHold), and what it cannot read is
	// the hold bound, which costs this group's first few Slots a later read
	// and no Slot.
	expired := execution.EvaluationTime(holds.now().Add(-controlplane.ReadHoldLinkLifetime).Unix())
	record := holds.controller.Inspect(qg).Record
	for _, link := range links {
		if link.ClosedAt < expired {
			holds.countLinks(map[string]int{"expired": 1})
			continue
		}
		wanted := holds.linkedPlans(spec, link)
		if len(wanted) == 0 || inheritedLinks(record, link, wanted) {
			// Once the fenced record carries this bridge, the old group's
			// facts may expire normally. A new owner restores the bridge.
			continue
		}
		group.predecessors = append(group.predecessors, link.QueryGroup)
		// A read that fails, or a record that does not decode, leaves the
		// Inspection saying so; neither is this group's error.
		holds.report("predecessor_unreadable", link.QueryGroup, holds.controller.RestoreBatch(ctx, []execution.QueryGroupIdentity{link.QueryGroup}))
		spec.Previous = append(spec.Previous, readhold.Previous{QueryGroup: link.QueryGroup, ClosedAt: link.ClosedAt, Links: wanted})
	}
	if err := holds.controller.Configure(spec); err != nil {
		return degraded(readhold.DegradedSpecRejected, err)
	}
	if schedule.Segment.End != nil {
		if err := holds.controller.CloseSchedule(ctx, schedule, fence); errors.Is(err, readhold.ErrSegmentStale) {
			return degraded(readhold.DegradedStaleSegment, err)
		} else if err != nil {
			return degraded(readhold.DegradedCloseFailed, err)
		}
		holds.releasePredecessors(group)
	}
	group.prepared = schedule.Segment
	// The settling wait its time_delay suggestion is reckoned beyond is this
	// Segment's, the newest prepared: the previous one closed above was
	// specified too, with Plans of its own, and an older one is refused
	// before here.
	holds.mu.Lock()
	group.settlingWait, group.settled = spec.SettlingWait, true
	group.delayUnit = group.queryStep
	if group.queryUnaligned {
		for _, plan := range schedule.Plans {
			group.delayUnit = min(group.delayUnit, time.Duration(plan.Spec.EvaluationIntervalSeconds)*time.Second)
		}
	}
	holds.mu.Unlock()
	return nil
}

func (holds *productionReadHolds) countLinks(skipped map[string]int) {
	if len(skipped) == 0 {
		return
	}
	holds.linksMu.Lock()
	for reason, count := range skipped {
		holds.links[reason] += uint64(count)
	}
	holds.linksMu.Unlock()
}

// linkedPlans is a link's Plans this Segment runs, with what the link says
// of each. A link for a Plan the Segment no longer runs carries nothing.
func (holds *productionReadHolds) linkedPlans(spec readhold.GroupSpec, link controlplane.ReadHoldPredecessor) []readhold.PlanLink {
	var wanted []readhold.PlanLink
	for _, plan := range link.Plans {
		for _, ref := range spec.Plans {
			if ref.Key == plan.Key {
				wanted = append(wanted, readhold.PlanLink{PlanRef: ref, PreviousSlot: plan.PreviousSlot,
					CompletionOffsetMillis: plan.CompletionOffsetMillis})
			}
		}
	}
	return wanted
}

// inheritedLinks is the group's own record carrying the bridge of every
// one of the link's Plans already.
func inheritedLinks(record readhold.Record, link controlplane.ReadHoldPredecessor, wanted []readhold.PlanLink) bool {
	for _, want := range wanted {
		found := false
		for _, plan := range record.Plans {
			found = found || (plan.PlanRef == want.PlanRef && plan.InheritedQueryGroup == link.QueryGroup && plan.InheritedClosedAt == link.ClosedAt)
		}
		if !found {
			return false
		}
	}
	return true
}

func (holds *productionReadHolds) ReadHold(qg execution.QueryGroupIdentity) time.Duration {
	return holds.controller.ReadHold(qg)
}

// SlotReadHold is the hold a new Slot is frozen with. A hold its group could
// not prepare or write is degraded, not refused: the Slot is frozen with the
// hold the group last read (degradedHold), and counted by what failed.
func (holds *productionReadHolds) SlotReadHold(ctx context.Context, schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime, fence execution.OwnerFence) (time.Duration, error) {
	err := holds.PrepareSchedule(ctx, schedule, fence)
	if err == nil {
		var hold time.Duration
		if hold, err = holds.slotReadHold(ctx, schedule, at, fence); err == nil {
			return hold, nil
		}
		reason := readhold.DegradedHoldFailed
		if errors.Is(err, readhold.ErrSegmentStale) {
			reason = readhold.DegradedStaleSegment
		}
		err = degraded(reason, err)
	}
	var degradation *readHoldDegradation
	if !errors.As(err, &degradation) {
		return 0, err
	}
	holds.countDegraded(schedule.Segment, degradation)
	return holds.degradedHold(schedule, at), nil
}

// degradedHold is the hold the group last read at the Slot, its transitions
// included -- zero for a group without a record -- within the schedule's hold
// limit. It reads nothing, so it cannot fail as the hold it stands in for did.
func (holds *productionReadHolds) degradedHold(schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime) time.Duration {
	limit := min(holds.holdLimit(schedule), time.Duration(execution.MaxReadHoldMillis)*time.Millisecond)
	return min(holds.controller.ReadHoldAt(schedule.Segment.QueryGroup, at), limit)
}

// countDegraded counts one degraded Slot under its reason, and says so in
// the log once per group, Segment and reason: a group whose hold stays
// degraded writes a line when it starts, not one per Slot.
func (holds *productionReadHolds) countDegraded(segment execution.ScheduleSegmentFact, degradation *readHoldDegradation) {
	holds.degradedMu.Lock()
	holds.degraded[degradation.reason]++
	holds.degradedMu.Unlock()
	holds.mu.Lock()
	group := holds.groups[segment.QueryGroup]
	holds.mu.Unlock()
	if group == nil {
		return
	}
	logged := readHoldDegradedLine{start: segment.Start, reason: degradation.reason}
	group.mu.Lock()
	first := group.degradedLogged != logged
	group.degradedLogged = logged
	group.mu.Unlock()
	if first {
		holds.report("degraded_"+degradation.reason, segment.QueryGroup, degradation.err)
	}
}

func (holds *productionReadHolds) slotReadHold(ctx context.Context, schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime, fence execution.OwnerFence) (time.Duration, error) {
	hold, err := holds.controller.SlotReadHold(ctx, schedule, at, fence)
	holds.mu.Lock()
	currentGroup := holds.groups[schedule.Segment.QueryGroup]
	holds.mu.Unlock()
	if currentGroup != nil {
		currentGroup.mu.Lock()
		holds.releasePredecessors(currentGroup)
		currentGroup.mu.Unlock()
	}
	if err == nil && hold > holds.controller.ReadHold(schedule.Segment.QueryGroup) {
		holds.mu.Lock()
		group := holds.groups[schedule.Segment.QueryGroup]
		holds.mu.Unlock()
		if group != nil {
			group.mu.Lock()
			if group.lastTransition != at {
				holds.transitions.Add(1)
				group.lastTransition = at
			}
			group.mu.Unlock()
		}
	}
	if errors.Is(err, readhold.ErrNotRestored) {
		holds.mu.Lock()
		group := holds.groups[schedule.Segment.QueryGroup]
		holds.mu.Unlock()
		if group != nil {
			group.mu.Lock()
			group.prepared = execution.ScheduleSegmentFact{}
			group.mu.Unlock()
		}
	}
	return hold, err
}

// RetireCloseFailed counts a retired group that retired without closing.
func (holds *productionReadHolds) RetireCloseFailed(err error) {
	holds.retireCloseFailed.Add(1)
	holds.report("retire_close_failed", "", err)
}

func (holds *productionReadHolds) report(reason string, qg execution.QueryGroupIdentity, err error) {
	if err != nil && holds.logger != nil {
		holds.logger.Warn("read_hold", reason, 0, 0, slog.String("query_group", string(qg)), slog.String("error", err.Error()))
	}
}

func (holds *productionReadHolds) observation(contractRef execution.FrozenExecutionContractRef, apply func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), directoryReadTimeout)
	defer cancel()
	owner, err := holds.owner(contractRef.Slot.QueryGroup)
	if err == nil {
		var schedule execution.FrozenQueryGroupSchedule
		schedule, err = holds.catalog.ReadFrozenSchedule(ctx, contractRef.Slot.QueryGroup, contractRef.Slot.EvaluationTime)
		if err == nil {
			err = holds.PrepareSchedule(ctx, schedule, owner.Fence)
		}
		if err == nil {
			err = apply(ctx)
		}
	}
	if errors.Is(err, scheduler.ErrReadHoldDegraded) {
		// The group learns nothing while its hold is degraded; its Slots
		// count and log that (countDegraded), not each finding here.
		return
	}
	holds.report("observation_failed", contractRef.Slot.QueryGroup, err)
}

func (holds *productionReadHolds) bindLookback(options *lookback.Options) {
	options.CurrentReadHold, options.ReadHoldAt = holds.controller.ReadHold, holds.controller.ReadHoldAt
	options.OnWholeWindowReadEarly = func(e lookback.ReadHoldEvidence) {
		holds.observation(e.Contract, func(ctx context.Context) error {
			return holds.controller.Observe(ctx, readhold.Evidence{Contract: e.Contract, ArrivalAge: e.ArrivalAge, FirstReadAge: e.FirstReadAge,
				Confirmed: true, WholeWindow: true, Rung: e.Rung, Buckets: e.Buckets})
		})
	}
	options.OnEarlierRead = func(e lookback.EarlierReadEvidence) {
		holds.observation(e.Contract, func(ctx context.Context) error {
			return holds.controller.EarlierRead(ctx,
				readhold.EarlierEvidence{Contract: e.Contract, CandidateHold: e.CandidateHold, Observed: e.Observed, Equal: e.Equal})
		})
	}
	options.OnReadHoldIgnored = func(e lookback.ReadHoldEvidence, reason string) {
		inspection := holds.controller.Inspect(e.Contract.Slot.QueryGroup)
		if reason != lookback.IgnoredNoWholeWindowArrival || !inspection.Loaded || inspection.Missing || holds.controller.ReadHold(e.Contract.Slot.QueryGroup) == 0 {
			return
		}
		holds.observation(e.Contract, func(ctx context.Context) error {
			return holds.controller.Observe(ctx, readhold.Evidence{Contract: e.Contract, Noise: true})
		})
	}
	// Ignored partial revisions and noise stay in the lookback's counters;
	// they never create or renew a zero-h Redis record.
}

func (holds *productionReadHolds) renew(ctx context.Context) {
	holds.mu.Lock()
	now := holds.now()
	if now.Before(holds.nextRenew) {
		holds.mu.Unlock()
		return
	}
	holds.nextRenew = now.Add(readhold.RenewInterval)
	holds.mu.Unlock()
	err := holds.controller.RenewDue(ctx)
	if err != nil {
		holds.mu.Lock()
		holds.nextRenew = now.Add(time.Minute)
		holds.mu.Unlock()
	}
	holds.report("renew_failed", "", err)
}

func (holds *productionReadHolds) observeOvertaken(ref execution.FrozenExecutionContractRef) {
	if record, known := holds.controller.Reading(ref.Slot.QueryGroup); known &&
		(record.Closed || record.SegmentStart > ref.ScheduleSegmentStart) {
		holds.overtaken.Add(1)
	}
}
