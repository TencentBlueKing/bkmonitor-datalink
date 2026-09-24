package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

const SourceAlgorithmTypePingUnreachable = "PingUnreachable"

type SourceIdentity struct {
	TenantID   string
	BusinessID string
	SpaceScope string
}

type SourceStrategy struct {
	SourceID          string
	Document          json.RawMessage
	Identity          SourceIdentity
	SourceDisposition *ObjectDisposition
	// digest is the source facts digest the observation computed for this
	// strategy, kept so that neither the observation id nor the candidate
	// cache hashes the document a second time. Empty means not computed yet;
	// strategyDigest then computes it.
	digest string
}

type PrimaryQuerySource struct {
	TimeDelaySeconds int64
	Identity         SourceIdentity
	StrategyID       string
	ItemID           string
	QueryMD5         string
	Expression       string
	IdentityFields   []string
	Functions        []json.RawMessage
	QueryConfigs     []json.RawMessage
}

type PrimaryQueryCompiler interface {
	CompilePrimaryQuery(context.Context, PrimaryQuerySource) (execution.QueryPlanFacts, error)
}

// RoundScopedCompiler is a compiler that closes over facts which can change
// between rounds. CompilerForRound freezes them into the compiler the round
// uses for every strategy, and names what it froze: the identity is part of
// what the CandidateCache keys reuse on, next to the wire protocol, so a
// candidate compiled under other facts is not handed to this round. A
// compiler whose closure is fixed for the process life need not implement
// it; its identity is the binary.
type RoundScopedCompiler interface {
	PrimaryQueryCompiler
	CompilerForRound() (PrimaryQueryCompiler, string, error)
}

type AlgorithmDependencyQueryCompiler interface {
	CompileAlgorithmDependencyQuery(context.Context, PrimaryQuerySource, string) (execution.QueryPlanFacts, error)
}

type BuildRequest struct {
	Strategies []SourceStrategy
	Planner    PrimaryQueryCompiler
	// Cache, when set, hands back what an earlier round compiled from the
	// same source document instead of compiling it again. Nil compiles every
	// strategy, which is what every round did before the cache existed.
	Cache *CandidateCache
	// OutputProtocol is the deployment's wire format choice, frozen into every
	// Plan this build produces. Empty means the revision decides, which is what
	// the process did before the choice existed.
	OutputProtocol string
	// TargetSources says which sources the deployment has for a target
	// plan's dynamic references. A Plan that references a source the
	// deployment does not have is withheld by name at compile time
	// (DYNAMIC_GROUP_SOURCE_UNCONFIGURED): a deployment fact is not a
	// runtime fact, and reporting it every Slot as an unavailable selector
	// would dress a configuration gap up as a cache outage.
	TargetSources TargetSources
	// NoDataPolicy is the deployment's no-data settings, frozen into every Plan
	// that does not override them. The zero value is what the process did
	// before the settings existed.
	NoDataPolicy NoDataPolicy
	LastGood     *PublishedSnapshot
	// PreviousDispositions is the published source audit of LastGood: the
	// memory of the removal grace, which is where a strategy absent from the
	// observed set was first found absent (PENDING_REMOVAL.AbsentSince). Nil
	// means no grace history.
	PreviousDispositions []ObjectDisposition
	// PendingAbsences is the same memory from the candidate the previous
	// round left unconfirmed, by source id: the first round of an absence
	// publishes a candidate, and the round that confirms it has to stamp the
	// same moment or the candidate never confirms. Nil means none pending.
	PendingAbsences map[string]int64
	// Now is the round's clock, what an absence is measured against. Zero
	// means the wall clock.
	Now time.Time
}

// AbsenceGracePeriod is how long a LastGood strategy absent from the
// observed active set keeps executing before its Plan leaves the Catalog.
//
// A period rather than one round because the active set is read from a
// list another program writes, and that list has been seen to lose entries
// for minutes at a time with the strategies unchanged: one deployment's
// hourly full refresh dropped 99 ids for about six and a half minutes every
// hour, and a one-round grace removed 22 Plans, swept their assignments and
// re-acquired them five minutes later, with every Slot in between missing
// and written off as CONFIG_DRIFT - eight percent of the hour blind, for a
// configuration that never changed. The writer's flutter is the writer's to
// fix; the reader still must not turn it into a detection gap, because the
// next writer will flutter too.
//
// Ten minutes is the observed flutter with room to spare, and a program
// constant rather than a setting: an operator does not know this number
// better than the program. What it costs is that a strategy really removed
// runs for up to ten minutes longer.
const AbsenceGracePeriod = 10 * time.Minute

type FrozenPlan struct {
	Identity        execution.PlanIdentity
	Plan            contract.EvaluationPlanV2
	PlanRevision    string
	StateGeneration execution.StateGeneration `json:",omitempty"`
	// NoDataSuspended names why this Plan's no-data detection is not attached,
	// and is empty for a Plan that has it and for one that never asked for it.
	// Those two are told apart by Plan.NoData, and the three together are what
	// the composition partitions; neither field is inferred from the other.
	//
	// It is omitempty and it has to be: this struct is inside the Query Group
	// the object digest is taken over, so a field that serialized on every
	// Plan would change every digest in the deployment at once and cost a
	// republication and a Segment recut for content that did not change.
	NoDataSuspended      string `json:",omitempty"`
	ScheduleSpec         execution.ScheduleSpec
	ScheduleRevision     execution.PlanScheduleRevision
	RequirementTemplates []execution.DataRequirementTemplate                    `json:"RequirementTemplates,omitempty"`
	QueryPlans           map[execution.LogicalQueryRef]execution.QueryPlanFacts `json:"QueryPlans,omitempty"`
	// Shard is the piece of a split strategy this Plan is, nil for a Plan
	// that is not split. It is execution content: the Slot names its gap
	// marker and no-data memory by it. Omitted when nil for the same reason
	// NoDataSuspended is - this struct is inside the object digest.
	Shard *execution.ShardRef `json:",omitempty"`
	// LevelContractRefs are the Level contract references the Leader derived
	// from the same compilation that produced StateGeneration, published so
	// every Worker validates and writes the Plan's records with the Leader's
	// refs rather than its own build's derivation (decision-020 section
	// 4.7.9). Execution content, inside the object digest: a Plan whose refs
	// moved is a Plan whose records mean something else. Omitted when nil so
	// an object published before the field keeps every digest it had.
	LevelContractRefs []execution.RuntimeLevelContractRef `json:",omitempty"`
	// NoDataLevelContractRefs are the same for the Plan's no-data view, whose
	// Level shares an ID with a declared Level and has refs of its own.
	NoDataLevelContractRefs []execution.RuntimeLevelContractRef `json:",omitempty"`
}

// Key is this Plan's index key: the strategy and the piece. Two pieces of one
// strategy are two Plans in every index the control plane keeps.
func (plan FrozenPlan) Key() execution.PlanKey {
	return execution.PlanKeyOf(plan.Identity, execution.ShardOf(plan.Shard))
}

// shardPointerOf is the carried form of a key's piece for a placeholder Plan
// built from an index entry: the index keeps only the piece's index, so the
// placeholder carries only that. Nil for a Plan that is not split.
func shardPointerOf(key execution.PlanKey) *execution.ShardRef {
	if key.ShardIndex == 0 {
		return nil
	}
	return &execution.ShardRef{Index: key.ShardIndex}
}

// TargetSources is what the deployment renders for a target plan's dynamic
// references: a dynamic group cache prefix, or not. Topology references
// resolve against the CMDB host cache every deployment has.
type TargetSources struct {
	DynamicGroups bool
}

func (sources TargetSources) key() string {
	if sources.DynamicGroups {
		return "dynamic_groups"
	}
	return ""
}

// NoDataPolicy is what the deployment says about no-data detection for every
// item that does not say it itself. Today that is one setting: how long an
// absence goes on being tracked.
//
// It is its own build input rather than part of the compiler's identity. The
// compiler identity closes over query compilation facts - event storage, data
// access, CMDB tables, the disk and network filters - and a tracking horizon
// changes no query's compiled result. Folding it in would buy cache
// invalidation by giving "query compiler identity" a second meaning, and tie a
// no-data policy to whichever compiler the deployment happens to use.
type NoDataPolicy struct {
	// TrackingHorizonSeconds is the platform default horizon. Zero means no
	// horizon, which is what every deployment had before the setting existed.
	TrackingHorizonSeconds int64
}

// key is this policy's contribution to the round key, in the same shape as
// TargetSources.key.
//
// A platform default that changed has to empty the candidate cache, because
// the cache is keyed by the strategy document and the document is exactly what
// did not change. Without this the new default reaches only the strategies
// whose own document happens to change next - every other Plan keeps compiling
// with the old horizon, the object bytes stay put, the digest does not move,
// and the setting reads as applied while doing nothing. That is the default
// way to configure it, so without this key the feature is off by default and
// looks on.
func (policy NoDataPolicy) key() string {
	if policy.TrackingHorizonSeconds == 0 {
		return ""
	}
	return "no_data_horizon=" + strconv.FormatInt(policy.TrackingHorizonSeconds, 10)
}

// planCompileFacts is what compiling one item produced besides the Plan: the
// schedule it runs on, and whether its no-data half was suspended.
//
// One struct rather than three more return values, because the three are one
// answer about one Plan and a caller that took two of them would be describing
// a Plan it did not fully read.
type planCompileFacts struct {
	ScheduleSpec     execution.ScheduleSpec
	ScheduleRevision execution.PlanScheduleRevision
	// NoDataSuspended is empty for a Plan whose no-data detection is attached
	// and for one that never configured it. See FrozenPlan.
	NoDataSuspended string
}

type QueryGroup struct {
	Identity         execution.QueryGroupIdentity
	QueryPlan        execution.QueryPlanFacts
	Plans            []FrozenPlan
	MembershipDigest string
	ScheduleRevision execution.ScheduleRevision
}

type Disposition string

const (
	DispositionAccepted             Disposition = "ACCEPTED"
	DispositionSourceIncomplete     Disposition = "SOURCE_INCOMPLETE"
	DispositionConfigRejected       Disposition = "CONFIG_REJECTED"
	DispositionStaleConfig          Disposition = "STALE_CONFIG"
	DispositionPendingRemoval       Disposition = "PENDING_REMOVAL"
	DispositionRemoved              Disposition = "REMOVED"
	DispositionUnsupported          Disposition = "UNSUPPORTED_PHASE2_CAPABILITY"
	DispositionCompatibilityIgnored Disposition = "COMPATIBILITY_IGNORED"
	// DispositionConfigNormalized is an object accepted with a part of its
	// configuration read as something other than what was written, the way
	// Python reads it: the Plan runs, and this says what was widened. It
	// is not withheld from anything; it is listed with the withheld
	// dispositions because that is the list a reader looks at for "what did
	// the catalog do to my strategy", and a widening that is not there is a
	// widening nobody finds.
	DispositionConfigNormalized Disposition = "CONFIG_NORMALIZED"
)

// ReasonEffectiveTimeRangeInvalid names a Level whose uptime has a range
// with a start or end that does not parse, read as 00:00 or 23:59 as Python
// reads it.
const ReasonEffectiveTimeRangeInvalid = "EFFECTIVE_TIME_RANGE_INVALID"

// ReasonPriorityIgnored names a Plan compiled from a strategy that takes
// part in priority arbitration, run as the standalone strategy it is. The
// arbitration belongs to the platform's alert pipeline, so a lower-priority
// strategy detects and alerts beside a higher one on the same target.
const ReasonPriorityIgnored = "PRIORITY_IGNORED"

// ReasonLevelTriggerBorrowed names a Level whose algorithms sit at a level
// the strategy wrote no trigger for, run on the strategy's first trigger as
// the platform runs it.
const ReasonLevelTriggerBorrowed = "LEVEL_TRIGGER_BORROWED"

// legacyDefaultRecoveryWindows is the recovery window the platform uses when
// an alert's level has no recovery configured (its recovery checker's
// DEFAULT_CHECK_WINDOW_SIZE).
const legacyDefaultRecoveryWindows = 5

// borrowedLegacyDetect is the detect a level without one runs on, read the
// way the platform reads it. Its trigger checker, finding no trigger for the
// level, falls back to the strategy's first: the count, the window and the
// effective time that travels with them. Its recovery checker finds no
// recovery for the level and takes its default window, so the recovery here
// is that default rather than the first detect's. The priority and the
// connector are the level's own defaults, not the first detect's: they
// belong to that detect's level.
//
// The platform goes further than this. Its recovery check also looks up the
// trigger for the level, fails, and falls back to a required count of zero,
// so an alert raised at the borrowed level is always still triggering and
// never recovers the ordinary way. That is a side effect of two fallbacks
// meeting, not a reading of the strategy, and it is not reproduced: the
// level here recovers after the default window like any other.
func borrowedLegacyDetect(first legacyDetect) legacyDetect {
	return legacyDetect{
		Level:    first.Level,
		Trigger:  first.Trigger,
		Recovery: json.RawMessage(fmt.Sprintf(`{"check_window":%d}`, legacyDefaultRecoveryWindows)),
	}
}

