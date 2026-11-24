// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// The decision words a job's operation id is made of, beside the edit
// decisions' own (editclose).
const (
	DecisionAbsent      = "absent"
	DecisionUnsupported = "unsupported"
	DecisionInactive    = "inactive"
	// DecisionDisabled and DecisionDeleted are a strategy gone under the
	// writer's mark (absentalerts.PathDeparted): each its own command and
	// count, with the same reason as DecisionAbsent.
	DecisionDisabled = "disabled"
	DecisionDeleted  = "deleted"
)

// absentReason is the reason a strategy gone is closed with, the platform's
// own sentence for a strategy disabled or deleted (bk-monitor upstream/master
// 6086a88112 alarm_backends/service/alert/manager/checker/close.py:84-95).
// The mark's state is in the decision word and the counts, not in the text.
const absentReason = "策略已被停用或删除，告警关闭"

// Absent closes every alert of a strategy no longer published (decision
// absent, or disabled or deleted under the writer's mark) or refused for a
// capability (unsupported). The expectation is every entry of the
// strategy's record; without one, each alert of the strategy is closed by
// its id. Its period is not known: the record vouches recordSettle after its
// start. It returns the word to wait on (AbsentResult has every part).
func (closer *Closer) Absent(ctx context.Context, scope Scope, decision string, revision int64) string {
	return closer.AbsentResult(ctx, scope, decision, revision).Word
}

// AbsentResult is Absent's Result: every part's word and what was closed.
func (closer *Closer) AbsentResult(ctx context.Context, scope Scope, decision string, revision int64) Result {
	filter, err := StrategyFilter(scope)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return only(OutcomeFilterRefused, jobTally{})
	}
	reason := fmt.Sprintf("alarmd: strategy %s %s", scope.StrategyID, decision)
	switch decision {
	case DecisionAbsent, DecisionDisabled, DecisionDeleted:
		reason = absentReason
	}
	return closer.recorded(ctx, Job{Filter: filter, Decision: decision, Revision: revision, Reason: reason},
		everyEntry, labelIs("strategy_id", scope.StrategyID))
}

// AbsentFound is a strategy the link holds active alerts of this source
// for that the snapshot does not run, with the version its sample alert
// carries.
type AbsentFound struct {
	StrategyID string
	Version    string
}

// MaxAbsentPerRound bounds the searches one tenant's discovery makes in a
// round, one per strategy found.
const MaxAbsentPerRound = 64

// AbsentStrategies finds the strategies of the tenant's active alerts of
// the source that are not among running, each with one search of limit 1
// that excludes running and the ones found so far: the alert with the
// smallest id names the next one. It replaces the link Console's roster.
// A strategy found is only a candidate; whether it is absent long enough
// to close is the caller's (absentalerts). A failed search ends the walk
// with its word; what was found before it is returned.
func (closer *Closer) AbsentStrategies(ctx context.Context, tenantID, eventSourceID string, running []string) ([]AbsentFound, string) {
	if word := closer.refuseUnsupported(); word != "" {
		return nil, word
	}
	exclude := append([]string(nil), running...)
	var found []AbsentFound
	for len(found) < MaxAbsentPerRound {
		query, err := discoveryFilter(tenantID, eventSourceID, exclude)
		if err != nil {
			closer.count(OutcomeFilterRefused, 1)
			return found, OutcomeFilterRefused
		}
		page, err := closer.link.Search(ctx, tenantID, query, "", 1)
		if err != nil {
			return found, closer.failed(err)
		}
		if len(page.Items) == 0 {
			return found, ""
		}
		strategy, ok := page.Items[0].Label("strategy_id")
		if !ok || !decimal(strategy) {
			// An id no term can exclude: the walk would find it again.
			closer.count(OutcomeSampleUnreadable, 1)
			return found, ""
		}
		version, _ := page.Items[0].Label("strategy_version")
		found = append(found, AbsentFound{StrategyID: strategy, Version: version})
		exclude = append(exclude, strategy)
	}
	closer.count(OutcomeAbsentTruncated, 1)
	return found, ""
}

