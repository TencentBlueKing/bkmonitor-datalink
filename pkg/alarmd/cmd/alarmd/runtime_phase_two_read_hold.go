package main

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"strings"
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
	controller *readhold.Controller
	cfg        config.Config
	repository readHoldCatalog
	catalog    readHoldTimeline
	now        func() time.Time
	logger     *observability.Logger
	mu         sync.Mutex
	groups     map[execution.QueryGroupIdentity]*productionReadHoldGroup
	// sources indexes the owned groups by the data they read
	// (productionReadHoldGroup.querySource), so a new group's seed walks its
	// peers and not every owned group.
	sources map[string]map[execution.QueryGroupIdentity]struct{}
	// limitCuts is the hold each group was last logged cut to by a limit
	// configured under it, so a cut is logged once per group. Its own lock:
	// a cut is told with the controller's group lock held.
	limitCutsMu sync.Mutex
	limitCuts   map[execution.QueryGroupIdentity]time.Duration
	nextRenew   time.Time
	transitions atomic.Uint64
	// queryDuration is the lookback's reading of each group's query
	// duration, for the controller to keep in its record; nil until the
	// lookback is built, and in a process without one.
	queryDuration atomic.Pointer[func(execution.QueryGroupIdentity) (time.Duration, bool)]
	overtaken     atomic.Uint64
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
	// readAtLimit counts the rounds read at their group's hold limit, by
	// the source the group reads (roundReadAtLimit).
	readAtLimitMu sync.Mutex
	readAtLimit   map[string]uint64
}

type productionReadHoldGroup struct {
	mu             sync.Mutex
	session        *ownership.Session
	prepared       execution.ScheduleSegmentFact
	lastTransition execution.EvaluationTime
	predecessors   []execution.QueryGroupIdentity
	queryRoute     string
	// querySource names the data the query reads (readHoldSource): the
	// groups that share it are the peers a new group's first hold is seeded
	// from.
	querySource string
	// semantics is the data source the query reads, as the lookback and the
	// catalog label it (controlplane.SourceSemanticsLabel), so a group the
	// lookback has not sampled is still counted under its source.
	semantics  string
	queryDelay time.Duration
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
	catalog readHoldTimeline, now func() time.Time,
	logger *observability.Logger) (*productionReadHolds, error) {
	holds := &productionReadHolds{cfg: cfg, repository: repository, catalog: catalog,
		now: now, logger: logger, groups: make(map[execution.QueryGroupIdentity]*productionReadHoldGroup), links: make(map[string]uint64),
		degraded: make(map[string]uint64)}
	var err error
	holds.controller, err = readhold.NewController(readhold.Options{Control: control,
		Prefix: productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"), MaxHold: cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration(),
		Now: now, Owner: holds.owner, OnLimitCut: holds.limitCut})
	return holds, err
}