// dispositionDetailMaxBytes bounds the text a disposition carries: enough
// for a decoder's sentence, not for a document.
const dispositionDetailMaxBytes = 256

// dispositionDetail bounds a refusal's text for the audit.
func dispositionDetail(text string) string {
	if len(text) <= dispositionDetailMaxBytes {
		return text
	}
	return text[:dispositionDetailMaxBytes]
}

type ObjectDisposition struct {
	SourceID    string
	Scope       string
	LevelID     uint32
	Disposition Disposition
	Reason      string
	// FieldPath is where in the strategy document the refusal happened, as the
	// compiler reported it. The compiler has always known; it was dropped on
	// the way out, and a reader was left with a reason word for a document of
	// a few hundred keys. One deployment's 327 LEVEL_INVALID strategies all
	// came from the same field, and finding out which took compiling the
	// documents again offline. Empty when the refusal is not about a field.
	FieldPath string
	// Detail is what the refusal said about the field, in the compiler's
	// words and bounded, when a word and a path are not enough to act on:
	// "query interval is invalid" beside items[0].query_configs[1] is what a
	// strategy owner can fix; QUERY_CONFIG_INVALID alone is not. Empty for
	// every refusal that carries no text. Omitted when empty, so a published
	// audit written before the field keeps its bytes.
	Detail string `json:",omitempty"`
	// AbsentSince is when a strategy under PENDING_REMOVAL was first found
	// absent from the observed active set, in Unix seconds; the removal
	// grace is measured from it. Zero on every other disposition, and on a
	// PENDING_REMOVAL written by a build before the grace was a period -
	// which the next build reads as absent since now, so a rollout can only
	// lengthen a grace, never cut one short.
	AbsentSince int64 `json:",omitempty"`
}

type Catalog struct {
	ObservationID    string
	SnapshotRevision execution.SnapshotRevision
	QueryGroups      []QueryGroup
	Dispositions     []ObjectDisposition
	// RetainedStaleRevisions counts the last-good Plans this build did not
	// retain because their persisted facts no longer hold under this binary,
	// under either refusal. Zero on every build within one release.
	RetainedStaleRevisions int
	// Retention is what this build's Levels ask the state store to keep,
	// measured off the compiled Levels rather than modelled from the shapes in
	// the strategy documents. It is the only place the whole compiled
	// population is in hand at once, which is what makes it a measurement.
	Retention CatalogRetention
	// ObjectRetention is how long this build's content objects and output
	// contexts are kept once no manifest names them any more: the longest a
	// frozen Slot of any of its Plans may still read them by content, which a
	// Plan evaluated every sixty hours needs for sixty hours. Decided by the
	// deployment's admission, which knows the Slot timings; zero keeps the
	// catalog TTL. It is stored beside the manifest, not in it: the manifest
	// is decoded strictly, and a field an older reader does not know would
	// make it refuse the whole publication.
	ObjectRetention time.Duration
}

// CatalogRetention sums the retained window of every Level the runtime
// executable Catalog accepted.
//
// The two point sums exist to be divided: decision-022 R5 retains a slack
// past the window a Level requires so a recovery can step over a rollout's
// hole, and how much that costs the deployment is RetentionPoints over
// RequiredPoints. It was estimated at between four and twenty-two percent
// depending on which shapes the population actually holds, and the estimate
// could not be narrowed from outside - nothing publishes a strategy's window
// and threshold in bulk. Compiling every Plan is the measurement, and this is
// where every Plan is compiled.
//
// Points, not bytes: bytes are what the retention pool is budgeted in, and
// points are their proxy here. The bytes are read from the store's own
// retained-bytes metric across the release rather than derived from these.
type CatalogRetention struct {
	// RequiredPoints and RetentionPoints are summed over every accepted
	// Level, the no-data Level included, because the store retains its points
	// on the same records.
	RequiredPoints  uint64
	RetentionPoints uint64
	// LevelsWithSlack is the Levels that retain more than they require: the
	// ones both R5 gates admitted. The rest retain exactly their window.
	LevelsWithSlack int
	// The same Levels split by which term of the window dominates, because
	// the cost follows the trigger window while the size follows the sum of
	// both. Two Levels with the same required window cost differently, and a
	// single count of paying Levels hides that.
	LevelsWithSlackWindowDominant   int
	LevelsWithSlackRecoveryDominant int
}

func BuildCatalog(ctx context.Context, request BuildRequest) (Catalog, error) {
	if request.Planner == nil || request.Strategies == nil {
		return Catalog{}, errors.New("alarmd controlplane: incomplete catalog build request")
	}
	planner, compilerIdentity, err := compilerForRound(request.Planner)
	if err != nil {
		return Catalog{}, err
	}
	request.Cache.beginRound(request.OutputProtocol, compilerIdentity, request.TargetSources.key(), request.NoDataPolicy.key())
	observationID, err := deriveObservationID(request.Strategies)
	if err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{ObservationID: observationID, QueryGroups: []QueryGroup{}, Dispositions: []ObjectDisposition{}}
	groups := make(map[execution.QueryGroupIdentity]*QueryGroup)
	// Keyed by strategy and piece: a split strategy is one Plan per piece,
	// and the same piece twice is the duplicate.
	seenPlans := make(map[execution.PlanKey]struct{}, len(request.Strategies))
	lastGood := indexLastGoodPlans(request.LastGood)
	addPlan := func(facts execution.QueryPlanFacts, plan FrozenPlan) error {
		if _, duplicate := seenPlans[plan.Key()]; duplicate {
			return errors.New("alarmd controlplane: duplicate Plan identity")
		}
		seenPlans[plan.Key()] = struct{}{}
		identity, err := deriveQueryGroupIdentity(facts)
		if err != nil {
			return err
		}
		group := groups[identity]
		if group == nil {
			group = &QueryGroup{Identity: identity, QueryPlan: facts}
			groups[identity] = group
		} else if group.QueryPlan.QueryRevision != facts.QueryRevision {
			// Unreachable while the identity reads every query fact: equal
			// identities mean equal facts mean equal revisions. It is kept
			// as the assertion of that, and it names the group and both
			// revisions, because the last time it fired the message said
			// only that it had, and finding out which group cost three
			// releases of a Catalog that was never rebuilt.
			return fmt.Errorf("alarmd controlplane: one query group has conflicting query revisions: "+
				"group %s holds %s and %s", identity, group.QueryPlan.QueryRevision, facts.QueryRevision)
		}
		group.Plans = append(group.Plans, plan)
		return nil
	}
	// A last-good Plan is retained only when its persisted facts still hold
	// under this binary. Two things can have changed since they were
	// published, and they are told apart because the label is what the
	// reader acts on:
	//
	// LAST_GOOD_REVISION_STALE: the facts are valid but the current formula
	// derives another revision from them. Within one formula that cannot
	// happen (the revision is a pure function of the facts); across a change
	// of the formula it happens to every retained Plan at once, and each
	// would then meet a freshly compiled sibling in its group under a
	// different revision -- the conflict addPlan refuses.
	//
	// LAST_GOOD_FACTS_INVALID: the facts no longer pass the rules
	// BuildQueryPlanFacts applies -- a validation tightened since they were
	// published, with the formula untouched. Calling that a stale revision
	// would send the reader to the wrong change.
	//
	// Either way the Plan is not retained: added under an old revision it
	// used to fail the whole Catalog, which is the wrong blast radius for a
	// strategy whose document cannot currently be compiled. It leaves the
	// Catalog, named and counted, until its document compiles again, and
	// the other strategies keep evaluating.
	retainLastGood := func(sourceID string) (bool, error) {
		entry, ok := lastGood[sourceID]
		if !ok {
			return false, nil
		}
		if reason := lastGoodRefusal(entry.facts); reason != "" {
			catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: sourceID, Scope: "PLAN",
				Disposition: DispositionConfigRejected, Reason: reason})
			catalog.RetainedStaleRevisions++
			return false, nil
		}
		if err := addPlan(entry.facts, entry.plan); err != nil {
			return false, err
		}
		return true, nil
	}
	observed := make(map[string]struct{}, len(request.Strategies))
	for _, source := range request.Strategies {
		observed[source.SourceID] = struct{}{}
		if source.SourceDisposition != nil {
			disposition := *source.SourceDisposition
			if disposition.SourceID == "" {
				disposition.SourceID = source.SourceID
			}
			if disposition.SourceID != source.SourceID || disposition.Scope != "STRATEGY" || disposition.Reason == "" ||
				(disposition.Disposition != DispositionSourceIncomplete && disposition.Disposition != DispositionConfigRejected) {
				return Catalog{}, errors.New("alarmd controlplane: invalid source disposition")
			}
			if compiled := compileTargetPlanDocument(source); compiled.refusal != nil {
				catalog.Dispositions = append(catalog.Dispositions, disposition, *compiled.refusal)
				continue
			}
			retained, err := retainLastGood(source.SourceID)
			if err != nil {
				return Catalog{}, err
			}
			if retained && disposition.Disposition == DispositionConfigRejected {
				disposition.Disposition = DispositionStaleConfig
			}
			catalog.Dispositions = append(catalog.Dispositions, disposition)
			continue
		}
		candidate, err := request.Cache.build(ctx, planner, source, request.OutputProtocol, request.TargetSources, request.NoDataPolicy)
		if err != nil {
			if len(candidate.dispositions) > 0 {
				catalog.Dispositions = append(catalog.Dispositions, candidate.dispositions...)
			} else {
				catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionConfigRejected, Reason: "PLAN_INVALID"})
			}
			if shouldRetainLastGood(candidate.dispositions) {
				retained, retainErr := retainLastGood(source.SourceID)
				if retainErr != nil {
					return Catalog{}, retainErr
				}
				if retained {
					markStaleConfig(catalog.Dispositions, source.SourceID)
				}
			}
			continue
		}
		if _, duplicate := seenPlans[candidate.plan.Key()]; duplicate {
			catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionConfigRejected, Reason: "DUPLICATE_STRATEGY_IDENTITY"})
			continue
		}
		if err := addPlan(candidate.facts, candidate.plan); err != nil {
			return Catalog{}, err
		}
		catalog.Dispositions = append(catalog.Dispositions, candidate.dispositions...)
		catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionAccepted})
	}
	// Reaching this point means the upstream strategy id list was read
	// completely; per-source incompleteness is already expressed above through
	// SourceDisposition. A LastGood strategy absent from the observed set
	// keeps executing under PENDING_REMOVAL until it has been absent for the
	// whole grace period, and only then leaves the Catalog with REMOVED. The
	// moment it was first found absent travels on the disposition, so every
	// round of the grace publishes the same audit and the candidate confirms.
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}
	absentSince := indexAbsentSince(request.PreviousDispositions, request.PendingAbsences)
	for sourceID := range lastGood {
		if _, found := observed[sourceID]; found {
			continue
		}
		since, graced := absentSince[sourceID]
		if !graced {
			since = now.Unix()
		}
		if now.Unix()-since >= int64(AbsenceGracePeriod/time.Second) {
			catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: sourceID, Scope: "STRATEGY",
				Disposition: DispositionRemoved, Reason: "ABSENT_FROM_ACTIVE_SET", AbsentSince: since})
			continue
		}
		retained, err := retainLastGood(sourceID)
		if err != nil {
			return Catalog{}, err
		}
		if retained {
			catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: sourceID, Scope: "STRATEGY",
				Disposition: DispositionPendingRemoval, Reason: "REMOVED_FROM_ACTIVE_SET", AbsentSince: since})
		}
	}

	for _, group := range groups {
		sort.Slice(group.Plans, func(i, j int) bool { return lessPlanIdentity(group.Plans[i].Identity, group.Plans[j].Identity) })
		identities := make([]execution.PlanIdentity, len(group.Plans))
		schedules := make([]execution.FrozenPlanSchedule, len(group.Plans))
		for i, plan := range group.Plans {
			identities[i] = plan.Identity
			schedules[i] = execution.FrozenPlanSchedule{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec}
		}
		digest, err := contract.DeriveCanonicalDigestV2("alarmd-query-group-membership-v1", identities)
		if err != nil {
			return Catalog{}, err
		}
		group.MembershipDigest = digest
		scheduleDigest, err := execution.DeriveQueryGroupScheduleRevision(schedules)
		if err != nil {
			return Catalog{}, err
		}
		group.ScheduleRevision = scheduleDigest
		catalog.QueryGroups = append(catalog.QueryGroups, *group)
	}
	sort.Slice(catalog.QueryGroups, func(i, j int) bool { return catalog.QueryGroups[i].Identity < catalog.QueryGroups[j].Identity })
	sort.Slice(catalog.Dispositions, func(i, j int) bool { return lessDisposition(catalog.Dispositions[i], catalog.Dispositions[j]) })
	catalog.SnapshotRevision, err = deriveSnapshotRevision(catalog.QueryGroups)
	if err != nil {
		return Catalog{}, err
	}
	request.Cache.endRound()
	return catalog, nil
}

