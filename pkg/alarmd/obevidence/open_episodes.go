// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obevidence

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// OpenEpisodesSummary is one strategy's record of the episodes alarmd opened,
// as the strategy-wide closes read it: the count they expect a dry run to
// match is the entries opened from the record's start plus its settle time
// (at least an hour), so a record whose entries are fewer than the link's
// alerts of the strategy is one those closes refuse. The entries themselves
// are counted, not listed.
type OpenEpisodesSummary struct {
	Exists    bool `json:"exists"`
	Truncated bool `json:"truncated"`
	// Since is the record's start; zero when it has none.
	Since      time.Time `json:"since,omitzero"`
	TTLMS      int64     `json:"ttl_ms"`
	Entries    int       `json:"entries"`
	Unreadable int       `json:"unreadable"`
	// ByVersion is the readable entries by the strategy revision they
	// opened at.
	ByVersion map[string]int `json:"by_version"`
	// OpenedFirst and OpenedLast bound the readable entries' openings;
	// OpenedAfterAnHour counts those at least an hour past Since, the least
	// a close vouches from.
	OpenedFirst       time.Time `json:"opened_first,omitzero"`
	OpenedLast        time.Time `json:"opened_last,omitzero"`
	OpenedAfterAnHour int       `json:"opened_after_an_hour"`
}

// openEpisodes reads one strategy's record by tenant and strategy.
func (service *Service) openEpisodes(ctx context.Context, request StoreRequest) Result {
	binding := service.options.OpenEpisodes
	if !positiveID(request.StrategyID) || !identifier(request.Tenant) || request.GroupID != "" || request.QueryGroup != "" ||
		len(request.Fields) > 0 || request.ObjectDigest != "" || request.Business != "" ||
		request.Version != "" || request.Cursor != "" || request.Limit != 0 {
		return invalid(request.Family, binding)
	}
	if binding.Client == nil || binding.Location.Prefix == "" {
		return result(request.Family, binding, "not_configured")
	}
	store, err := openepisodes.NewStore(binding.Client, binding.Location.Prefix)
	if err != nil {
		return result(request.Family, binding, "not_configured")
	}
	read, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	key := openepisodes.Key{TenantID: request.Tenant, StrategyID: request.StrategyID}
	record, err := store.Load(read, key)
	if err != nil {
		binding.failed("unreachable")
		return result(request.Family, binding, "unreachable")
	}
	r := result(request.Family, binding, "ok")
	r.Location.Key = store.KeyOf(key)
	r.Type = "hash"
	r.Complete = !record.Truncated
	if !record.Exists {
		r.Status = "missing"
	}
	summary := OpenEpisodesSummary{Exists: record.Exists, Truncated: record.Truncated, Since: record.Since, ByVersion: map[string]int{}}
	if ttl, err := binding.Client.PTTL(read, r.Location.Key).Result(); err == nil {
		summary.TTLMS = ttl.Milliseconds()
	}
	var openings []int64
	for _, entry := range record.Entries {
		summary.Entries++
		if entry.Unreadable {
			summary.Unreadable++
			continue
		}
		summary.ByVersion[entry.Version]++
		openings = append(openings, entry.OpenedAt)
		if !record.Since.IsZero() && entry.OpenedAt >= record.Since.Add(time.Hour).Unix() {
			summary.OpenedAfterAnHour++
		}
	}
	if len(openings) > 0 {
		sort.Slice(openings, func(i, j int) bool { return openings[i] < openings[j] })
		summary.OpenedFirst = time.Unix(openings[0], 0).UTC()
		summary.OpenedLast = time.Unix(openings[len(openings)-1], 0).UTC()
	}
	r.Value = summary
	return r
}

// The page of open_episodes_entries: DefaultEpisodeEntries when the request
// names none, never more than MaxEpisodeEntries.
const (
	DefaultEpisodeEntries = 200
	MaxEpisodeEntries     = 500
)

// OpenEpisodeEntries is a page of one strategy's record, entry by entry,
// in fingerprint order: each entry's fingerprint, the revision it opened at
// and when. Matching counts the page counts, NextCursor the fingerprint to
// ask after for the next page, empty on the last.
type OpenEpisodeEntries struct {
	Exists    bool `json:"exists"`
	Truncated bool `json:"truncated"`
	// Since is the record's start; zero when it has none.
	Since time.Time `json:"since,omitzero"`
	// Matching is how many entries the request's version selects, all of
	// them without a version, unreadable ones included.
	Matching   int                `json:"matching"`
	Entries    []OpenEpisodeEntry `json:"entries"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// OpenEpisodeEntry is one entry: an unreadable one carries only its
// fingerprint, and is listed whatever version the request names, since its
// version cannot be read.
type OpenEpisodeEntry struct {
	Fingerprint string    `json:"fingerprint"`
	Version     string    `json:"version,omitempty"`
	OpenedAt    time.Time `json:"opened_at,omitzero"`
	Unreadable  bool      `json:"unreadable,omitempty"`
}

// openEpisodeEntries lists one strategy's record a page at a time.
func (service *Service) openEpisodeEntries(ctx context.Context, request StoreRequest) Result {
	binding := service.options.OpenEpisodes
	if !positiveID(request.StrategyID) || !identifier(request.Tenant) || request.GroupID != "" || request.QueryGroup != "" ||
		len(request.Fields) > 0 || request.ObjectDigest != "" || request.Business != "" ||
		request.Limit < 0 || request.Limit > MaxEpisodeEntries || len(request.Cursor) > 64 || len(request.Version) > 64 ||
		strings.ContainsAny(request.Cursor+request.Version, " \t\r\n") {
		return invalid(request.Family, binding)
	}
	if binding.Client == nil || binding.Location.Prefix == "" {
		return result(request.Family, binding, "not_configured")
	}
	store, err := openepisodes.NewStore(binding.Client, binding.Location.Prefix)
	if err != nil {
		return result(request.Family, binding, "not_configured")
	}
	read, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	key := openepisodes.Key{TenantID: request.Tenant, StrategyID: request.StrategyID}
	record, err := store.Load(read, key)
	if err != nil {
		binding.failed("unreachable")
		return result(request.Family, binding, "unreachable")
	}
	r := result(request.Family, binding, "ok")
	r.Location.Key = store.KeyOf(key)
	r.Type = "hash"
	r.Complete = !record.Truncated
	if !record.Exists {
		r.Status = "missing"
	}
	limit := request.Limit
	if limit == 0 {
		limit = DefaultEpisodeEntries
	}
	page := OpenEpisodeEntries{Exists: record.Exists, Truncated: record.Truncated, Since: record.Since, Entries: []OpenEpisodeEntry{}}
	selected := make([]openepisodes.Entry, 0, len(record.Entries))
	for _, entry := range record.Entries {
		if entry.Unreadable || request.Version == "" || entry.Version == request.Version {
			selected = append(selected, entry)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Fingerprint < selected[j].Fingerprint })
	page.Matching = len(selected)
	for _, entry := range selected {
		if entry.Fingerprint <= request.Cursor {
			continue
		}
		if len(page.Entries) == limit {
			page.NextCursor = page.Entries[len(page.Entries)-1].Fingerprint
			break
		}
		listed := OpenEpisodeEntry{Fingerprint: entry.Fingerprint, Unreadable: entry.Unreadable}
		if !entry.Unreadable {
			listed.Version, listed.OpenedAt = entry.Version, time.Unix(entry.OpenedAt, 0).UTC()
		}
		page.Entries = append(page.Entries, listed)
	}
	r.Value = page
	return r
}