// Fingerprints closes the strategy's active alerts of the given
// fingerprints opened at or before decidedAt under one decision (a close
// reason and its detail): the series a caller decided one by one, read at
// decidedAt. The expectation is the fingerprints themselves - one active
// alert each at most.
func (closer *Closer) Fingerprints(ctx context.Context, scope Scope, fingerprints []string, decision string, revision int64,
	decidedAt time.Time) string {
	filter, err := FingerprintFilter(scope, fingerprints, decidedAt)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return OutcomeFilterRefused
	}
	strategy, err := StrategyFilter(scope)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return OutcomeFilterRefused
	}
	return closer.Run(ctx, Job{Filter: filter, Decision: decision, Revision: revision, Recheck: strategy,
		Expect: Expectation{Count: len(fingerprints), Source: ExpectDecidedIDs},
		Reason: fmt.Sprintf("alarmd: strategy %s %s", scope.StrategyID, decision)})
}

// Inactive closes the strategy's alerts outside its effective time; version
// non-empty closes only the alerts opened at it, where the decision is the
// version's alone. since is when the caller first found this inactive
// period: each period is its own decision, with its own command, so a
// later period does not close its alerts at an earlier period's
// effective_at. The expectation is the record's entries of the version
// (every entry without one); without a record, each alert whose own
// strategy_version label is the version is closed by its id. period is how
// often the strategy is evaluated (settle).
func (closer *Closer) Inactive(ctx context.Context, scope Scope, version string, revision int64, since time.Time,
	period time.Duration) string {
	return closer.inactiveResult(ctx, scope, version, revision, since, period).Word
}

// inactiveResult is Inactive with every job's word and what they sent and
// closed.
func (closer *Closer) inactiveResult(ctx context.Context, scope Scope, version string, revision int64, since time.Time,
	period time.Duration) Result {
	if since.IsZero() {
		closer.count(OutcomeFilterRefused, 1)
		return only(OutcomeFilterRefused, jobTally{})
	}
	filter, err := StrategyFilter(scope)
	match, decide := everyEntry, labelIs("strategy_id", scope.StrategyID)
	if version != "" {
		filter, err = VersionFilter(scope, version)
		match, decide = versionIs(version), labelIs("strategy_version", version)
	}
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return only(OutcomeFilterRefused, jobTally{})
	}
	return closer.recorded(ctx, Job{Filter: filter, Decision: DecisionInactive, Version: version, Revision: revision,
		Batch:  "since:" + strconv.FormatInt(since.Unix(), 10),
		Reason: fmt.Sprintf("alarmd: strategy %s outside its effective time", scope.StrategyID),
		period: period}, match, decide)
}

// The bounds of one strategy's edit close in a round: the buckets it
// enumerates (one search each), and the alerts it reads one by one.
const (
	maxBuckets       = 64
	maxAlertsPerTurn = 2000
)

// EditInput is one strategy's edit close: its current Plans.
type EditInput struct {
	Scope Scope
	Plan  editclose.Plan
	// Period is how often the strategy is evaluated (settle); zero where
	// it is not known.
	Period time.Duration
}