// compilerForRound resolves the compiler the round compiles with. A
// round-scoped compiler hands out one frozen over its facts as they are now;
// any other compiler is its own round compiler with an empty identity.
func compilerForRound(planner PrimaryQueryCompiler) (PrimaryQueryCompiler, string, error) {
	scoped, ok := planner.(RoundScopedCompiler)
	if !ok {
		return planner, "", nil
	}
	compiler, identity, err := scoped.CompilerForRound()
	if err != nil {
		return nil, "", err
	}
	if compiler == nil {
		return nil, "", errors.New("alarmd controlplane: round-scoped compiler handed out no compiler")
	}
	return compiler, identity, nil
}

// CandidateCache keeps what buildCandidate produced for each source document
// across rounds, keyed by the source facts digest.
//
// Every round used to compile every active strategy again, although the
// document of almost all of them had not changed since the previous round:
// on the shadow deployment that is a thousand compilations per refresh, and
// on the largest target deployment it is tens of thousands and more CPU than
// one leader has. The digest already decides the observation id, so a
// strategy whose digest is unchanged is by definition the same input to the
// compiler, and the compiler is a pure function of that input, the wire
// protocol, what the compiler closes over (its identity) and the binary.
//
// What is cached is the compiler's output before the retention pass. That
// pass takes a copy of the Plan, replaces its slices with fresh ones before
// it appends to any of them, and never writes into the cached copy, which is
// what allows the round's Catalog and the cache to share the compiled Plan
// rather than copy it.
//
// The cache holds exactly the strategies the last successful round saw: a
// strategy that left the active set, or whose document changed and so got
// another digest, is dropped at the end of the round. A round that fails
// part-way leaves the cache as it was.
//
// One round runs at a time by construction (the control leader's refresh
// loop); the mutex only keeps a stray concurrent build from corrupting the
// maps.
type CandidateCache struct {
	mu           sync.Mutex
	protocol     string
	compiler     string
	sources      string
	noDataPolicy string
	entries      map[string]cachedCandidate
	seen         map[string]struct{}
	compiled     int
	reused       int
}

type cachedCandidate struct {
	candidate sourceCandidate
	err       error
}

func NewCandidateCache() *CandidateCache {
	return &CandidateCache{entries: make(map[string]cachedCandidate)}
}

// beginRound opens the round's bookkeeping. The wire protocol and the
// compiler's identity are part of what the compiler closes over, so a change
// of either empties the cache rather than keying entries by them. The
// protocol is set once at assembly and a second value here means a
// misconfiguration worth paying one full round; the compiler identity
// changes when the platform changes a setting the plans are compiled by,
// and the full round is exactly what that change asks for: every strategy
// recompiled under the new setting, so every plan's revision moves and the
// cutover carries the new Catalog out.
func (cache *CandidateCache) beginRound(protocol, compiler, sources, noDataPolicy string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.protocol != protocol || cache.compiler != compiler || cache.sources != sources ||
		cache.noDataPolicy != noDataPolicy {
		cache.entries = make(map[string]cachedCandidate)
		cache.protocol, cache.compiler, cache.sources = protocol, compiler, sources
		cache.noDataPolicy = noDataPolicy
	}
	cache.seen = make(map[string]struct{}, len(cache.entries))
	cache.compiled, cache.reused = 0, 0
}

// build returns what the compiler produces for source, from the cache when an
// earlier round compiled the same document. Errors are cached with the
// candidate: a document the compiler rejects is rejected the same way every
// round, and recompiling it each time only to reject it again is the cost
// this cache exists to remove.
func (cache *CandidateCache) build(ctx context.Context, planner PrimaryQueryCompiler, source SourceStrategy, protocol string, sources TargetSources, policy NoDataPolicy) (sourceCandidate, error) {
	if cache == nil {
		return buildCandidate(ctx, planner, source, protocol, sources, policy)
	}
	digest, err := strategyDigest(source)
	if err != nil {
		return buildCandidate(ctx, planner, source, protocol, sources, policy)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.seen != nil {
		cache.seen[digest] = struct{}{}
	}
	if entry, ok := cache.entries[digest]; ok {
		cache.reused++
		return entry.candidate, entry.err
	}
	candidate, err := buildCandidate(ctx, planner, source, protocol, sources, policy)
	cache.entries[digest] = cachedCandidate{candidate: candidate, err: err}
	cache.compiled++
	return candidate, err
}

// endRound drops every entry the round did not ask for, so the cache holds
// exactly the active strategies and nothing that left or changed.
func (cache *CandidateCache) endRound() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.seen == nil {
		return
	}
	for digest := range cache.entries {
		if _, ok := cache.seen[digest]; !ok {
			delete(cache.entries, digest)
		}
	}
	cache.seen = nil
}

// Stats reports how many strategies the last round compiled and how many it
// took from an earlier round. The two add up to the strategies the round
// asked the compiler about, which excludes the ones the source itself
// reported as incomplete.
func (cache *CandidateCache) Stats() (compiled, reused int) {
	if cache == nil {
		return 0, 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.compiled, cache.reused
}

// Len reports how many compiled strategies the cache holds.
func (cache *CandidateCache) Len() int {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.entries)
}

type lastGoodPlan struct {
	facts execution.QueryPlanFacts
	plan  FrozenPlan
}

func indexLastGoodPlans(snapshot *PublishedSnapshot) map[string]lastGoodPlan {
	result := make(map[string]lastGoodPlan)
	if snapshot == nil {
		return result
	}
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			result[plan.Identity.StrategyID] = lastGoodPlan{facts: group.QueryPlan, plan: plan}
		}
	}
	return result
}

// lastGoodRefusal says why a last-good Plan's persisted facts cannot be
// retained under this binary, or nothing when they can. The two refusals
// are told apart by re-deriving the facts: rules that no longer accept them
// are one thing, a formula that derives another revision from accepted
// facts is the other.
const (
	reasonLastGoodRevisionStale = "LAST_GOOD_REVISION_STALE"
	reasonLastGoodFactsInvalid  = "LAST_GOOD_FACTS_INVALID"
)

func lastGoodRefusal(facts execution.QueryPlanFacts) string {
	persisted := facts.QueryRevision
	facts.QueryRevision = ""
	rebuilt, err := execution.BuildQueryPlanFacts(facts)
	switch {
	case err != nil:
		return reasonLastGoodFactsInvalid
	case persisted == "" || rebuilt.QueryRevision != persisted:
		return reasonLastGoodRevisionStale
	default:
		return ""
	}
}

// indexAbsentSince is when each strategy under grace was first found
// absent: from the published audit's PENDING_REMOVAL dispositions, and from
// the unconfirmed candidate where the audit does not say. A PENDING_REMOVAL
// without a moment - written by a build before the grace was a period - is
// left out, and the caller stamps it absent since now.
func indexAbsentSince(dispositions []ObjectDisposition, pending map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(dispositions)+len(pending))
	for sourceID, since := range pending {
		if sourceID != "" && since > 0 {
			result[sourceID] = since
		}
	}
	for _, disposition := range dispositions {
		if disposition.Scope == "STRATEGY" && disposition.Disposition == DispositionPendingRemoval &&
			disposition.SourceID != "" && disposition.AbsentSince > 0 {
			result[disposition.SourceID] = disposition.AbsentSince
		}
	}
	return result
}

// AbsencesOf is the removal-grace memory of an audit, by source id: what a
// candidate built from it carries into the next round.
func AbsencesOf(dispositions []ObjectDisposition) map[string]int64 {
	absences := indexAbsentSince(dispositions, nil)
	if len(absences) == 0 {
		return nil
	}
	return absences
}

// RetainsLastGoodDefinition says whether a refusal leaves the strategy running
// the last definition that compiled.
//
// One predicate, called from both places that decide it. They used to say it
// in two ways that happened to agree: the source audit retained under anything
// but UNSUPPORTED, and the executable catalog retained under CONFIG_REJECTED,
// which were the only two dispositions a compiler terminal could carry. The
// first terminal filed as something else - a snapshot that had not arrived,
// which is neither the definition's fault nor this build's - would have been
// retained by one and dropped by the other, and dropped means the strategy
// stops detecting for as long as the source is missing a piece.
//
// UNSUPPORTED is the one that does not retain, and for a reason that does not
// generalise: this build cannot evaluate that definition at all, so an older
// one of it is not a safer answer, it is the same refusal one revision back.
func RetainsLastGoodDefinition(disposition Disposition) bool {
	return disposition != DispositionUnsupported
}

func shouldRetainLastGood(dispositions []ObjectDisposition) bool {
	for _, disposition := range dispositions {
		if !RetainsLastGoodDefinition(disposition.Disposition) {
			return false
		}
	}
	return true
}

func markStaleConfig(dispositions []ObjectDisposition, sourceID string) {
	for index := range dispositions {
		if dispositions[index].SourceID == sourceID && dispositions[index].Disposition == DispositionConfigRejected {
			dispositions[index].Disposition = DispositionStaleConfig
		}
	}
}

func lessDisposition(left, right ObjectDisposition) bool {
	if left.SourceID != right.SourceID {
		return left.SourceID < right.SourceID
	}
	if left.Scope != right.Scope {
		return left.Scope < right.Scope
	}
	if left.LevelID != right.LevelID {
		return left.LevelID < right.LevelID
	}
	if left.Disposition != right.Disposition {
		return left.Disposition < right.Disposition
	}
	return left.Reason < right.Reason
}

func deriveSnapshotRevision(groups []QueryGroup) (execution.SnapshotRevision, error) {
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-strategy-snapshot-v1", groups)
	return execution.SnapshotRevision(digest), err
}

type sourceCandidate struct {
	facts        execution.QueryPlanFacts
	plan         FrozenPlan
	dispositions []ObjectDisposition
}

