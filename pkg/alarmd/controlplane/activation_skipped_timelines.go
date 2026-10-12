// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"sort"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Control Leader that did not write the activation head it reads - a
// restart, a new leader - gets the Plan records back from every active Query
// Group's open Segment (materializeActivationPlans). One timeline whose bytes
// did not decode used to fail that whole read: the activation did not load,
// nothing was cut over for any Query Group, and the repair of the one bad
// timeline (RepairUnreadableTimelines) was never reached. One bad key held
// the fleet's control plane.
//
// Now the read leaves such a Query Group out, names it, and goes on with the
// rest. Only bytes that do not decode are left out: a Redis read that fails,
// a reply that is an error, a transport that drops - none of that says
// anything about the timeline, and it fails the read as before, so a fault of
// the store never becomes a skip, a rebuild or a rewrite.

// SkippedTimelineReason says why a Query Group's timeline was left out of the
// activation read.
type SkippedTimelineReason string

const (
	// SkippedTimelineUndecodable: the bytes do not decode as a timeline. The
	// Leader rebuilds that Query Group's records from the publication it
	// runs and rewrites the timeline (RepairUnreadableTimelines).
	SkippedTimelineUndecodable SkippedTimelineReason = "undecodable"
	// SkippedTimelineNewerFormat: the bytes say they are a timeline of
	// another schema version, which a build that reads them wrote. Left
	// alone: rebuilding or rewriting them here would destroy what that build
	// runs by.
	SkippedTimelineNewerFormat SkippedTimelineReason = "newer_format"
)

// SkippedTimelineReasons is the closed list, for the counter's series.
var SkippedTimelineReasons = []SkippedTimelineReason{SkippedTimelineUndecodable, SkippedTimelineNewerFormat}

// MaxNamedSkippedTimelines bounds the names the activation row carries; the
// count says how many there are in all.
const MaxNamedSkippedTimelines = 64

// SkippedTimeline is one Query Group the activation read left out, and why.
type SkippedTimeline struct {
	QueryGroup execution.QueryGroupIdentity
	Reason     SkippedTimelineReason
}

// skippedTimelineReason is the reason a timeline that did not decode is left
// out under.
func skippedTimelineReason(raw []byte) SkippedTimelineReason {
	if writtenByAnotherSchema(raw) {
		return SkippedTimelineNewerFormat
	}
	return SkippedTimelineUndecodable
}

// undecodableSkipped is the Query Groups of skipped whose bytes did not
// decode, as a set; nil when there are none.
func undecodableSkipped(skipped []SkippedTimeline) map[execution.QueryGroupIdentity]struct{} {
	var set map[execution.QueryGroupIdentity]struct{}
	for _, entry := range skipped {
		if entry.Reason != SkippedTimelineUndecodable {
			continue
		}
		if set == nil {
			set = make(map[execution.QueryGroupIdentity]struct{}, len(skipped))
		}
		set[entry.QueryGroup] = struct{}{}
	}
	return set
}

// skippedTimelineState is every skip this process made, by reason, and the
// Query Groups the last activation read left out; read at scrape time and by
// the activation row.
type skippedTimelineState struct {
	mu      sync.Mutex
	counts  map[SkippedTimelineReason]uint64
	current []SkippedTimeline
}

// note records the Query Groups one activation read left out: each is
// counted, and they replace the ones the last read left out.
func (state *skippedTimelineState) note(skipped []SkippedTimeline) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.counts == nil {
		state.counts = make(map[SkippedTimelineReason]uint64, len(SkippedTimelineReasons))
	}
	for _, entry := range skipped {
		state.counts[entry.Reason]++
	}
	state.current = append([]SkippedTimeline(nil), skipped...)
}

// stillUnread replaces the Query Groups named as left out by the ones a
// write left unread, without counting them again: a Leader reads its own
// write back without reading the open Segments, so the name of a timeline it
// rewrote would otherwise stay on the row.
func (state *skippedTimelineState) stillUnread(previous []SkippedTimeline, rewritten map[execution.QueryGroupIdentity]struct{}) {
	var unread []SkippedTimeline
	for _, entry := range previous {
		if _, done := rewritten[entry.QueryGroup]; !done {
			unread = append(unread, entry)
		}
	}
	state.mu.Lock()
	state.current = unread
	state.mu.Unlock()
}

// SkippedTimelinesReading is what the metric and the activation row read: the
// count of every skip by reason, this process only, and the Query Groups the
// last activation read left out, the first MaxNamedSkippedTimelines of them
// in Query Group order, with how many there were.
type SkippedTimelinesReading struct {
	Counts map[SkippedTimelineReason]uint64
	Named  []SkippedTimeline
	Total  int
}

// SkippedTimelinesReading is the repository's reading; every reason is in
// Counts, zero included.
func (repository *RedisCatalogRepository) SkippedTimelinesReading() SkippedTimelinesReading {
	reading := SkippedTimelinesReading{Counts: make(map[SkippedTimelineReason]uint64, len(SkippedTimelineReasons))}
	if repository == nil {
		return reading
	}
	state := &repository.skippedTimelines
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, reason := range SkippedTimelineReasons {
		reading.Counts[reason] = state.counts[reason]
	}
	named := append([]SkippedTimeline(nil), state.current...)
	sort.Slice(named, func(i, j int) bool { return named[i].QueryGroup < named[j].QueryGroup })
	reading.Total = len(named)
	if len(named) > MaxNamedSkippedTimelines {
		named = named[:MaxNamedSkippedTimelines]
	}
	reading.Named = named
	return reading
}