// Edit closes the alerts an edit of their strategy orphaned. Each bucket of
// alerts opened at an older version is found with one search (limit 1,
// the buckets seen excluded), judged from that one alert's facts with
// editclose.DecideGroup - every alert of the bucket carries the same facts,
// the digest says so - and closed by the bucket's filter. Alerts opened
// without a digest, and a bucket only each alert's dimensions can judge,
// are read and decided one by one with editclose.Decide and closed by
// their ids under the bucket's filter. The threshold alerts of the older
// versions at a level the current Plans no longer detect are closed by
// one more filter (level_removed). It returns the first word that ended
// the strategy's turn early - OutcomeUnsupported ends the round - or
// empty, and how many buckets of other versions it found: none is a
// strategy no edit close has anything left of.
//
// A bucket's expectation is the strategy record's entries of its version
// (all its buckets' alerts: an over-count of the one bucket), the level
// filter's the entries of the older versions it names. Without a record a
// bucket decided to close is walked alert by alert instead, and the level
// filter's alerts are decided one by one (editclose.Decide). A complete
// walk that finds no older bucket prunes the record's older entries.
func (closer *Closer) Edit(ctx context.Context, input EditInput) (string, int) {
	if word := closer.refuseUnsupported(); word != "" {
		return word, 0
	}
	plan := input.Plan
	if plan.Revision <= 0 {
		closer.count("edit_"+editclose.OutcomeRevisionUnknown, 1)
		return "", 0
	}
	current := strconv.FormatInt(plan.Revision, 10)
	buckets, samples, complete, word := closer.enumerate(ctx, input.Scope, current)
	if word != "" {
		return word, 0
	}
	if complete && len(buckets) == 0 {
		closer.pruneOlder(ctx, input.Scope, plan.Revision)
	}
	// The record is read once per strategy, by the first dry run that needs
	// it; its start once, by the first close.
	var record openepisodes.Record
	recordWord, loaded := "", false
	var since time.Time
	sinceWord, sinceRead := "", false
	recordSince := func(ctx context.Context) (time.Time, string) {
		if !sinceRead {
			since, sinceWord = closer.readSince(ctx, input.Scope, input.Period)
			sinceRead = true
		}
		return since, sinceWord
	}
	fromRecord := func(versions ...string) func(context.Context) (Expectation, string) {
		return func(ctx context.Context) (Expectation, string) {
			if !loaded {
				record, recordWord = closer.readRecord(ctx, input.Scope)
				loaded = true
			}
			if recordWord != "" {
				return Expectation{}, recordWord
			}
			vouch, _ := recordSince(ctx)
			n, _ := covered(record, vouch, func(entry openepisodes.Entry) bool {
				for _, version := range versions {
					if entry.Version == version {
						return true
					}
				}
				return false
			})
			return Expectation{Count: n, Source: ExpectOwnRecord}, ""
		}
	}
	var older []string
	seenOlder := map[string]bool{}
	for i, bucket := range buckets {
		if ctx.Err() != nil {
			return "", len(buckets)
		}
		revision, err := strconv.ParseInt(bucket.Version, 10, 64)
		if err == nil && revision > 0 && revision < plan.Revision && !seenOlder[bucket.Version] {
			seenOlder[bucket.Version] = true
			older = append(older, bucket.Version)
		}
		// A bucket without a digest, or one whose version is of the other
		// revision scheme than the Plans' (editclose.RevisionsComparable),
		// is judged alert by alert; oneByOne counts each incomparable alert.
		if bucket.Digest == "" || (err == nil && revision > 0 && !editclose.RevisionsComparable(revision, plan.Revision)) {
			if word := closer.oneByOneSettling(ctx, input, bucket, time.Time{}); word == OutcomeUnsupported {
				return word, len(buckets)
			}
			continue
		}
		facts, ok := sampleFacts(samples[i])
		if !ok {
			closer.count(OutcomeSampleUnreadable, 1)
			continue
		}
		if facts.Digest() != bucket.Digest {
			// Facts this build does not read whole, or a label that is not
			// the digest of the facts beside it: the sample does not speak
			// for the bucket.
			closer.count(OutcomeDigestMismatch, 1)
			continue
		}
		family := "metric_algorithm"
		if bucket.NoData {
			family = "no_data"
		}
		if sampleFamily(samples[i]) != family {
			// The tag says one family and extra_data.evaluation_family
			// another: the sample does not say which the bucket is.
			closer.count(OutcomeFamilyMismatch, 1)
			continue
		}
		decision, note := editclose.DecideGroup(plan, editclose.Group{Revision: revision, Family: family, Facts: facts})
		closer.count("edit_"+decision, 1)
		closer.count("edit_"+note, 1)
		switch {
		case decision == editclose.OutcomeGroupDimensionsUndecided:
			if word := closer.oneByOneSettling(ctx, input, bucket, time.Time{}); word == OutcomeUnsupported {
				return word, len(buckets)
			}
		case editclose.Closes(decision):
			since, sinceWord := recordSince(ctx)
			if sinceWord == OutcomeRecordUnavailable {
				continue
			}
			word := OutcomeRecordMissing
			if sinceWord == "" {
				filter, err := BucketFilter(input.Scope, bucket)
				if err == nil {
					filter, err = openedSince(filter, since)
				}
				if err != nil {
					closer.count(OutcomeFilterRefused, 1)
					continue
				}
				word = closer.runSettling(ctx, Job{Filter: filter, Decision: decision, Version: bucket.Version, Digest: bucket.Digest,
					Revision: plan.Revision, expect: fromRecord(bucket.Version), Reason: editReason(input.Scope, decision, bucket.Version)})
			}
			switch word {
			case OutcomeUnsupported:
				return word, len(buckets)
			case OutcomeRecordMissing:
				// No record, or none with a start: the whole bucket alert by
				// alert.
				word = closer.oneByOneSettling(ctx, input, bucket, time.Time{})
			default:
				// The bucket's alerts opened before the record's start,
				// which it cannot vouch for: alert by alert.
				word = closer.oneByOneSettling(ctx, input, bucket, since)
			}
			if word == OutcomeUnsupported {
				return word, len(buckets)
			}
		}
	}
	severities, word := editclose.RemovedSeverities(plan)
	closer.count("edit_"+word, 1)
	if len(older) == 0 || len(severities) == 0 {
		return "", len(buckets)
	}
	filter, err := LevelFilter(input.Scope, older, severities)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return "", len(buckets)
	}
	sort.Strings(older)
	versions := ""
	for _, version := range older {
		versions += version + ","
	}
	job := Job{Filter: filter, Decision: editclose.OutcomeLevelRemoved, Version: versions, Revision: plan.Revision,
		expect: fromRecord(older...), Reason: editReason(input.Scope, editclose.OutcomeLevelRemoved, "")}
	levelRemoved := func(alert linkdmanage.Alert) bool {
		decision, _ := editclose.Decide(plan, recordOf(alert))
		return decision == editclose.OutcomeLevelRemoved
	}
	start, startWord := recordSince(ctx)
	switch startWord {
	case OutcomeRecordUnavailable:
		return "", len(buckets)
	case OutcomeRecordMissing:
		word = OutcomeRecordMissing
	default:
		recent, olderPart := job, job
		recent.Recheck, olderPart.Recheck = filter, filter
		var err, olderErr error
		recent.Filter, err = openedSince(filter, start)
		olderPart.Filter, olderErr = openedBefore(filter, start)
		olderPart.expect = nil
		if err != nil || olderErr != nil {
			closer.count(OutcomeFilterRefused, 1)
			return "", len(buckets)
		}
		word = closer.runSettling(ctx, recent)
		if word != OutcomeRecordMissing && word != OutcomeUnsupported {
			word = closer.perAlertSettling(ctx, olderPart, levelRemoved)
		}
	}
	if word == OutcomeRecordMissing {
		word = closer.perAlertSettling(ctx, job, levelRemoved)
	}
	if word == OutcomeUnsupported {
		return word, len(buckets)
	}
	return "", len(buckets)
}