// limitCut logs, once per group and cut, a Slot the limit froze more Slots
// below the one before it than a replay covers: an operator who lowered the
// limit sees which group pays for it and about how many Slots it skips.
func (holds *productionReadHolds) limitCut(cut readhold.LimitCut) {
	maxReplay := int64(holds.cfg.PhaseTwo.Scheduler.MaxReplaySlots)
	if holds.logger == nil || cut.Period <= 0 {
		return
	}
	steps := int64((cut.From - cut.To) / cut.Period)
	if steps <= maxReplay {
		return
	}
	holds.limitCutsMu.Lock()
	if holds.limitCuts == nil {
		holds.limitCuts = make(map[execution.QueryGroupIdentity]time.Duration)
	}
	logged, ok := holds.limitCuts[cut.QueryGroup]
	holds.limitCuts[cut.QueryGroup] = cut.To
	holds.limitCutsMu.Unlock()
	if ok && logged == cut.To {
		return
	}
	holds.logger.Warn("read_hold", "limit_cut", 0, 0, slog.String("query_group", string(cut.QueryGroup)),
		slog.Int64("slot", int64(cut.Slot)), slog.Int64("from_hold_ms", cut.From.Milliseconds()), slog.Int64("to_hold_ms", cut.To.Milliseconds()),
		slog.Int64("period_s", int64(cut.Period/time.Second)), slog.Int64("max_replay_slots", maxReplay),
		slog.Int64("expected_skipped_slots", steps-maxReplay))
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

// readHoldInheritedQueryLatency is the previous owner's reading of each Query
// Group's query duration, as its read-hold record keeps it, for the slot
// source to judge a group it has no reading of its own for; none without
// read holds. The controller is looked up when asked, so a source built
// before it has one, and has no reading until it does. It takes the group's
// controller lock and nothing else: the source asks it under none of its own.
func readHoldInheritedQueryLatency(holds *productionReadHolds) scheduler.QueryLatency {
	if holds == nil {
		return nil
	}
	return holds.inheritedQueryDuration
}

// inheritedQueryDuration is the group's record's query duration
// (readhold.Controller.InheritedQueryDuration); none without a controller.
func (holds *productionReadHolds) inheritedQueryDuration(qg execution.QueryGroupIdentity) (time.Duration, bool) {
	if holds == nil || holds.controller == nil {
		return 0, false
	}
	return holds.controller.InheritedQueryDuration(qg)
}

func (holds *productionReadHolds) bind(qg execution.QueryGroupIdentity, session *ownership.Session) {
	holds.mu.Lock()
	defer holds.mu.Unlock()
	holds.indexSourceLocked(qg, "")
	holds.groups[qg] = &productionReadHoldGroup{session: session}
}

// indexSourceLocked moves the group in the source index from the source it
// was under to source; empty takes it out. Called with holds.mu held.
func (holds *productionReadHolds) indexSourceLocked(qg execution.QueryGroupIdentity, source string) {
	if previous := holds.groups[qg]; previous != nil && previous.querySource != "" {
		if peers := holds.sources[previous.querySource]; peers != nil {
			delete(peers, qg)
			if len(peers) == 0 {
				delete(holds.sources, previous.querySource)
			}
		}
	}
	if source == "" {
		return
	}
	if holds.sources == nil {
		holds.sources = make(map[string]map[execution.QueryGroupIdentity]struct{})
	}
	if holds.sources[source] == nil {
		holds.sources[source] = make(map[execution.QueryGroupIdentity]struct{})
	}
	holds.sources[source][qg] = struct{}{}
}

// forget releases the binding a Runner made, and only that one: the session
// is the Runner's own, and a Runner lost and released after the next one
// bound the same Query Group must leave the newer binding - and the
// controller's state for the group - in place. Released by name, a late
// release unbound its successor, whose every Slot was then refused as a
// stale fence while its lease kept renewing. Compared as the *Session the
// binding holds, never as an address or an id derived from one: the binding
// keeps the old session reachable, so a pointer it compares against cannot
// have been reused. The controller's state goes in the same critical section
// as the binding: forgotten after the lock was let go, a successor could bind
// and load in between, and this release then dropped the successor's freshly
// read hold. Forget takes only the controller's map lock, which is never
// held across the Owner callback that takes holds.mu, so holding holds.mu
// over it is safe, as releasePredecessors already does.
func (holds *productionReadHolds) forget(qg execution.QueryGroupIdentity, session *ownership.Session) {
	holds.mu.Lock()
	defer holds.mu.Unlock()
	group := holds.groups[qg]
	if group == nil || group.session != session {
		return
	}
	holds.indexSourceLocked(qg, "")
	delete(holds.groups, qg)
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

// Restore only invalidated entries of groups this Worker has opened: a read
// that failed, or a write conflict that dropped what was read. A group not
// opened here is read by the round that opens it (load), once its lease is
// held; read before, the record could still change under the old owner's
// lease, and nothing would read it again. One bad answer leaves that group
// retryable without discarding successfully loaded siblings.
func (holds *productionReadHolds) restore(ctx context.Context, groups []execution.QueryGroupIdentity) error {
	var missing []execution.QueryGroupIdentity
	holds.mu.Lock()
	for _, qg := range groups {
		if holds.groups[qg] != nil {
			missing = append(missing, qg)
		}
	}
	holds.mu.Unlock()
	missing = slices.DeleteFunc(missing, func(qg execution.QueryGroupIdentity) bool { return holds.controller.Inspect(qg).Loaded })
	if len(missing) == 0 {
		return nil
	}
	return holds.controller.RestoreBatch(ctx, missing)
}

// load reads the records of the Query Groups a round has just opened, each
// whether or not its entry is loaded: one loaded before this Worker held the
// lease - read as another group's predecessor - may be older than what the
// previous owner last wrote. A pipeline of ownership.ControlReadBatch groups
// at a time, so a pipeline that fails leaves only its own groups to the next
// reconcile's restore; until then their Slots take the unrestored path, never
// a hold read before the lease.
func (holds *productionReadHolds) load(ctx context.Context, groups []execution.QueryGroupIdentity) {
	for start := 0; start < len(groups); start += ownership.ControlReadBatch {
		batch := groups[start:min(start+ownership.ControlReadBatch, len(groups))]
		holds.report("restore_failed", "", holds.controller.RestoreBatch(ctx, batch))
	}
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
		source := readHoldSource(object.QueryPlan, route)
		holds.indexSourceLocked(qg, source)
		owned.querySource = source
		owned.semantics = controlplane.SourceSemanticsLabel(object.QueryPlan.SourceSemantics)
		owned.queryRoute, owned.queryDelay, owned.queryStep = route, delay, step
		owned.queryUnaligned = object.QueryPlan.NotTimeAlign
	}
	holds.mu.Unlock()
	return route, delay, step, nil
}

// readHoldSource names the data a query reads: its result tables and data
// sources, in order, or for a query that names none (a PromQL one) its route.
// Groups that read the same data see it land at the same time, which is what
// seeding a new group from its peers rests on.
func readHoldSource(facts execution.QueryPlanFacts, route string) string {
	var names []string
	for _, clause := range facts.QueryList {
		if clause.TableID != "" || clause.DataSource != "" {
			names = append(names, clause.DataSource+"/"+clause.TableID)
		}
	}
	if len(names) == 0 {
		return "route:" + route
	}
	slices.Sort(names)
	return strings.Join(slices.Compact(names), ",")
}

// A group on a data source no group here has measured starts from the
// deployment's ready delay (MinReadyDelay): the wait every group read at
// before the wait came down to its floor and the rest was measured per
// source, so a new source reads no earlier than every source read before,
// and its own early reads take it down from there. It is the cold-start value
// of a new source.

// readHoldPeerPercentile is how high in its peers' measured arrivals a new
// group starts: high enough that few of them would have read incomplete.
const readHoldPeerPercentile = 0.9

// seedArrival is where a group with no record of its own starts: the
// readHoldPeerPercentile of the arrival ages its peers on the same data
// measured, each net of its own time_delay, with the group's time_delay added;
// or coldStart on a source none of them has measured.
//
// A peer seed is never past ceiling, what the group's own read budget leaves
// past its settling floor. Peers on the same data can run on longer
// schedules: a ten-second group beside one-minute groups that measured a
// 30-second landing would otherwise start reading past its own budget, late
// on every Slot until its own early reads brought it down. Its own
// measurements still raise it past the ceiling when its data lands later.
func (holds *productionReadHolds) seedArrival(qg execution.QueryGroupIdentity, delay, coldStart, ceiling time.Duration) (time.Duration, string) {
	type peer struct {
		group execution.QueryGroupIdentity
		delay time.Duration
	}
	var peers []peer
	holds.mu.Lock()
	if owned := holds.groups[qg]; owned != nil && owned.querySource != "" {
		for group := range holds.sources[owned.querySource] {
			if other := holds.groups[group]; group != qg && other != nil {
				peers = append(peers, peer{group, other.queryDelay})
			}
		}
	}
	holds.mu.Unlock()
	var arrivals []time.Duration
	for _, p := range peers {
		if arrival, seed := holds.controller.Arrival(p.group); arrival > 0 && seed == "" {
			arrivals = append(arrivals, time.Duration(arrival)*time.Millisecond-p.delay)
		}
	}
	if len(arrivals) == 0 {
		return delay + coldStart, readhold.SeedColdStart
	}
	slices.Sort(arrivals)
	index := min(int(float64(len(arrivals))*readHoldPeerPercentile), len(arrivals)-1)
	return delay + min(max(arrivals[index], 0), max(ceiling, 0)), readhold.SeedPeer
}

// seedFor is the seed of a group with no record of its own, on the schedule
// it is prepared for: the cold start is the deployment's wait as the
// schedule's tightest budget allows it, and the peer seed's ceiling is that
// budget past its settling floor - both from the Plan with the smallest
// budget, the one a late read misses first.
func (holds *productionReadHolds) seedFor(qg execution.QueryGroupIdentity, delay time.Duration, schedule execution.FrozenQueryGroupSchedule) (time.Duration, string) {
	var coldStart, ceiling time.Duration
	for index, plan := range schedule.Plans {
		offset := time.Duration(plan.Spec.CompletionOffsetSeconds()) * time.Second
		budget := offset - holds.cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
		// The cold start is where the group was read before the wait came
		// down to its floor: the deployment's wait as its schedule's budget
		// allowed it - a ten-second period was read at ten seconds then too.
		previous := execution.SettlingWaitWithinQueryBudget(budget, holds.cfg.PhaseTwo.Access.MinReadyDelay.Duration())
		room := budget - holds.holdBase(qg, budget)
		if index == 0 || previous < coldStart {
			coldStart = previous
		}
		if index == 0 || room < ceiling {
			ceiling = room
		}
	}
	return holds.seedArrival(qg, delay, coldStart, ceiling)
}

func (holds *productionReadHolds) spec(ctx context.Context, schedule execution.FrozenQueryGroupSchedule) (readhold.GroupSpec, error) {
	_, delay, step, err := holds.queryBasis(ctx, schedule)
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
		spec.Plans = append(spec.Plans, readhold.PlanRef{Key: plan.Key()})
		offset := time.Duration(plan.Spec.CompletionOffsetSeconds()) * time.Second
		budget := offset - holds.cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
		wait := holds.holdBase(schedule.Segment.QueryGroup, budget)
		if len(spec.Plans) == 1 || wait < spec.SettlingWait {
			spec.SettlingWait = wait
		}
	}
	// Only a group with no record of its own is ever seeded: one with a
	// record skips the walk of its peers, at a rollout as in steady state.
	if !holds.controller.HasOwnRecord(schedule.Segment.QueryGroup) {
		spec.SeedArrivalAge, spec.SeedOrigin = holds.seedFor(schedule.Segment.QueryGroup, delay, schedule)
	}
	return spec, nil
}