func buildCandidate(ctx context.Context, planner PrimaryQueryCompiler, source SourceStrategy, outputProtocol string, sources TargetSources, policy NoDataPolicy) (sourceCandidate, error) {
	candidate := sourceCandidate{}
	targetPlanDocument := compileTargetPlanDocument(source)
	if targetPlanDocument.refusal != nil {
		return sourceCandidate{dispositions: []ObjectDisposition{*targetPlanDocument.refusal}}, errors.New("alarmd controlplane: target_plan refused: " + targetPlanDocument.refusal.Reason)
	}
	if targetPlanDocument.plan != nil && len(targetPlanDocument.plan.DynamicGroups) > 0 && !sources.DynamicGroups {
		// The plan reads a dynamic group cache this deployment does not
		// render a prefix for. Withheld here, once, as a deployment fact;
		// the Plans that reference no group are untouched.
		refusal := ObjectDisposition{
			SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported,
			Reason: "DYNAMIC_GROUP_SOURCE_UNCONFIGURED", FieldPath: fmt.Sprintf("items[%d].target_plan.dynamic_groups", targetPlanDocument.position),
		}
		return sourceCandidate{dispositions: []ObjectDisposition{refusal}}, errors.New("alarmd controlplane: target_plan references dynamic groups and the deployment renders no dynamic group cache prefix")
	}
	if err := source.Identity.validate(); err != nil {
		return sourceCandidate{}, err
	}
	legacy, err := decodeLegacyStrategy(source.Document)
	if err != nil {
		return sourceCandidate{}, err
	}
	if strconv.FormatInt(legacy.BusinessID, 10) != source.Identity.BusinessID {
		return sourceCandidate{}, errors.New("BUSINESS_IDENTITY_MISMATCH")
	}
	if source.SourceID == "" || source.SourceID != strconv.FormatInt(legacy.ID, 10) {
		return sourceCandidate{}, errors.New("SOURCE_IDENTITY_MISMATCH")
	}
	// Priority is how the platform's alert pipeline arbitrates between the
	// strategies of one priority group: the highest one watching a dimension
	// keeps the lower ones from detecting it, and closes their alerts. That
	// is coordination across strategies, not a fact about this one, so the
	// strategy is compiled as the standalone strategy it is and the Plan is
	// named as having had its priority ignored.
	priorityIgnored := legacyPriorityApplies(legacy)
	if len(legacy.Items) > 1 {
		return sourceCandidate{dispositions: []ObjectDisposition{{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported, Reason: "UNSUPPORTED_MULTI_ITEM_STRATEGY"}}}, errors.New("alarmd controlplane: multiple Item strategies unsupported in phase two")
	}
	item := legacy.Items[0]
	if item.ID <= 0 || item.QueryMD5 == "" || item.Expression == "" || len(item.QueryConfigs) == 0 || len(item.Algorithms) == 0 {
		return candidate, errors.New("INCOMPLETE_SERIES_THRESHOLD_ITEM")
	}
	// The monitoring target is resolved before anything else is compiled, and
	// a target this compiler cannot honour is reported as an unsupported
	// capability rather than a rejected configuration. The difference decides
	// the outcome: a rejected configuration retains the last good Plan, and
	// that Plan predates the target filter, so the strategy would go on
	// alerting outside its target with nothing to show for it.
	var targetScope *contract.TargetScopeV2
	var targetPlan *contract.TargetPlanV1
	switch {
	case targetPlanDocument.present:
		// The new protocol is the whole target: the item's old target, whatever
		// shape it now has, is display data and is not read.
		targetPlan = targetPlanDocument.plan
	case item.Target.selection:
		// The strategy's target has moved to the selection protocol without a
		// target_plan beside it. That is the writer switching protocols out
		// of order, and it is refused by name rather than compiled as a
		// strategy with no target, or kept on the last Plan of the old one.
		return sourceCandidate{dispositions: []ObjectDisposition{{
			SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported,
			Reason: "TARGET_PLAN_MISSING", FieldPath: "items[0].target",
		}}}, errors.New("TARGET_PLAN_MISSING: the item's target is a selection document and no target_plan accompanies it")
	case item.Target.unreadable:
		// A target this reader cannot make out is refused the way a target
		// value it cannot read is, and for the same reason: kept on the last
		// good Plan, the strategy would go on alerting on a target nobody
		// can show it was pointed at.
		return sourceCandidate{dispositions: []ObjectDisposition{{
			SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported,
			Reason: "UNSUPPORTED_TARGET_SCOPE", FieldPath: "items[0].target",
		}}}, errors.New("TARGET_SCOPE_UNSUPPORTED: the item's target could not be decoded")
	default:
		scope, err := compileTargetScope(item.Target.groups, item.QueryConfigs)
		if err != nil {
			return sourceCandidate{dispositions: []ObjectDisposition{{
				SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported,
				Reason: targetScopeDispositionReason(err),
			}}}, err
		}
		targetScope = scope
	}
	primaryExpression, identityFields := primaryQueryContract(item)
	functions := append([]json.RawMessage(nil), item.Functions...)
	if itemHasAlgorithm(item, strategy.DetectorKindOsRestart) {
		functions = nil
	}
	querySource := PrimaryQuerySource{
		Identity: source.Identity, StrategyID: source.SourceID, ItemID: strconv.FormatInt(item.ID, 10),
		TimeDelaySeconds: item.TimeDelay, QueryMD5: item.QueryMD5, Expression: primaryExpression, IdentityFields: identityFields,
		Functions: functions, QueryConfigs: append([]json.RawMessage(nil), item.QueryConfigs...),
	}
	facts, err := planner.CompilePrimaryQuery(ctx, querySource)
	if err != nil {
		var compileFailure *QueryPlanCompileError
		if errors.As(err, &compileFailure) && compileFailure.Disposition != "" && compileFailure.Reason != "" {
			disposition := ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: compileFailure.Disposition, Reason: compileFailure.Reason}
			if compileFailure.FieldPath != "" {
				disposition.FieldPath = "items[0]." + compileFailure.FieldPath
			}
			if compileFailure.Err != nil {
				disposition.Detail = dispositionDetail(compileFailure.Err.Error())
			}
			candidate.dispositions = append(candidate.dispositions, disposition)
		}
		return candidate, fmt.Errorf("QUERY_PLAN_INVALID: %w", err)
	}
	if err := validateQueryIdentity(source.Identity, facts); err != nil {
		return candidate, err
	}
	compiledInputs := compiledPlanInputs{primary: facts}
	for _, label := range itemDataTypes(item) {
		if label == "log" || label == "event" {
			compiledInputs.missingHistoryAsZero = true
		}
	}
	if itemHasAlgorithm(item, strategy.DetectorKindOsRestart) {
		dependencyCompiler, ok := planner.(AlgorithmDependencyQueryCompiler)
		if !ok {
			return candidate, errors.New("ALGORITHM_DEPENDENCY_QUERY_COMPILER_UNAVAILABLE")
		}
		history, historyErr := dependencyCompiler.CompileAlgorithmDependencyQuery(ctx, querySource, "a")
		if historyErr != nil {
			return candidate, fmt.Errorf("QUERY_PLAN_INVALID: %w", historyErr)
		}
		if err := validateQueryIdentity(source.Identity, history); err != nil {
			return candidate, err
		}
		compiledInputs.osRestartHistory = &history
	}
	plan, compiled, dispositions, err := compilePlan(
		legacy, item, source.Identity, facts.Normalization.DatasetContract, source.SourceID, &compiledInputs, targetScope, targetPlan, policy,
	)
	if err != nil {
		candidate.dispositions = append(candidate.dispositions, dispositions...)
		return candidate, err
	}
	// The protocol is decided here, once, and frozen with the Plan: a Slot that
	// is retried must not change wire format between attempts.
	//
	// A compatibility context is attached whenever the Plan will publish that
	// protocol - which under a forced legacy choice includes strategies that do
	// have a revision, because the context is what the conversion reads. Under a
	// forced native choice a strategy without a revision is refused instead:
	// the alert it would open is identified by a fingerprint derived from that
	// revision, so there is nothing to send it as, and sending it the other way
	// is the silent fallback the choice exists to prevent.
	format, honoured := resolveWireFormat(outputProtocol, plan.StrategyRef.SnapshotRevision)
	if !honoured {
		candidate.dispositions = append(candidate.dispositions, ObjectDisposition{
			SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionConfigRejected,
			Reason: "OUTPUT_PROTOCOL_REQUIRES_STRATEGY_REVISION",
		})
		return candidate, errors.New("OUTPUT_PROTOCOL_REQUIRES_STRATEGY_REVISION")
	}
	plan.WireFormat = format
	// Beside the wire format and for the same reason: the sink writes one
	// event at a time with no Plan in hand, and no record says whether it came
	// from a metric, a log or an event stream.
	plan.SignalType = contract.SignalTypeForDataTypes(itemDataTypes(item))
	// The alert consumer keys alerts by a fingerprint built from the output
	// identity; its sink refuses an envelope without one, and refuses the
	// whole batch with it. The identity is set with the revision above and
	// the protocol requires the revision, so this cannot fire on this path;
	// it pins the pairing where both halves are decided, so that a change
	// to either shows up here and not as a Slot that can never write.
	if format == contract.WireFormatStandardRawEvent && plan.OutputIdentity == nil {
		candidate.dispositions = append(candidate.dispositions, ObjectDisposition{
			SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionConfigRejected,
			Reason: "OUTPUT_PROTOCOL_REQUIRES_OUTPUT_IDENTITY",
		})
		return candidate, errors.New("OUTPUT_PROTOCOL_REQUIRES_OUTPUT_IDENTITY")
	}
	if format == contract.WireFormatPythonCompatible {
		plan.LegacyOutput = &contract.LegacyOutputContext{DynamicDimensions: facts.Normalization.DatasetContract.DynamicDimensions, Strategy: legacyOutputStrategyDocument(source.Document), DimensionFields: append([]string{}, facts.Normalization.DatasetContract.IdentityFields...), ItemID: strconv.FormatInt(item.ID, 10)}
	}
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan)
	if err != nil {
		return sourceCandidate{}, err
	}
	candidate.facts = facts
	candidate.plan = FrozenPlan{
		Identity: execution.PlanIdentity{TenantID: source.Identity.TenantID, BusinessID: source.Identity.BusinessID, StrategyID: source.SourceID},
		Plan:     plan, PlanRevision: revision,
		ScheduleSpec: compiled.ScheduleSpec, ScheduleRevision: compiled.ScheduleRevision,
		NoDataSuspended:      compiled.NoDataSuspended,
		RequirementTemplates: compiledInputs.requirements, QueryPlans: compiledInputs.queryPlans,
	}
	candidate.dispositions = append(candidate.dispositions, dispositions...)
	if priorityIgnored {
		candidate.dispositions = append(candidate.dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionConfigNormalized, Reason: ReasonPriorityIgnored})
	}
	return candidate, nil
}

// legacyOutputStrategyDocument is the strategy document a Python-compatible
// Plan carries for its output, less what the platform's strategy cache writes
// differently from one refresh to the next without the strategy changing.
//
// The document is copied into the Plan, so every byte of it is part of the
// snapshot revision, and the cache rewrites two things round after round: an
// invalid strategy's invalid_type, which its two refresh paths write as empty
// and as the reason in turn, and the order of the hosts a dynamic target
// resolves to. Measured on the verification deployment, every one of the
// Plans that differed between two readings minutes apart differed only
// there, and each such round published a whole new catalog for content that
// executes exactly as before.
//
// is_invalid and invalid_type are dropped: nothing that reads the output
// snapshot reads either. Each target condition's value list is put in the
// order of its elements' canonical bytes: a target matches as a set, and the
// elements take several shapes (a host, a topology node, a dynamic group), so
// no one field orders them all. No other list is touched: the order of a
// condition list or a dimension list means something to the platform, which
// compares an alert's snapshot with the current strategy in order. A document
// that does not have the expected shape is carried as it was.
func legacyOutputStrategyDocument(document json.RawMessage) json.RawMessage {
	verbatim := append(json.RawMessage(nil), document...)
	var fields map[string]json.RawMessage
	if json.Unmarshal(document, &fields) != nil || fields == nil {
		return verbatim
	}
	delete(fields, "is_invalid")
	delete(fields, "invalid_type")
	if raw, present := fields["items"]; present {
		if items, ok := orderedTargetValues(raw); ok {
			fields["items"] = items
		}
	}
	normalized, err := marshalJSONUnescaped(fields)
	if err != nil {
		return verbatim
	}
	return normalized
}

// orderedTargetValues is the items list with each target condition's value
// list in canonical order; false leaves the list as it was.
func orderedTargetValues(raw json.RawMessage) (json.RawMessage, bool) {
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	for _, item := range items {
		targetRaw, present := item["target"]
		if !present {
			continue
		}
		var target [][]map[string]json.RawMessage
		if json.Unmarshal(targetRaw, &target) != nil {
			continue
		}
		for _, group := range target {
			for _, condition := range group {
				valuesRaw, present := condition["value"]
				if !present {
					continue
				}
				var values []json.RawMessage
				if json.Unmarshal(valuesRaw, &values) != nil {
					continue
				}
				keys := make([]string, len(values))
				canonical := true
				for index, value := range values {
					key, err := contract.CanonicalJSONV2(value)
					if err != nil {
						canonical = false
						break
					}
					keys[index] = string(key)
				}
				if !canonical {
					continue
				}
				order := make([]int, len(values))
				for index := range order {
					order[index] = index
				}
				sort.SliceStable(order, func(left, right int) bool { return keys[order[left]] < keys[order[right]] })
				sorted := make([]json.RawMessage, len(values))
				for index, from := range order {
					sorted[index] = values[from]
				}
				encoded, err := marshalJSONUnescaped(sorted)
				if err != nil {
					return nil, false
				}
				condition["value"] = encoded
			}
		}
		encoded, err := marshalJSONUnescaped(target)
		if err != nil {
			return nil, false
		}
		item["target"] = encoded
	}
	encoded, err := marshalJSONUnescaped(items)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// marshalJSONUnescaped is json.Marshal without HTML escaping, so a strategy
// name with an ampersand reads back as the platform wrote it.
func marshalJSONUnescaped(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})), nil
}

// legacyPriorityApplies is the platform's own test for a strategy taking part
// in priority arbitration: a priority is set, zero included, and the strategy
// belongs to a priority group. Either one alone arbitrates nothing there, so
// it is not named here either.
func legacyPriorityApplies(legacy legacyStrategy) bool {
	value := strings.TrimSpace(string(legacy.Priority))
	return value != "" && value != "null" && legacy.PriorityGroupKey != ""
}