// runSettling, perAlertSettling and oneByOneSettling are how the edit
// close runs each of its jobs: again within the round while the job has
// more (job_done_rederived, or job_incomplete with pages left) and its own
// last run closed something, with a request's budget left
// (UntilProgressStops). Progress is the job's own
// tally, not what the process closed meanwhile, so a job that got nowhere
// is not run again while other closes get somewhere. Each returns the
// word the Edit acts on.
func (closer *Closer) runSettling(ctx context.Context, job Job) string {
	return closer.UntilProgressStops(ctx, func() Result {
		word, tally := closer.runTallied(ctx, job)
		return only(word, tally)
	}).Word
}

func (closer *Closer) perAlertSettling(ctx context.Context, job Job, decide func(linkdmanage.Alert) bool) string {
	return closer.UntilProgressStops(ctx, func() Result { return closer.perAlertResult(ctx, job, decide) }).Word
}

func (closer *Closer) oneByOneSettling(ctx context.Context, input EditInput, bucket Bucket, before time.Time) string {
	return closer.UntilProgressStops(ctx, func() Result { return closer.oneByOne(ctx, input, bucket, before) }).Word
}

// pruneOlder removes the record's entries of versions older than current:
// the link holds no alert of the strategy at another version.
func (closer *Closer) pruneOlder(ctx context.Context, scope Scope, current int64) {
	record, err := closer.record.Load(ctx, recordKey(scope))
	if err != nil {
		closer.count(OutcomeRecordUnavailable, 1)
		return
	}
	var older []string
	for _, entry := range record.Entries {
		if revision, err := strconv.ParseInt(entry.Version, 10, 64); err == nil && revision < current {
			older = append(older, entry.Fingerprint)
		}
	}
	closer.prune(ctx, scope, older)
}