// holdBase is where a group's read hold is reckoned from, for one of its
// Plans' query budgets.
//
// The settling floor, not the group's whole read wait: the hold is how much
// later than the floor the group's data lands, and the phase is added after
// it (read = window end + max(floor, landing) + phase). Reckoned from the
// floor plus the phase, the hold absorbed the phase, and every group whose
// data lands at one moment - every group seeded the cold start at a rollout,
// every group on one source once measured - read in the same second again.
func (holds *productionReadHolds) holdBase(_ execution.QueryGroupIdentity, budget time.Duration) time.Duration {
	return execution.SettlingFloorWithinQueryBudget(budget, holds.cfg.PhaseTwo.Access.MinReadyDelay.Duration())
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

// PeekSlotReadHold is the hold the Slot would be frozen with, asked without
// freezing it (readhold.Controller.PeekSlotReadHold): nothing is prepared,
// released, counted or degraded. An error refuses the question, and the
// asker freezes the Slot the ordinary way.
func (holds *productionReadHolds) PeekSlotReadHold(schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime) (time.Duration, error) {
	return holds.controller.PeekSlotReadHold(schedule, at)
}

// degradedHold is the hold the group last read at the Slot, its transitions
// included -- zero for a group without a record -- within the schedule's hold
// limit. It reads nothing, so it cannot fail as the hold it stands in for did.
func (holds *productionReadHolds) degradedHold(schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime) time.Duration {
	limit := min(holds.holdLimit(schedule), time.Duration(execution.MaxReadHoldMillis)*time.Millisecond)
	return min(holds.controller.ReadHoldAt(schedule.Segment.QueryGroup, at), limit)
}

// roundReadAtLimit says the round of the Slot ref names was read at its
// group's hold limit while the group's data lands past it - frozen with a
// hold at the limit its record keeps, the record held there with its data
// still read early within its quiet bar (readhold.Record.AtLimitHeld) - and
// counts it under the source the group reads: one of lookback.Sources,
// other for a group whose source is not known yet. The record is the
// group's now, not as the Slot was frozen: the first round after a raise
// is read at the limit on the arrival that raised it, and a group quiet
// past its bar is being tried lower, not read at the limit.
func (holds *productionReadHolds) roundReadAtLimit(ref execution.FrozenExecutionContractRef) bool {
	record, known := holds.controller.Reading(ref.Slot.QueryGroup)
	if !known || ref.ReadHoldMillis < record.LimitMillis || !record.AtLimitHeld(ref.Slot.QueryGroup, holds.now().UnixMilli()) {
		return false
	}
	holds.mu.Lock()
	source := ""
	if group := holds.groups[ref.Slot.QueryGroup]; group != nil {
		source = group.semantics
	}
	holds.mu.Unlock()
	if !slices.Contains(lookback.Sources, source) {
		source = lookback.SourceOther
	}
	holds.readAtLimitMu.Lock()
	if holds.readAtLimit == nil {
		holds.readAtLimit = make(map[string]uint64, len(lookback.Sources))
	}
	holds.readAtLimit[source]++
	holds.readAtLimitMu.Unlock()
	return true
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
	// Read before the controller is: the lookback asks the controller under
	// its own lock, so it is never asked under the controller's.
	if measure := holds.queryDuration.Load(); measure != nil {
		if measured, ok := (*measure)(schedule.Segment.QueryGroup); ok {
			holds.controller.MeasureQueryDuration(schedule.Segment.QueryGroup, measured)
		}
	}
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

// observation reads the Slot's schedule and applies one finding: about three
// Redis calls, each bounded by the client's own timeouts, with no deadline
// over them; one borrowed from the directory's used to drop the sample when
// the three together ran past it.
func (holds *productionReadHolds) observation(contractRef execution.FrozenExecutionContractRef, apply func(context.Context) error) {
	ctx := context.Background()
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
	options.CurrentCandidate = holds.controller.EarlierCandidate
	options.EarlierRefused = holds.controller.EarlierRefused
	options.OnWholeWindowReadEarly = func(e lookback.ReadHoldEvidence) {
		holds.observation(e.Contract, func(ctx context.Context) error {
			return holds.controller.Observe(ctx, readhold.Evidence{Contract: e.Contract, ArrivalAge: e.ArrivalAge,
				Rung: e.Rung, Buckets: e.Buckets, Probation: e.Probation})
		})
	}
	options.OnEarlierRead = func(e lookback.EarlierReadEvidence) {
		holds.observation(e.Contract, func(ctx context.Context) error {
			return holds.controller.EarlierRead(ctx,
				readhold.EarlierEvidence{Contract: e.Contract, CandidateHold: e.CandidateHold, Observed: e.Observed, Equal: e.Equal})
		})
	}
	// One line per trial end, so a deployment whose lookback route is not
	// public can still read, group by group, whether its trials complete and
	// what overtakes those that do not. A group has at most one trial per
	// rest, so the lines are as many as the trials.
	// The probation after a lowering is resumed by the process that takes
	// the group over, from the lowering its record names.
	options.LastLowering = holds.controller.LastLowering
	// A group whose search has run out has its floor probed once it has
	// stood long enough; a probe that passes lowers the hold, a probation
	// after it as after every lowering, one that fails relearns the floor. One line per
	// probe end, a probe being two hours of rounds at least.
	options.FloorProbe, options.LoweringVerified = holds.controller.FloorProbe, holds.controller.LoweringVerified
	// A probe goes on, after a rollout or a takeover, from the clean rounds its
	// record kept, written every lookback.FloorProbeProgressEvery of them; a
	// write that loses its compare is dropped, the move that won it taken.
	options.FloorProbeResume = holds.controller.FloorProbeResume
	options.OnFloorProbeProgress = func(e lookback.FloorProbeProgress) {
		holds.observation(e.Contract, func(ctx context.Context) error {
			return holds.controller.FloorProbeProgress(ctx, readhold.FloorProbeProgressEvidence{Contract: e.Contract, Floor: e.Floor,
				Kind: e.Kind, Hold: e.Hold, Clean: e.Clean, Started: e.Started, Period: e.Period})
		})
	}
	// One line per round found read early, not sampled: what it was read at
	// and after which lowering, how its data came, and what it decided - kept
	// for a group in probation (lookbackDecisionTap) - or why that is not
	// known. Its strategies are the decided Plans', or the Query Group's from
	// its frozen schedule when the decision is not known.
	options.OnWindowReadEarly = func(e lookback.WindowReadEarly) {
		if holds.logger == nil {
			return
		}
		holds.logger.Info("lookback", "window_read_early", 0, 0, holds.readEarlyAttrs(e)...)
	}
	// One line per probe start: resumed_from is the clean rounds it went on
	// from, kept by the process that ran it before, 0 for a fresh one.
	options.OnFloorProbeStart = func(e lookback.FloorProbeStart) {
		if holds.logger == nil {
			return
		}
		holds.logger.Info("lookback", "floor_probe_started", 0, 0, slog.String("query_group", string(e.Contract.Slot.QueryGroup)),
			slog.Int64("slot", int64(e.Contract.Slot.EvaluationTime)), slog.String("source", e.Source), slog.String("kind", e.Kind),
			slog.Int64("probe_ms", e.Floor.Milliseconds()), slog.Int64("read_hold_ms", e.Hold.Milliseconds()),
			slog.Int("rounds_required", e.Rounds), slog.Int64("started_slot", int64(e.Started)), slog.Int("resumed_from", e.ResumedFrom))
	}
	options.OnFloorProbe = func(e lookback.FloorProbeEnd) {
		if holds.logger != nil {
			// kind is what the probe read: verify, the candidate the trials chose
			// before the hold moves; midpoint; floor. probe_ms is the hold the
			// probe's rounds were read at; floor_ms the
			// group's floor as the controller has it when the line is written
			// (-1 while none): the two are one while the probe reads the floor
			// itself, apart while it reads a midpoint above it. rounds_required
			// is the clean rounds it needed: ProbationRounds, or near a floor a
			// probe failed at eight hours of its rounds if more - 480 at a
			// minute's period. A failed probe says how its last
			// round's formal read differed, in buckets by change class.
			floor := int64(-1)
			if record, loaded := holds.controller.Reading(e.Contract.Slot.QueryGroup); loaded && record.EarlierFloorMillis != nil {
				floor = *record.EarlierFloorMillis
			}
			attrs := []slog.Attr{slog.String("query_group", string(e.Contract.Slot.QueryGroup)),
				slog.Int64("slot", int64(e.Contract.Slot.EvaluationTime)), slog.String("source", e.Source), slog.String("event", e.Event),
				slog.String("kind", e.Kind), slog.Int64("probe_ms", e.Floor.Milliseconds()), slog.Int64("floor_ms", floor), slog.Int64("read_hold_ms", e.Hold.Milliseconds()),
				slog.Int("clean_rounds", e.Clean), slog.Int("rounds_required", e.Rounds), slog.Int64("started_slot", int64(e.Started)),
				slog.Int("resumed_from", e.ResumedFrom)}
			if e.Event == lookback.FloorProbeFailed {
				for _, change := range lookback.Changes {
					attrs = append(attrs, slog.Int(change, e.Changes[change]))
				}
			}
			holds.logger.Info("lookback", "floor_probe_ended", 0, 0, attrs...)
		}
		if e.Event != lookback.FloorProbePassed && e.Event != lookback.FloorProbeFailed {
			return
		}
		holds.observation(e.Contract, func(ctx context.Context) error {
			return holds.controller.FloorProbeResult(ctx, readhold.FloorProbeEvidence{Contract: e.Contract, Floor: e.Floor,
				Kind: e.Kind, Hold: e.Hold, Passed: e.Event == lookback.FloorProbePassed})
		})
	}
	options.OnEarlierTrialEnd = func(e lookback.EarlierTrialEnd) {
		if holds.logger == nil {
			return
		}
		// The search below the hold as the controller has it when the line is
		// written: its floor (-1 while none) and different trials in a row,
		// and the hold a moved one is moving to (-1 while none is pending).
		floor, differents, pending := int64(-1), 0, int64(-1)
		if record, loaded := holds.controller.Reading(e.Contract.Slot.QueryGroup); loaded {
			if record.EarlierFloorMillis != nil {
				floor = *record.EarlierFloorMillis
			}
			differents = record.EarlierDifferents
			if record.PendingHoldMillis != nil {
				pending = *record.PendingHoldMillis
			}
		}
		attrs := []slog.Attr{slog.String("query_group", string(e.Contract.Slot.QueryGroup)),
			slog.Int64("slot", int64(e.Contract.Slot.EvaluationTime)), slog.String("source", e.Source), slog.String("outcome", e.Outcome),
			slog.String("overtaken_by", e.OvertakenBy), slog.Int64("candidate_hold_ms", e.CandidateHold.Milliseconds()),
			slog.Int64("read_hold_ms", e.Contract.ReadHoldMillis), slog.Int("ahead_slots", e.AheadSlots),
			slog.Int64("floor_ms", floor), slog.Int("floor_differents", differents)}
		if e.OvertakenBy == lookback.OvertakenHoldMoved {
			// A descent a step per Slot reads as reserved at the lower hold,
			// frozen a step below the Slot before, pending the lower hold.
			attrs = append(attrs, slog.Int64("reserved_hold_ms", e.ReservedHold.Milliseconds()),
				slog.Int64("frozen_hold_ms", e.FrozenHold.Milliseconds()), slog.Int64("pending_hold_ms", pending))
		}
		holds.logger.Info("lookback", "earlier_trial", 0, 0, attrs...)
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

// readEarlyAttrs is a round found read early's line.
func (holds *productionReadHolds) readEarlyAttrs(e lookback.WindowReadEarly) []slog.Attr {
	// The hold the group's lowering came from, as the controller has it when
	// the line is written - before the finding it carries raises the hold -
	// and -1 while the group is not lowered.
	loweredFrom := int64(-1)
	if record, loaded := holds.controller.Reading(e.Contract.Slot.QueryGroup); loaded && record.Lowered {
		loweredFrom = record.LoweredFromMillis
	}
	attrs := []slog.Attr{slog.String("query_group", string(e.Contract.Slot.QueryGroup)),
		slog.Int64("slot", int64(e.Contract.Slot.EvaluationTime)), slog.String("source", e.Source), slog.String("path", e.Path),
		slog.Int64("read_hold_ms", e.Hold.Milliseconds()), slog.Int64("lowered_from_ms", loweredFrom),
		slog.String("rung", e.Rung), slog.Int64("completion_age_ms", e.CompletionAge.Milliseconds())}
	for _, change := range lookback.Changes {
		attrs = append(attrs, slog.Int(change, e.Changes[change]))
	}
	strategies, count, from := []string(nil), 0, "decision"
	if decision := e.Decision; decision != nil {
		attrs = append(attrs, slog.String("decision_summary", "kept"), slog.Bool("completed", decision.Completed),
			slog.Uint64("events", decision.Events), slog.Uint64("state_mutations", decision.StateMutations))
		for _, kind := range observability.LevelOutcomeKinds {
			attrs = append(attrs, slog.Uint64("levels_"+strings.ToLower(kind), decision.Levels[kind]))
		}
		strategies, count = decision.Strategies, decision.StrategyCount
	} else {
		attrs = append(attrs, slog.String("decision_summary", "unavailable"), slog.String("decision_missing", e.DecisionMissing))
		strategies, count, from = holds.scheduledStrategies(e.Contract.Slot.QueryGroup, e.Contract.Slot.EvaluationTime)
	}
	return append(attrs, slog.String("strategies", strings.Join(strategies, ",")), slog.Int("strategy_count", count),
		slog.String("strategies_from", from))
}

// scheduledStrategies is the first four strategies of the Query Group's frozen schedule at slot and how many it has,
// "schedule"; or none, "unavailable", when the schedule cannot be read.
func (holds *productionReadHolds) scheduledStrategies(qg execution.QueryGroupIdentity, slot execution.EvaluationTime) ([]string, int, string) {
	if holds.catalog == nil {
		return nil, 0, "unavailable"
	}
	schedule, err := holds.catalog.ReadFrozenSchedule(context.Background(), qg, slot)
	if err != nil {
		return nil, 0, "unavailable"
	}
	seen := map[string]bool{}
	var strategies []string
	for _, plan := range schedule.Plans {
		if id := plan.Identity.StrategyID; id != "" && !seen[id] {
			seen[id] = true
			strategies = append(strategies, id)
		}
	}
	sort.Strings(strategies)
	return strategies[:min(len(strategies), 4)], len(strategies), "schedule"
}