// itemUnit is the item's data unit, derived the way Python derives it: the
// first non-empty unit among the item's query configs.
//
// Python builds the same value with list(set(...))[0] over the non-empty ones,
// which is unordered when the configs disagree. This takes the first in the
// stored order instead, so one document always compiles to one Plan. The
// disagreement itself is not refused here -- Python does not refuse it, and a
// new refusal would take strategies out that run today.
// itemDataTypes is the data_type_label of every query config of one item, in
// the stored order, including the empty ones.
//
// The empty ones are kept rather than skipped: a config this build could not
// decode is a config whose data type is unknown, and dropping it would let the
// rest agree on a signal type the item may not have. The one caller that only
// asks "is any of them a log or an event" is unaffected either way.
func itemDataTypes(item legacyItem) []string {
	labels := make([]string, 0, len(item.QueryConfigs))
	for _, raw := range item.QueryConfigs {
		config, _ := decodeLegacyQueryConfig(raw)
		labels = append(labels, config.DataTypeLabel)
	}
	return labels
}

func itemUnit(item legacyItem) string {
	for _, raw := range item.QueryConfigs {
		var config struct {
			Unit string `json:"unit"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			continue
		}
		if config.Unit != "" {
			return config.Unit
		}
	}
	return ""
}

func primaryQueryContract(item legacyItem) (string, []string) {
	expression := item.Expression
	if itemHasAlgorithm(item, strategy.DetectorKindOsRestart) {
		expression = "a <= 3600"
	}
	if itemHasAlgorithm(item, strategy.DetectorKindProcPort) {
		return expression, []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}
	}
	return expression, nil
}

func itemHasAlgorithm(item legacyItem, kind string) bool {
	for _, algorithm := range item.Algorithms {
		if algorithm.Type == kind {
			return true
		}
	}
	return false
}

func (identity SourceIdentity) validate() error {
	if identity.TenantID == "" || identity.BusinessID == "" || identity.SpaceScope == "" {
		return errors.New("alarmd controlplane: tenant, business and space facts are required")
	}
	business, err := strconv.ParseInt(identity.BusinessID, 10, 64)
	if err != nil || business == 0 || strconv.FormatInt(business, 10) != identity.BusinessID {
		return errors.New("alarmd controlplane: business identity must be canonical non-zero signed decimal")
	}
	return nil
}

func validateQueryIdentity(identity SourceIdentity, facts execution.QueryPlanFacts) error {
	if err := facts.Validate(); err != nil {
		return err
	}
	if facts.TenantID != identity.TenantID || facts.BusinessID != identity.BusinessID || facts.SpaceScope != identity.SpaceScope {
		return errors.New("alarmd controlplane: query plan identity differs from control facts")
	}
	return nil
}

func lessPlanIdentity(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

type legacyStrategy struct {
	EffectiveTimeSnapshot json.RawMessage `json:"effective_time_snapshot,omitempty"`
	ID                    int64           `json:"id"`
	BusinessID            int64           `json:"bk_biz_id"`
	UpdateTime            json.Number     `json:"update_time"`
	SnapshotRevision      json.RawMessage `json:"strategy_revision,omitempty"`
	Priority              json.RawMessage `json:"priority"`
	PriorityGroupKey      string          `json:"priority_group_key"`
	Labels                []string        `json:"labels"`
	Items                 []legacyItem    `json:"items"`
	Detects               []legacyDetect  `json:"detects"`
}
type legacyItem struct {
	TimeDelay    int64             `json:"time_delay"`
	ID           int64             `json:"id"`
	QueryMD5     string            `json:"query_md5"`
	Expression   string            `json:"expression"`
	Functions    []json.RawMessage `json:"functions"`
	QueryConfigs []json.RawMessage `json:"query_configs"`
	Algorithms   []legacyAlgorithm `json:"algorithms"`
	// Unit is deliberately absent from this struct. The strategy cache has no
	// unit on an item: Python's Item.unit is a derived property that walks
	// query_configs and takes the first non-empty one, so reading a "unit" key
	// here found nothing on every strategy the platform stores. Every
	// threshold algorithm configured with a unit prefix then compiled against
	// an empty data unit, failed to find the prefix in the identity unit's
	// suffix table, and took the whole level out as LEVEL_INVALID. A full
	// reading of one deployment put 327 strategies -- 11.5% of all of them --
	// behind that one missing key, none of them evaluating at all, and the
	// only symptom was a count of withheld objects that named no field.
	//
	// Read it with itemUnit.
	// Target is the strategy's monitoring scope. It was silently ignored here
	// until 2026-09-09, which is how alarmd came to alert on hosts outside
	// every scoped strategy's target while Python filtered them out.
	Target legacyTarget `json:"target"`
	// NoDataConfig is the item's no-data setting. A pointer so that "the
	// strategy cache carried no section" is distinguishable from "it carried
	// one with everything at zero"; the two mean different things and the
	// second is a malformed entry rather than a disabled item.
	NoDataConfig json.RawMessage `json:"no_data_config"`
}

// legacyTarget is the item's target as the strategy cache stores it: the
// platform's list of condition groups, or, once the writer has moved the
// strategy to the selection protocol, an object, or something this reader
// cannot make out. None of the three fails the decode of the whole
// strategy: a decode failure retains the strategy's last good Plan, and
// that Plan was compiled from the old target - the one outcome a target
// change must never produce. What each shape means is decided where the
// target is compiled, and only when no target_plan stands in for it.
type legacyTarget struct {
	groups [][]legacyTargetCondition
	// selection is true when the target was an object: the new selection
	// protocol, which this compiler reads only through target_plan.
	selection bool
	// unreadable is true when the target was neither absent, an object nor
	// a list this reader could decode.
	unreadable bool
}

func (target *legacyTarget) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		*target = legacyTarget{}
		return nil
	}
	if strings.HasPrefix(trimmed, "{") {
		*target = legacyTarget{selection: true}
		return nil
	}
	var groups [][]legacyTargetCondition
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&groups); err != nil {
		*target = legacyTarget{unreadable: true}
		return nil
	}
	*target = legacyTarget{groups: groups}
	return nil
}

// legacyNoDataConfig is the no_data_config the strategy cache stores.
//
// The whole section is raw, and every field inside it is raw again, because the
// type it arrives in is open at every position: the SaaS serializer stores
// no_data_config as a bare DictField with no field-level validation, and the
// backend reads it with int(), a truthiness test and a list comprehension, none
// of which care what JSON type the value had.
//
// The reason to be raw is not tolerance for its own sake. A narrower Go type
// here does not disable no-data when it meets a shape it did not expect - it
// fails Decode for the whole strategy document, and the item's threshold
// detection stops with it, under an error naming a field the vanished strategy
// had nothing to do with. Typing the numbers alone was not enough: "is_enabled":
// "true" and "agg_dimension": [1] kept the old blast radius until this became
// raw too. Every shape problem now lands on NO_DATA_CONFIG_INVALID, which names
// the item and leaves the rest of the catalogue alone.
type legacyNoDataConfig struct {
	IsEnabled    json.RawMessage   `json:"is_enabled"`
	Continuous   json.RawMessage   `json:"continuous"`
	AggDimension []json.RawMessage `json:"agg_dimension"`
	Level        json.RawMessage   `json:"level"`
	// TrackingHorizonSeconds is this item's own horizon, overriding the
	// deployment's. Absent means the deployment's applies; a stated zero is an
	// opt-out and is not the same as absent.
	TrackingHorizonSeconds json.RawMessage `json:"tracking_horizon_seconds"`
}

// legacyNoDataEnabled reads is_enabled the way the backend's truthiness test
// does: a JSON true, a non-zero number, or a non-empty string that is not one
// of Python's falsey spellings. A shape it cannot read is an error rather than
// a silent "off", because "off" here is a strategy that stops detecting no-data
// without saying so.
func legacyNoDataEnabled(raw json.RawMessage) (bool, error) {
	text := strings.TrimSpace(string(raw))
	switch text {
	case "", "null", "false", "0", `""`:
		return false, nil
	case "true":
		return true, nil
	}
	if unquoted, err := strconv.Unquote(text); err == nil {
		// Python's `if no_data_config.get("is_enabled")` is true for any
		// non-empty string, "false" included. Following that literally is the
		// point: this reads a store the backend also reads.
		return strings.TrimSpace(unquoted) != "", nil
	}
	if number, err := json.Number(text).Float64(); err == nil {
		return number != 0, nil
	}
	return false, fmt.Errorf("no_data_config is_enabled %s is not a value this can read", text)
}

// legacyNoDataDimension reads one agg_dimension entry. The backend puts these
// straight into a set and compares them against dimension names, which are
// strings; a number there is a name no series can carry, and saying so by item
// is better than losing the strategy to a decode error.
func legacyNoDataDimension(raw json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(raw))
	if unquoted, err := strconv.Unquote(text); err == nil {
		return unquoted, nil
	}
	return "", fmt.Errorf("no_data_config agg_dimension entry %s is not a dimension name", text)
}

// legacyNoDataNumber reads one of that section's numbers the way the backend's
// int() does: a JSON number is truncated toward zero, a string is parsed as an
// integer and refused if it is not one. int("5.9") raises in Python, so "5.9"
// is refused here, while int(5.9) is 5 and 5.9 is 5 here.
func legacyNoDataNumber(field string, raw json.RawMessage) (uint32, bool, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, false, nil
	}
	if quoted, err := strconv.Unquote(text); err == nil {
		text = strings.TrimSpace(quoted)
		if text == "" {
			return 0, false, nil
		}
	}
	value := json.Number(text)
	if parsed, err := value.Int64(); err == nil {
		if parsed < 0 || parsed > math.MaxUint32 {
			return 0, false, fmt.Errorf("no_data_config %s %s is outside the supported range", field, text)
		}
		return uint32(parsed), true, nil
	}
	// A float reaches here because Int64 refuses the fraction. Python truncates
	// it; a quoted float is a different thing and Python raises on it, but the
	// decoder has already erased the quotes, so both arrive the same way and
	// both are truncated. The difference costs nothing a validated value would
	// notice: it admits "5.9" where Python raises, and the alternative is
	// refusing 5.9 where Python detects on 5.
	parsed, err := value.Float64()
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false, fmt.Errorf("no_data_config %s %q is not a number", field, text)
	}
	truncated := math.Trunc(parsed)
	if truncated < 0 || truncated > math.MaxUint32 {
		return 0, false, fmt.Errorf("no_data_config %s %s is outside the supported range", field, text)
	}
	return uint32(truncated), true, nil
}

// defaultNoDataLevel is the backend's read-side default for a level the item
// omits: mixins/nodata.py reads .get("level", NO_DATA_LEVEL). continuous has no
// counterpart here on purpose - the same dict literal that defaults level
// subscripts continuous, so an item omitting it detects nothing rather than
// detecting on a default.
const defaultNoDataLevel uint32 = 2

// noDataRosterUnsupported names the combination of target shape and no-data
// dimensions this build cannot derive an expected set for, or "" when it can.
//
// It asks the derivation rather than repeating it. The Slot builds the roster
// from the same two frozen facts, and the point of refusing here is that the
// Slot never has to - which only holds while both reach the same verdict. A
// second predicate that agrees today is a predicate that can drift tomorrow,
// and the drift is silent in both directions: a Plan that errors every round,
// or a Plan that quietly expects nothing.
func noDataRosterUnsupported(scope *contract.TargetScopeV2, plan *contract.TargetPlanV1, config *contract.NoDataConfigV1) string {
	if config == nil {
		return ""
	}
	if _, err := nodata.ClassifyTarget(scope, plan, config.AggDimension); err != nil {
		var unsupported *nodata.RosterUnsupportedError
		if errors.As(err, &unsupported) {
			return unsupported.Reason
		}
		return err.Error()
	}
	return ""
}

// frozenNoDataConfig returns the section to freeze on the Plan, or nil when the
// item does not detect no-data. An item that is enabled but whose setting
// cannot be validated is an error rather than a silent disable: the strategy
// asked for the detection, and dropping it quietly is the failure mode that
// looks like nothing happened.
func frozenNoDataConfig(item legacyItem, policy NoDataPolicy) (*contract.NoDataConfigV1, error) {
	raw := strings.TrimSpace(string(item.NoDataConfig))
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var source legacyNoDataConfig
	if err := json.Unmarshal(item.NoDataConfig, &source); err != nil {
		return nil, fmt.Errorf("alarmd controlplane: item %d no_data_config: %w", item.ID, err)
	}
	enabled, err := legacyNoDataEnabled(source.IsEnabled)
	if err != nil {
		return nil, fmt.Errorf("alarmd controlplane: item %d %w", item.ID, err)
	}
	if !enabled {
		return nil, nil
	}
	dimensions := make([]string, 0, len(source.AggDimension))
	for _, entry := range source.AggDimension {
		dimension, err := legacyNoDataDimension(entry)
		if err != nil {
			return nil, fmt.Errorf("alarmd controlplane: item %d %w", item.ID, err)
		}
		dimensions = append(dimensions, dimension)
	}
	config := &contract.NoDataConfigV1{AggDimension: dimensions, Level: defaultNoDataLevel}
	// Continuous stays zero when the item omits it, and Validate refuses that.
	// See defaultNoDataLevel for why this one is not defaulted.
	continuous, stated, err := legacyNoDataNumber("continuous", source.Continuous)
	if err != nil {
		return nil, fmt.Errorf("alarmd controlplane: item %d %w", item.ID, err)
	}
	if stated {
		config.Continuous = continuous
	}
	level, stated, err := legacyNoDataNumber("level", source.Level)
	if err != nil {
		return nil, fmt.Errorf("alarmd controlplane: item %d %w", item.ID, err)
	}
	if stated {
		config.Level = level
	}
	// The effective horizon is frozen here, so a Slot reads one number and
	// never has to know whether it came from the item or the deployment. An
	// item that states its own uses it; stating nothing inherits the
	// deployment's.
	//
	// A stated zero is refused rather than taken as an opt-out. The contract
	// is to give every group a finite horizon, so there is no opting out to
	// express, and the horizon has no value meaning "forever" for a zero to
	// stand in for. An item that wants the platform's horizon says nothing,
	// which is already how it is said - so a written zero is a mistake worth
	// naming where the configuration is checked, rather than a silent switch
	// back to the unbounded tracking this whole decision exists to end.
	config.TrackingHorizonSeconds = policy.TrackingHorizonSeconds
	if policy.TrackingHorizonSeconds > 0 {
		config.TrackingHorizonSource = contract.NoDataHorizonSourcePlatform
	}
	horizon, stated, err := legacyNoDataNumber("tracking_horizon_seconds", source.TrackingHorizonSeconds)
	if err != nil {
		return nil, fmt.Errorf("alarmd controlplane: item %d %w", item.ID, err)
	}
	if stated {
		if horizon == 0 {
			return nil, fmt.Errorf("alarmd controlplane: item %d no_data_config tracking_horizon_seconds "+
				"must be a positive number of seconds; remove the field to inherit the platform's", item.ID)
		}
		// The source is frozen beside the number: a reader of the Plan does
		// not have to compare it against the platform's current value to
		// know whose it is.
		config.TrackingHorizonSeconds = int64(horizon)
		config.TrackingHorizonSource = contract.NoDataHorizonSourceStrategy
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("alarmd controlplane: item %d no_data_config: %w", item.ID, err)
	}
	return config, nil
}

type legacyAlgorithm struct {
	Level      uint32          `json:"level"`
	Type       string          `json:"type"`
	UnitPrefix string          `json:"unit_prefix"`
	Config     json.RawMessage `json:"config"`
}
type legacyDetect struct {
	Level     uint32          `json:"level"`
	Priority  *uint32         `json:"priority"`
	Connector string          `json:"connector"`
	Trigger   legacyTrigger   `json:"trigger_config"`
	Recovery  json.RawMessage `json:"recovery_config"`
}
type legacyTrigger struct {
	Count       uint32          `json:"count"`
	CheckWindow uint32          `json:"check_window"`
	Uptime      json.RawMessage `json:"uptime"`
}
type legacyRecovery struct {
	CheckWindow uint32 `json:"check_window"`
}

func decodeLegacyStrategy(document json.RawMessage) (legacyStrategy, error) {
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.UseNumber()
	var value legacyStrategy
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("alarmd controlplane: decode strategy: %w", err)
	}
	if value.ID <= 0 || value.BusinessID == 0 || len(value.Items) == 0 {
		return value, errors.New("alarmd controlplane: incomplete legacy strategy")
	}
	if len(value.SnapshotRevision) != 0 {
		var revision int64
		if err := json.Unmarshal(value.SnapshotRevision, &revision); err != nil || revision <= 0 {
			return value, errors.New("alarmd controlplane: strategy_revision must be a positive int64 JSON number")
		}
	} else {
		var snapshot struct {
			UpdateTime int64 `json:"update_time"`
		}
		if err := json.Unmarshal(document, &snapshot); err != nil || snapshot.UpdateTime <= 0 {
			return value, errors.New("alarmd controlplane: legacy output requires a positive integer update_time")
		}
	}
	return value, nil
}

// compiledTargetPlan is what the target_plan document of one strategy came
// to: the frozen form when it read, the disposition refusing it when it did
// not, and neither when the strategy carries no such field.
type compiledTargetPlan struct {
	present  bool
	plan     *contract.TargetPlanV1
	refusal  *ObjectDisposition
	position int
}

// compileTargetPlanDocument reads the strategy's target_plan, if it has one.
//
// Presence selects the new target protocol, even for null or malformed
// values, and it is read before the legacy decoder sees the document: the
// item's display-only target may by then be an object, which the legacy
// decoder rejects as a configuration error and would otherwise retain the
// old target's Plan. A target_plan that does not read is refused by name and
// field, and the strategy is never compiled from its old target instead.
// Ordinary sources are checked inside the candidate cache; incomplete
// sources also check before their separate last-good retention path.
func compileTargetPlanDocument(source SourceStrategy) compiledTargetPlan {
	var document struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(source.Document, &document); err != nil {
		return compiledTargetPlan{} // An unreadable source keeps its existing failure semantics.
	}
	for index, raw := range document.Items {
		var item struct {
			TargetPlan   json.RawMessage   `json:"target_plan"`
			QueryConfigs []json.RawMessage `json:"query_configs"`
		}
		if err := json.Unmarshal(raw, &item); err != nil || len(item.TargetPlan) == 0 {
			continue
		}
		field := fmt.Sprintf("items[%d].target_plan", index)
		plan, refusal := targetplan.Decode(item.TargetPlan, targetplan.Options{ObjectIdentities: objectIdentityPairs(item.QueryConfigs)})
		if refusal != nil {
			if refusal.Path != "" {
				field += "." + refusal.Path
			}
			return compiledTargetPlan{present: true, position: index, refusal: &ObjectDisposition{
				SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported,
				Reason: refusal.Reason, FieldPath: field,
			}}
		}
		return compiledTargetPlan{present: true, position: index, plan: plan}
	}
	return compiledTargetPlan{}
}

type compiledPlanInputs struct {
	missingHistoryAsZero bool
	primary              execution.QueryPlanFacts
	osRestartHistory     *execution.QueryPlanFacts
	requirements         []execution.DataRequirementTemplate
	queryPlans           map[execution.LogicalQueryRef]execution.QueryPlanFacts
	seenRequirements     map[execution.RequirementID]struct{}
}

// frozenSubjectFacts freezes the strategy facts the subject projection reads
// when a record's own dimensions do not name its object: the strategy's labels,
// and the result table of its first query config - which is the only one Python
// inspects. Freezing them keeps the object a Slot reports inside the frozen
// revision, so it cannot change because the strategy was edited between two
// evaluations of the same Slot.
func frozenSubjectFacts(source legacyStrategy, item legacyItem) *contract.MonitorSubjectFacts {
	facts := contract.MonitorSubjectFacts{Labels: append([]string{}, source.Labels...)}
	if len(item.QueryConfigs) > 0 {
		var first struct {
			ResultTableID string `json:"result_table_id"`
		}
		if err := json.Unmarshal(item.QueryConfigs[0], &first); err == nil {
			facts.ResultTableID = first.ResultTableID
		}
	}
	if len(facts.Labels) == 0 && facts.ResultTableID == "" {
		// Nothing to freeze. An absent section says the projection answers from
		// the dimensions alone, which is not the same as an empty one.
		return nil
	}
	return &facts
}

func compilePlan(
	source legacyStrategy,
	item legacyItem,
	identity SourceIdentity,
	dataset contract.DatasetContractV2,
	sourceID string,
	inputs *compiledPlanInputs,
	targetScope *contract.TargetScopeV2,
	targetPlan *contract.TargetPlanV1,
	policy NoDataPolicy,
) (contract.EvaluationPlanV2, planCompileFacts, []ObjectDisposition, error) {
	interval, err := itemInterval(item)
	if err != nil {
		return contract.EvaluationPlanV2{}, planCompileFacts{}, nil, err
	}
	strategyID := strconv.FormatInt(source.ID, 10)
	revision := source.UpdateTime.String()
	if revision != "" {
		value, parseErr := source.UpdateTime.Float64()
		if parseErr != nil {
			return contract.EvaluationPlanV2{}, planCompileFacts{}, nil, fmt.Errorf("alarmd controlplane: invalid strategy update_time: %w", parseErr)
		}
		if value == 0 {
			revision = ""
		}
	}
	if revision == "" {
		revision, err = contract.DeriveCanonicalDigestV2("alarmd-legacy-strategy-revision-v1", source)
		if err != nil {
			return contract.EvaluationPlanV2{}, planCompileFacts{}, nil, err
		}
	}
	ref := contract.StrategyRefV2{TenantID: identity.TenantID, StrategyID: strategyID, Revision: revision}
	if len(source.SnapshotRevision) != 0 {
		if err := json.Unmarshal(source.SnapshotRevision, &ref.SnapshotRevision); err != nil || ref.SnapshotRevision <= 0 {
			return contract.EvaluationPlanV2{}, planCompileFacts{}, nil, errors.New("alarmd controlplane: invalid strategy_revision")
		}
	}
	unit := itemUnit(item)
	dimensionFields := append([]string(nil), dataset.IdentityFields...)
	if itemHasAlgorithm(item, strategy.DetectorKindProcPort) {
		dimensionFields = []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"}
	}
	projection := contract.InputProjectionV2{DynamicDimensions: dataset.DynamicDimensions, ValueFields: []string{"value"}, DimensionFields: dimensionFields, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: unit, MissingValuePolicy: contract.MissingValuePolicyRequired}
	detectByLevel := make(map[uint32]legacyDetect, len(source.Detects))
	duplicateDetect := make(map[uint32]struct{})
	for _, detect := range source.Detects {
		if _, duplicate := detectByLevel[detect.Level]; duplicate {
			duplicateDetect[detect.Level] = struct{}{}
		}
		detectByLevel[detect.Level] = detect
	}
	rawAlgorithms := make(map[uint32][]legacyAlgorithm)
	for _, raw := range item.Algorithms {
		rawAlgorithms[raw.Level] = append(rawAlgorithms[raw.Level], raw)
	}
	levelIDs := make([]int, 0, len(rawAlgorithms))
	for level := range rawAlgorithms {
		levelIDs = append(levelIDs, int(level))
	}
	sort.Ints(levelIDs)
	for _, detect := range source.Detects {
		_, err := strategy.CompileUptime(detect.Trigger.Uptime)
		if err != nil {
			reason := "EFFECTIVE_TIME_INVALID"
			return contract.EvaluationPlanV2{}, planCompileFacts{}, []ObjectDisposition{{SourceID: sourceID, Scope: "PLAN", Disposition: DispositionConfigRejected, Reason: reason}}, fmt.Errorf("alarmd controlplane: %s", reason)
		}
	}
	levels := make([]contract.LevelIRV2, 0, len(levelIDs))
	dispositions := make([]ObjectDisposition, 0)
	for _, rawLevel := range levelIDs {
		levelID := uint32(rawLevel)
		detect, ok := detectByLevel[levelID]
		_, duplicate := duplicateDetect[levelID]
		borrowed := false
		if !ok && !duplicate && len(source.Detects) > 0 {
			// The platform's trigger reads a level with no trigger of its own
			// with the strategy's first one; its recovery finds none for the
			// level and takes its default. See borrowedLegacyDetect. The first
			// one is taken whatever it holds, and one that cannot trigger is
			// refused below as the missing trigger it is, not lent. Nor is one
			// whose own level is written twice: the platform would lend the
			// last of the two, and two triggers at one level are refused here.
			if _, twice := duplicateDetect[source.Detects[0].Level]; !twice {
				detect, ok, borrowed = borrowedLegacyDetect(source.Detects[0]), true, true
			}
		}
		if !ok || detect.Level == 0 || detect.Trigger.Count == 0 || detect.Trigger.CheckWindow == 0 || duplicate {
			dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "TRIGGER_CONFIG_MISSING"})
			continue
		}
		levelInputs := compiledPlanInputs{missingHistoryAsZero: inputs.missingHistoryAsZero, primary: inputs.primary, osRestartHistory: inputs.osRestartHistory}
		compiledAlgorithms := make([]contract.AlgorithmIRV2, 0, len(rawAlgorithms[levelID]))
		invalid := false
		for _, raw := range rawAlgorithms[levelID] {
			if !supportedAlgorithmKind(raw.Type) {
				dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionUnsupported, Reason: "ALGORITHM_NOT_MIGRATED"})
				invalid = true
				break
			}
			if err := validateCanonicalAlgorithmQuery(raw.Type, item.QueryConfigs); err != nil {
				dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "ALGORITHM_QUERY_INVALID"})
				invalid = true
				break
			}
			config, err := compileAlgorithmConfig(raw, unit, levelID, projection, dataset.IdentityFields, interval, &levelInputs)
			if err != nil {
				reason := "ALGORITHM_CONFIG_INVALID"
				if raw.Type == strategy.DetectorKindThreshold {
					reason = "THRESHOLD_CONFIG_INVALID"
				}
				dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: reason})
				invalid = true
				break
			}
			detectorKind := raw.Type
			if raw.Type == SourceAlgorithmTypePingUnreachable {
				detectorKind = strategy.DetectorKindThreshold
			}
			compiledAlgorithms = append(compiledAlgorithms, contract.AlgorithmIRV2{Type: detectorKind, Version: 1, Config: config})
		}
		if invalid {
			continue
		}
		priority := uint32(0)
		if detect.Priority == nil {
			if levelID <= 3 {
				priority = levelID
			}
		} else {
			priority = *detect.Priority
		}
		if priority == 0 {
			dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "LEVEL_PRIORITY_INVALID"})
			continue
		}
		recoveryConfig, recoveryEnabled, err := decodeLegacyRecovery(detect.Recovery)
		if err != nil || (recoveryEnabled && recoveryConfig.CheckWindow == 0) {
			dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "RECOVERY_CONFIG_INVALID"})
			continue
		}
		triggerFields := map[string]any{"required_anomalies": detect.Trigger.Count, "step_seconds": interval, "window_size": detect.Trigger.CheckWindow}
		if len(detect.Trigger.Uptime) > 0 && string(detect.Trigger.Uptime) != "null" {
			triggerFields["uptime"] = detect.Trigger.Uptime
			triggerFields["timezone_ref"] = "BUSINESS_LOCAL"
			// Named here, where the Leader can say which strategy and Level;
			// the compiler reads the range as Python does and does not know
			// whose it is.
			if strategy.UptimeTimeRangesNormalized(detect.Trigger.Uptime) {
				dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigNormalized, Reason: ReasonEffectiveTimeRangeInvalid})
			}
		}
		trigger, _ := json.Marshal(triggerFields)
		recovery, _ := json.Marshal(map[string]any{"consecutive_windows": recoveryConfig.CheckWindow, "enabled": recoveryEnabled})
		connector := contract.LevelConnectorAND
		if strings.EqualFold(detect.Connector, "or") {
			connector = contract.LevelConnectorOR
		}
		if borrowed {
			dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigNormalized, Reason: ReasonLevelTriggerBorrowed,
				Detail: fmt.Sprintf("trigger_from_level=%d", detect.Level)})
		}
		levels = append(levels, contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: levelID, Priority: priority}, Connector: connector, DetectPlan: contract.DetectPlanV2{Algorithms: compiledAlgorithms}, TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: trigger}, RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: recovery}})
		inputs.merge(levelInputs)
	}
	if len(levels) == 0 {
		return contract.EvaluationPlanV2{}, planCompileFacts{}, dispositions, errors.New("alarmd controlplane: no executable level")
	}
	semantics := contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: uint32(interval), AggregationInterval: uint32(interval), EvaluationInterval: uint32(interval), LatenessTolerance: uint32(interval * 2)}
	ir := contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: ref, ExecutionSemantics: semantics, InputProjection: projection, Levels: levels}
	plan := contract.EvaluationPlanV2{PlanID: strategyID, StrategyRef: ref, InputProjection: projection, SourceCompatibility: &contract.SourceCompatibilityV2{ItemID: strconv.FormatInt(item.ID, 10)}, StrategyIR: ir}
	plan.TargetScope = targetScope
	plan.EffectiveTimeSnapshot = append(json.RawMessage(nil), source.EffectiveTimeSnapshot...)
	plan.TargetPlan = targetPlan
	// A no-data configuration this build cannot compile suspends no-data
	// detection for this Plan and nothing else.
	//
	// It used to withhold the whole Plan, thresholds included, on the argument
	// that half a Plan makes "is this strategy covered" unanswerable. The
	// answer to that is to say which half rather than to stop both: a strategy
	// whose threshold detection is switched off because its no-data settings
	// do not compile has lost the detection somebody was actually watching,
	// over the half they may not have known was configured. The half that is
	// off is named here, counted in the composition, and listed by strategy.
	suspended := ""
	noData, err := frozenNoDataConfig(item, policy)
	if err != nil {
		suspended, noData = contract.ReasonNoDataConfigInvalid, nil
	}
	plan.NoData = noData
	if reason := noDataRosterUnsupported(targetScope, targetPlan, noData); suspended == "" && reason != "" {
		// Decided here rather than every round. The expected set is a function
		// of the target's shape and the no-data dimensions, both frozen here,
		// so a Slot would reach the same answer with no new information - and
		// reaching it there would mean a Plan that runs while detecting no
		// absence at all, which reads as a working strategy.
		//
		// Suspending rather than refusing for the reason above it: what this
		// build cannot do is the roster, and the thresholds do not depend on
		// it.
		suspended, plan.NoData = contract.ReasonNoDataRosterUnsupported, nil
	}
	if ref.SnapshotRevision > 0 {
		plan.OutputIdentity = &contract.MonitorOutputIdentity{DynamicDimensions: dataset.DynamicDimensions, DimensionFields: append([]string{}, dataset.IdentityFields...)}
		plan.SubjectFacts = frozenSubjectFacts(source, item)
	}
	scheduleSpec, refusal, err := planScheduleSpec(sourceID, plan, interval)
	if err != nil {
		if refusal != nil {
			dispositions = append(dispositions, *refusal)
		}
		return contract.EvaluationPlanV2{}, planCompileFacts{}, dispositions, err
	}
	schedule, err := execution.DerivePlanScheduleRevision(scheduleSpec)
	return plan, planCompileFacts{ScheduleSpec: scheduleSpec, ScheduleRevision: schedule, NoDataSuspended: suspended},
		dispositions, err
}

// planScheduleSpec derives the Plan's schedule and holds the three copies of
// its evaluation step to one another: the schedule's cadence, the execution
// semantics' evaluation interval, and each Level's trigger step. Today all
// three are the item's aggregation interval, written from one variable, and
// the state grid, the trigger windows and the Slot cadence only line up
// because they are. A change that moves one of them -- the day the step is
// separated from the aggregation interval -- has to answer for the others,
// and this is where it finds out: the Plan is withheld by name rather than
// run on a schedule its windows do not count in.
func planScheduleSpec(sourceID string, plan contract.EvaluationPlanV2, intervalSeconds int64) (execution.ScheduleSpec, *ObjectDisposition, error) {
	spec := execution.DeriveScheduleSpec(intervalSeconds)
	step := int64(plan.StrategyIR.ExecutionSemantics.EvaluationInterval)
	mismatch := func(what string, value int64) (execution.ScheduleSpec, *ObjectDisposition, error) {
		detail := fmt.Sprintf("%s=%d evaluation_interval=%d", what, value, step)
		return execution.ScheduleSpec{}, &ObjectDisposition{SourceID: sourceID, Scope: "PLAN", Disposition: DispositionUnsupported, Reason: "EVALUATION_STEP_INCONSISTENT",
			Detail: detail}, errors.New("alarmd controlplane: evaluation step copies disagree: " + detail)
	}
	if spec.EvaluationIntervalSeconds != step {
		return mismatch("schedule_interval", spec.EvaluationIntervalSeconds)
	}
	for _, level := range plan.StrategyIR.Levels {
		var trigger struct {
			StepSeconds int64 `json:"step_seconds"`
		}
		if err := json.Unmarshal(level.TriggerPlan.Config, &trigger); err != nil {
			return execution.ScheduleSpec{}, nil, err
		}
		if trigger.StepSeconds != step {
			return mismatch(fmt.Sprintf("level_%d_step_seconds", level.Definition.LevelID), trigger.StepSeconds)
		}
	}
	return spec, nil, nil
}

func supportedAlgorithmKind(kind string) bool {
	if strategy.IsTraditionalComparison(kind) {
		return true
	}
	switch kind {
	case strategy.DetectorKindThreshold, strategy.DetectorKindSimpleRingRatio, strategy.DetectorKindOsRestart,
		strategy.DetectorKindProcPort, SourceAlgorithmTypePingUnreachable:
		return true
	default:
		return false
	}
}

func compileAlgorithmConfig(
	raw legacyAlgorithm,
	unit string,
	levelID uint32,
	projection contract.InputProjectionV2,
	identityFields []string,
	interval int64,
	inputs *compiledPlanInputs,
) (json.RawMessage, error) {
	if raw.Type == strategy.DetectorKindThreshold {
		return thresholdConfig(raw, unit)
	}
	if inputs == nil {
		return nil, errors.New("alarmd controlplane: algorithm input facts are missing")
	}
	inputProjection := execution.InputProjection{
		ValueFields:     append([]string(nil), projection.ValueFields...),
		DimensionFields: append([]string(nil), projection.DimensionFields...),
		IdentityFields:  append([]string(nil), identityFields...),
	}
	requirements, err := inputs.buildRequirements(raw.Type, levelID, interval, inputProjection)
	if err != nil {
		return nil, err
	}
	if strategy.IsTraditionalComparison(raw.Type) {
		var parameters strategy.TraditionalComparisonParameters
		if err := json.Unmarshal(raw.Config, &parameters); err != nil {
			return nil, err
		}
		offsets, err := strategy.TraditionalHistoryOffsets(raw.Type, parameters, interval)
		if err != nil {
			return nil, err
		}
		for _, group := range strategy.TraditionalHistoryGroups(raw.Type, offsets) {
			name := strategy.TraditionalHistoryDataset(raw.Type, group)
			points := make([]execution.NamedInputPoint, len(group))
			for i, offset := range group {
				points[i] = execution.NamedInputPoint{Name: strategy.TraditionalHistoryName(offset), OffsetSeconds: offset}
			}
			dependency, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
				DatasetName: execution.DatasetName(name), Role: execution.InputRoleAlgorithmDependency, ConsumerLevelID: levelID, LogicalQueryRef: execution.LogicalQueryRef(inputs.primary.QueryRevision),
				RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -inputs.primary.QueryDelaySeconds - (group[len(group)-1] + interval), EndOffsetSeconds: -inputs.primary.QueryDelaySeconds - group[0], HalfOpen: true}, StepMillis: inputs.primary.StepMillis, AlignmentMillis: inputs.primary.AlignmentMillis,
				ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired, InputProjection: inputProjection, PointOffsetsSeconds: group, NamedPoints: points,
			})
			if err != nil {
				return nil, err
			}
			requirements = append(requirements, dependency)
			inputs.addRequirement(dependency)
		}
	}
	algorithmProjection := strategy.AlgorithmInputProjection{
		ValueFields:     append([]string(nil), inputProjection.ValueFields...),
		DimensionFields: append([]string(nil), inputProjection.DimensionFields...),
		IdentityFields:  append([]string(nil), inputProjection.IdentityFields...),
	}
	algorithmRequirements := make([]strategy.AlgorithmInputRequirement, len(requirements))
	for index, requirement := range requirements {
		algorithmRequirements[index] = strategy.AlgorithmInputRequirement{
			RequirementID: string(requirement.RequirementID), DatasetName: string(requirement.DatasetName),
			Role: strategy.AlgorithmInputRole(requirement.Role), ConsumerLevelID: requirement.ConsumerLevelID,
			LogicalQueryRef: string(requirement.LogicalQueryRef),
			RelativeWindow: strategy.AlgorithmRelativeWindow{
				StartOffsetSeconds: requirement.RelativeWindow.StartOffsetSeconds,
				EndOffsetSeconds:   requirement.RelativeWindow.EndOffsetSeconds,
				HalfOpen:           requirement.RelativeWindow.HalfOpen,
			},
			StepMillis: requirement.StepMillis, AlignmentMillis: requirement.AlignmentMillis,
			ReadinessClass:      strategy.AlgorithmReadinessClass(requirement.ReadinessClass),
			InputProjection:     algorithmProjection,
			PointOffsetsSeconds: append([]int64(nil), requirement.PointOffsetsSeconds...),
			NamedPoints:         make([]strategy.AlgorithmNamedInputPoint, len(requirement.NamedPoints)),
		}
		for pointIndex, point := range requirement.NamedPoints {
			algorithmRequirements[index].NamedPoints[pointIndex] = strategy.AlgorithmNamedInputPoint{
				Name: point.Name, OffsetSeconds: point.OffsetSeconds,
			}
		}
	}
	if strategy.IsTraditionalComparison(raw.Type) {
		var sourceConfig map[string]json.RawMessage
		if err := json.Unmarshal(raw.Config, &sourceConfig); err != nil {
			return nil, err
		}
		for key, value := range map[string]any{"data_unit": unit, "algorithm_unit": raw.UnitPrefix, "precision": 6, "missing_history_as_zero": inputs.missingHistoryAsZero, "input_projection": algorithmProjection, "requirements": algorithmRequirements} {
			sourceConfig[key], err = json.Marshal(value)
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(sourceConfig)
	}
	if raw.Type == strategy.DetectorKindSimpleRingRatio {
		var sourceConfig struct {
			Floor json.RawMessage `json:"floor"`
			Ceil  json.RawMessage `json:"ceil"`
		}
		if err := json.Unmarshal(raw.Config, &sourceConfig); err != nil {
			return nil, errors.New("alarmd controlplane: invalid SimpleRingRatio config")
		}
		return json.Marshal(struct {
			MissingHistoryAsZero bool                                 `json:"missing_history_as_zero"`
			Floor                json.RawMessage                      `json:"floor"`
			Ceil                 json.RawMessage                      `json:"ceil"`
			InputProjection      strategy.AlgorithmInputProjection    `json:"input_projection"`
			Requirements         []strategy.AlgorithmInputRequirement `json:"requirements"`
		}{inputs.missingHistoryAsZero, sourceConfig.Floor, sourceConfig.Ceil, algorithmProjection, algorithmRequirements})
	}
	if raw.Type == SourceAlgorithmTypePingUnreachable {
		if !emptyAlgorithmConfig(raw.Config) {
			return nil, errors.New("alarmd controlplane: invalid PingUnreachable config")
		}
		return json.Marshal(struct {
			ValueField            string                               `json:"value_field"`
			DataUnit              string                               `json:"data_unit"`
			ThresholdUnitPrefix   string                               `json:"threshold_unit_prefix"`
			Precision             map[string]any                       `json:"precision"`
			Groups                []map[string]any                     `json:"groups"`
			SourceAlgorithmFamily string                               `json:"source_algorithm_family"`
			SourceMappingVersion  string                               `json:"source_mapping_version"`
			CanonicalQueryDigest  string                               `json:"canonical_query_digest"`
			InputProjection       strategy.AlgorithmInputProjection    `json:"input_projection"`
			Requirements          []strategy.AlgorithmInputRequirement `json:"requirements"`
		}{
			ValueField: "value", DataUnit: unit, ThresholdUnitPrefix: "",
			Precision:             map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"},
			Groups:                []map[string]any{{"conditions": []map[string]any{{"operator": "GTE", "threshold_decimal": "1"}}}},
			SourceAlgorithmFamily: strategy.SourceAlgorithmFamilyPingUnreachable,
			SourceMappingVersion:  strategy.SourceMappingPingUnreachableV1,
			CanonicalQueryDigest:  string(inputs.primary.QueryRevision),
			InputProjection:       algorithmProjection, Requirements: algorithmRequirements,
		})
	}
	return json.Marshal(struct {
		InputProjection strategy.AlgorithmInputProjection    `json:"input_projection"`
		Requirements    []strategy.AlgorithmInputRequirement `json:"requirements"`
	}{algorithmProjection, algorithmRequirements})
}

type canonicalAlgorithmQuery struct {
	ResultTableID string
	MetricID      string
	MetricField   string
	AggMethod     string
	AggInterval   int64
}

func validateCanonicalAlgorithmQuery(kind string, rawConfigs []json.RawMessage) error {
	expected, fixed := map[string]canonicalAlgorithmQuery{
		strategy.DetectorKindOsRestart:     {ResultTableID: "system.env", MetricID: "bk_monitor.os_restart", MetricField: "uptime", AggMethod: "MAX", AggInterval: 60},
		strategy.DetectorKindProcPort:      {ResultTableID: "system.proc_port", MetricID: "bk_monitor.proc_port", MetricField: "proc_exists", AggMethod: "MAX", AggInterval: 60},
		SourceAlgorithmTypePingUnreachable: {ResultTableID: "pingserver.base", MetricID: "bk_monitor.ping-gse", MetricField: "loss_percent", AggMethod: "MAX", AggInterval: 60},
	}[kind]
	if !fixed {
		return nil
	}
	if len(rawConfigs) != 1 {
		return errors.New("alarmd controlplane: fixed algorithm requires one canonical query")
	}
	config, err := decodeLegacyQueryConfig(rawConfigs[0])
	if err != nil {
		return err
	}
	table := config.ResultTableID
	if config.DataLabel != "" {
		table = config.DataLabel
	}
	if config.ResultTableID != expected.ResultTableID || table != expected.ResultTableID || config.MetricID != expected.MetricID || config.MetricField != expected.MetricField ||
		config.AggMethod != expected.AggMethod || config.AggInterval != expected.AggInterval || len(config.Values) != 0 {
		return errors.New("alarmd controlplane: fixed algorithm query differs from canonical source")
	}
	return nil
}

func emptyAlgorithmConfig(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "[]"
}

func (inputs *compiledPlanInputs) buildRequirements(
	kind string,
	levelID uint32,
	interval int64,
	projection execution.InputProjection,
) ([]execution.DataRequirementTemplate, error) {
	if err := inputs.primary.Validate(); err != nil {
		return nil, err
	}
	primaryRef := execution.LogicalQueryRef(inputs.primary.QueryRevision)
	primary, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
		DatasetName: "primary", Role: execution.InputRolePrimary, ConsumerLevelID: levelID,
		LogicalQueryRef: primaryRef,
		RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -inputs.primary.QueryDelaySeconds - interval, EndOffsetSeconds: -inputs.primary.QueryDelaySeconds, HalfOpen: true},
		StepMillis:      inputs.primary.StepMillis, AlignmentMillis: inputs.primary.AlignmentMillis,
		ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessEager,
		InputProjection: projection,
	})
	if err != nil {
		return nil, err
	}
	result := []execution.DataRequirementTemplate{primary}
	inputs.addRequirement(primary)
	inputs.addQuery(primaryRef, inputs.primary)

	switch kind {
	case strategy.DetectorKindSimpleRingRatio:
		dependency, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
			DatasetName: "previous", Role: execution.InputRoleAlgorithmDependency, ConsumerLevelID: levelID,
			LogicalQueryRef: primaryRef,
			RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -inputs.primary.QueryDelaySeconds - 2*interval, EndOffsetSeconds: -inputs.primary.QueryDelaySeconds - interval, HalfOpen: true},
			StepMillis:      inputs.primary.StepMillis, AlignmentMillis: inputs.primary.AlignmentMillis,
			ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired,
			InputProjection: projection, PointOffsetsSeconds: []int64{interval},
			NamedPoints: []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: interval}},
		})
		if err != nil {
			return nil, err
		}
		result = append(result, dependency)
		inputs.addRequirement(dependency)
	case strategy.DetectorKindOsRestart:
		if inputs.osRestartHistory == nil {
			return nil, errors.New("alarmd controlplane: OsRestart history query facts are missing")
		}
		historyRef := execution.LogicalQueryRef(inputs.osRestartHistory.QueryRevision)
		dependency, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
			DatasetName: "uptime_history", Role: execution.InputRoleAlgorithmDependency, ConsumerLevelID: levelID,
			LogicalQueryRef: historyRef,
			RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -inputs.primary.QueryDelaySeconds - (1500 + interval), EndOffsetSeconds: -inputs.primary.QueryDelaySeconds, HalfOpen: true},
			StepMillis:      inputs.osRestartHistory.StepMillis, AlignmentMillis: inputs.osRestartHistory.AlignmentMillis,
			ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired,
			InputProjection: projection, PointOffsetsSeconds: []int64{interval, 600, 1500},
			NamedPoints: []execution.NamedInputPoint{
				{Name: "previous", OffsetSeconds: interval}, {Name: "previous_10m", OffsetSeconds: 600},
				{Name: "previous_25m", OffsetSeconds: 1500},
			},
		})
		if err != nil {
			return nil, err
		}
		result = append(result, dependency)
		inputs.addRequirement(dependency)
		inputs.addQuery(historyRef, *inputs.osRestartHistory)
	}
	return result, nil
}

func (inputs *compiledPlanInputs) addRequirement(requirement execution.DataRequirementTemplate) {
	if inputs.seenRequirements == nil {
		inputs.seenRequirements = make(map[execution.RequirementID]struct{})
	}
	if _, exists := inputs.seenRequirements[requirement.RequirementID]; exists {
		return
	}
	inputs.seenRequirements[requirement.RequirementID] = struct{}{}
	inputs.requirements = append(inputs.requirements, requirement)
}

func (inputs *compiledPlanInputs) addQuery(ref execution.LogicalQueryRef, facts execution.QueryPlanFacts) {
	if inputs.queryPlans == nil {
		inputs.queryPlans = make(map[execution.LogicalQueryRef]execution.QueryPlanFacts)
	}
	inputs.queryPlans[ref] = facts
}

func (inputs *compiledPlanInputs) merge(source compiledPlanInputs) {
	for _, requirement := range source.requirements {
		inputs.addRequirement(requirement)
	}
	for ref, facts := range source.queryPlans {
		inputs.addQuery(ref, facts)
	}
}

func decodeLegacyRecovery(raw json.RawMessage) (legacyRecovery, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return legacyRecovery{}, false, nil
	}
	var recovery legacyRecovery
	if err := json.Unmarshal(raw, &recovery); err != nil {
		return legacyRecovery{}, false, err
	}
	return recovery, true, nil
}

func hasJSONValue(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "0" && value != `""`
}

func isAlwaysActiveUptime(raw json.RawMessage) bool {
	if !hasJSONValue(raw) {
		return true
	}
	var uptime struct {
		Calendars       []json.RawMessage             `json:"calendars"`
		ActiveCalendars []json.RawMessage             `json:"active_calendars"`
		TimeRanges      []struct{ Start, End string } `json:"time_ranges"`
	}
	if json.Unmarshal(raw, &uptime) != nil || len(uptime.Calendars) > 0 || len(uptime.ActiveCalendars) > 0 {
		return false
	}
	if len(uptime.TimeRanges) == 0 {
		return true
	}
	return len(uptime.TimeRanges) == 1 && ((uptime.TimeRanges[0].Start == "00:00" && uptime.TimeRanges[0].End == "23:59") ||
		(uptime.TimeRanges[0].Start == "00:00:00" && uptime.TimeRanges[0].End == "23:59:59"))
}

func itemInterval(item legacyItem) (int64, error) {
	if len(item.QueryConfigs) == 0 {
		return 0, errors.New("alarmd controlplane: invalid query config")
	}
	var minimum int64
	for _, raw := range item.QueryConfigs {
		var query struct {
			AggInterval json.Number `json:"agg_interval"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&query) != nil {
			return 0, errors.New("alarmd controlplane: invalid query config")
		}
		interval, err := strconv.ParseInt(query.AggInterval.String(), 10, 64)
		if err != nil || interval <= 0 {
			return 0, errors.New("alarmd controlplane: positive aggregation interval is required")
		}
		if minimum == 0 || interval < minimum {
			minimum = interval
		}
	}
	return minimum, nil
}