func editReason(scope Scope, decision, version string) string {
	if version == "" {
		return fmt.Sprintf("alarmd: strategy %s changed (%s)", scope.StrategyID, decision)
	}
	return fmt.Sprintf("alarmd: strategy %s changed (%s) since version %s", scope.StrategyID, decision, version)
}

// enumerate is the strategy's buckets other than the current version's,
// each with the one alert that found it, and whether the walk reached its
// end.
func (closer *Closer) enumerate(ctx context.Context, scope Scope, current string) ([]Bucket, []linkdmanage.Alert, bool, string) {
	var buckets []Bucket
	var samples []linkdmanage.Alert
	for len(buckets) < maxBuckets {
		filter, err := enumerationFilter(scope, current, buckets)
		if err == nil {
			filter, err = activeOnly(filter)
		}
		if err != nil {
			closer.count(OutcomeFilterRefused, 1)
			return nil, nil, false, OutcomeFilterRefused
		}
		page, err := closer.link.Search(ctx, scope.TenantID, filter.body, "", 1)
		if err != nil {
			return nil, nil, false, closer.failed(err)
		}
		if len(page.Items) == 0 {
			return buckets, samples, true, ""
		}
		alert := page.Items[0]
		bucket, ok := bucketOf(alert)
		if !ok {
			// A version the filter required and the alert does not carry as
			// a decimal: no bucket can exclude it, so the walk stops here.
			closer.count(OutcomeSampleUnreadable, 1)
			return buckets, samples, false, ""
		}
		buckets = append(buckets, bucket)
		samples = append(samples, alert)
	}
	closer.count(OutcomeBucketsTruncated, 1)
	return buckets, samples, false, ""
}

func bucketOf(alert linkdmanage.Alert) (Bucket, bool) {
	version, ok := alert.Label("strategy_version")
	if !ok || !decimal(version) {
		return Bucket{}, false
	}
	digest, _ := alert.Label("strategy_facts_digest")
	if digest != "" && !hexDigest(digest) {
		return Bucket{}, false
	}
	_, noData := alert.Dimensions[contract.NoDataDimensionTag]
	return Bucket{Version: version, Digest: digest, NoData: noData}, true
}

type extraData struct {
	EvaluationFamily string          `json:"evaluation_family"`
	StrategyFacts    json.RawMessage `json:"strategy_facts"`
}

// sampleFamily is the alert's extra_data.evaluation_family, empty when
// unreadable.
func sampleFamily(alert linkdmanage.Alert) string {
	var extra extraData
	if json.Unmarshal(alert.ExtraData, &extra) != nil {
		return ""
	}
	return extra.EvaluationFamily
}

func sampleFacts(alert linkdmanage.Alert) (contract.StrategyFactsV2, bool) {
	var extra extraData
	var facts contract.StrategyFactsV2
	if json.Unmarshal(alert.ExtraData, &extra) != nil || len(extra.StrategyFacts) == 0 || json.Unmarshal(extra.StrategyFacts, &facts) != nil {
		return contract.StrategyFactsV2{}, false
	}
	return facts, true
}

// oneByOne reads a bucket's alerts (only the ones opened before before,
// when it is set), up to maxAlertsPerTurn a round from where the last
// round stopped, decides each from its own record, and
// closes the ones decided by their ids, under the bucket's filter. A bucket
// a whole pass ended under the Plans' current key, with every close it
// decided settled, is not read again while the key holds and no alert has
// opened in it since the newest one the pass read - one search of a single
// alert asks: the alerts read decide as they did (Plan.Key), and an alert
// can still join a bucket of an older version, from a Slot frozen with the
// older Plans and replayed or taken over after the edit. A changed key or a
// newer alert starts a new pass from the bucket's first alert. Its Result
// is each close's word and tally, and job_incomplete while the pass has
// pages left; its Word is the walk's own: empty, or the word that ended it
// early (filter_refused, a link word, unsupported).
func (closer *Closer) oneByOne(ctx context.Context, input EditInput, bucket Bucket, before time.Time) Result {
	started := time.Now()
	filter, err := BucketFilter(input.Scope, bucket)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return only(OutcomeFilterRefused, jobTally{})
	}
	read := filter
	if !before.IsZero() {
		read, err = openedBefore(filter, before)
	}
	if err == nil {
		read, err = activeOnly(read)
	}
	walkKey := filterKey(read)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return only(OutcomeFilterRefused, jobTally{})
	}
	planKey := input.Plan.Key()
	closer.mu.Lock()
	walked, remembered := closer.walked[walkKey]
	closer.mu.Unlock()
	if remembered && walked.key == planKey {
		newer, err := openedAfter(read, walked.newest)
		if err != nil {
			closer.count(OutcomeFilterRefused, 1)
			return only(OutcomeFilterRefused, jobTally{})
		}
		page, err := closer.link.Search(ctx, input.Scope.TenantID, newer.body, "", 1)
		if err != nil {
			return only(closer.failed(err), jobTally{})
		}
		if len(page.Items) == 0 {
			closer.count(OutcomeWalkRemembered, 1)
			return Result{}
		}
		closer.count(OutcomeWalkReopened, 1)
		closer.mu.Lock()
		delete(closer.walked, walkKey)
		delete(closer.passes, walkKey)
		closer.mu.Unlock()
	}
	closer.mu.Lock()
	pass := closer.passes[walkKey]
	if pass == nil || pass.key != planKey {
		pass = &walkPass{key: planKey, clean: true}
		closer.passes[walkKey] = pass
		delete(closer.walked, walkKey)
		closer.buckets[walkKey] = ""
	}
	after := closer.buckets[walkKey]
	closer.mu.Unlock()
	decided := map[string][]string{}
	n := 0
	for n < maxAlertsPerTurn {
		page, err := closer.link.Search(ctx, input.Scope.TenantID, read.body, after, min(linkdmanage.MaxLimit, maxAlertsPerTurn-n))
		if err != nil {
			return only(closer.failed(err), jobTally{})
		}
		for _, alert := range page.Items {
			n++
			if alert.CreateAt.After(pass.newest) {
				closer.mu.Lock()
				pass.newest = alert.CreateAt
				closer.mu.Unlock()
			}
			if alert.Status != "active" {
				continue
			}
			record := recordOf(alert)
			decision, note := editclose.Decide(input.Plan, record)
			closer.count("edit_"+decision, 1)
			closer.count("edit_"+note, 1)
			if record.Revision > 0 && !editclose.RevisionsComparable(record.Revision, input.Plan.Revision) {
				closer.count("edit_"+editclose.OutcomeRevisionIncomparable, 1)
			}
			if editclose.Closes(decision) {
				decided[decision] = append(decided[decision], alert.AlertID)
			}
		}
		after = page.Next
		if page.Next == "" || len(page.Items) == 0 {
			after = ""
			break
		}
	}
	closer.count(OutcomeAlertsRead, n)
	if after != "" {
		closer.count(OutcomeAlertsTruncated, 1)
		closer.notice(Notice{Outcome: OutcomeAlertsTruncated, Scope: input.Scope, Decision: "edit", Read: n, Elapsed: time.Since(started)})
	}
	closer.mu.Lock()
	closer.buckets[walkKey] = after
	closer.mu.Unlock()
	// result holds each close's word and tally, for the caller to judge
	// whether to walk again within the round; its Word stays the walk's
	// own, empty unless a close was unsupported.
	var result Result
	decisions := make([]string, 0, len(decided))
	for decision := range decided {
		decisions = append(decisions, decision)
	}
	sort.Strings(decisions)
	for _, decision := range decisions {
		ids := decided[decision]
		closer.count(OutcomeAlertDecidedClose, len(ids))
		for start := 0; start < len(ids); start += linkdmanage.MaxLimit {
			batch := ids[start:min(len(ids), start+linkdmanage.MaxLimit)]
			byID, err := alertsFilter(input.Scope, bucket, batch)
			if err != nil {
				closer.count(OutcomeFilterRefused, 1)
				closer.mu.Lock()
				pass.clean = false
				closer.mu.Unlock()
				continue
			}
			word, tally := closer.runTallied(ctx, Job{Filter: byID, Decision: decision, Version: bucket.Version, Digest: bucket.Digest,
				Revision: input.Plan.Revision, Expect: Expectation{Count: len(batch), Source: ExpectDecidedIDs},
				Reason: editReason(input.Scope, decision, bucket.Version), Recheck: filter})
			result.add(word, tally)
			if word == OutcomeUnsupported {
				result.Word = word
				return result
			}
			if !settledWord(word) {
				closer.mu.Lock()
				pass.clean = false
				closer.mu.Unlock()
			}
		}
	}
	if after == "" {
		// The pass reached the bucket's last alert. Remembered when every
		// close it decided settled; otherwise the next round walks it again.
		closer.mu.Lock()
		if pass.clean {
			closer.walked[walkKey] = walkedBucket{key: planKey, newest: pass.newest}
		}
		delete(closer.passes, walkKey)
		closer.mu.Unlock()
	} else {
		// Pages left: the walk is not done, whatever its closes answered.
		result.add(OutcomeJobIncomplete, jobTally{})
	}
	result.Word = ""
	return result
}

// recordOf is what Decide reads of an alert: the record the link returned.
func recordOf(alert linkdmanage.Alert) editclose.Alert {
	record := editclose.Alert{Severity: alert.Severity}
	if version, ok := alert.Label("strategy_version"); ok {
		record.Revision, _ = strconv.ParseInt(version, 10, 64)
	}
	if alert.Dimensions != nil {
		record.DimensionsRead = true
		for name := range alert.Dimensions {
			record.DimensionKeys = append(record.DimensionKeys, name)
		}
		sort.Strings(record.DimensionKeys)
	}
	var extra extraData
	if json.Unmarshal(alert.ExtraData, &extra) == nil {
		record.EvaluationFamily = extra.EvaluationFamily
		if len(extra.StrategyFacts) > 0 && string(extra.StrategyFacts) != "null" {
			record.Facts = extra.StrategyFacts
		}
	}
	return record
}