func thresholdConfig(raw legacyAlgorithm, unit string) (json.RawMessage, error) {
	type condition struct {
		Method    string      `json:"method"`
		Threshold json.Number `json:"threshold"`
	}
	var groups [][]condition
	decoder := json.NewDecoder(strings.NewReader(string(raw.Config)))
	decoder.UseNumber()
	if err := decoder.Decode(&groups); err != nil || len(groups) == 0 {
		var single []condition
		decoder = json.NewDecoder(strings.NewReader(string(raw.Config)))
		decoder.UseNumber()
		if err := decoder.Decode(&single); err != nil || len(single) == 0 {
			return nil, errors.New("alarmd controlplane: invalid Threshold config")
		}
		groups = [][]condition{single}
	}
	wireGroups := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		conditions := make([]map[string]any, 0, len(group))
		for _, condition := range group {
			operator := map[string]string{"gt": "GT", "gte": "GTE", "eq": "EQ", "neq": "NEQ", "lt": "LT", "lte": "LTE"}[strings.ToLower(condition.Method)]
			if operator == "" || condition.Threshold.String() == "" {
				return nil, errors.New("alarmd controlplane: invalid Threshold condition")
			}
			conditions = append(conditions, map[string]any{"operator": operator, "threshold_decimal": condition.Threshold.String()})
		}
		wireGroups = append(wireGroups, map[string]any{"conditions": conditions})
	}
	return json.Marshal(map[string]any{"value_field": "value", "data_unit": unit, "threshold_unit_prefix": raw.UnitPrefix, "precision": map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"}, "groups": wireGroups})
}
