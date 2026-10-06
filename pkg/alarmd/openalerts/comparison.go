// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"sort"
)

// The comparison's bounds. It is read on every health request and carried in
// every replica's snapshot, so what it holds is a sample, never the sets.
const (
	ComparisonStrategies         = 8
	ComparisonSamplesPerStrategy = 3
	ComparisonSources            = 8
	// comparisonPrefix is how much of a fingerprint a sample shows: enough
	// to see two lists side by side, not enough to be the key.
	comparisonPrefix = 8
)

// The shapes a key can have, closed. The gate looks up the alert id this
// process sends, a 32-character lowercase hex digest; a set whose members
// are another shape was written under another fingerprint rule.
const (
	ShapeHex32    = "hex32"
	ShapeHex64    = "hex64"
	ShapeHexOther = "hex_other"
	ShapeNonHex   = "non_hex"
)

// ComparisonOtherSource is where alerts from sources past the bound are
// counted.
const ComparisonOtherSource = "other"

// ComparisonNoSource is where alerts the link listed without a source are
// counted, so an empty key never stands for them.
const ComparisonNoSource = "none"

// Comparison puts what this process sent beside what the link holds for the
// same strategies, so a gate that never finds its own alerts can be told
// apart by reading: members of another shape, alerts from another source, or
// our alerts present under another fingerprint.
type Comparison struct {
	// OwnEventSourceID is this deployment's source as the last successful
	// calibration named it; empty until one has.
	OwnEventSourceID string
	// Sent counts the keys this process sent and still holds as open,
	// SentShapes their shapes; MemberShapes the shapes of the members the
	// link holds for the strategies this process tracks.
	Sent         int
	SentShapes   map[string]int
	MemberShapes map[string]int
	// AlertSources counts the calibrated active alerts by their source,
	// with sources past ComparisonSources summed under "other".
	AlertSources map[string]int
	// Of the sent keys, how many a calibrated strategy lists as an active
	// alert's id, and how many as an active alert's fingerprint. Equal to
	// the id and not the fingerprint is an alert the link holds under
	// another fingerprint rule. Only strategies with a calibration count.
	SentInCalibrated        int
	SentMatchingAlertID     int
	SentMatchingFingerprint int
	Strategies              []ComparisonStrategy
}

// ComparisonStrategy is one strategy's sample: at most
// ComparisonSamplesPerStrategy of each list, each a fingerprint prefix.
type ComparisonStrategy struct {
	TenantID, StrategyID string
	Sent, Members        int
	Alerts               int
	Calibrated           bool
	SentSample           []string
	MemberSample         []string
	AlertSample          []ComparisonAlert
}

// ComparisonAlert is one active alert as the calibration listed it, reduced
// to its keys.
type ComparisonAlert struct {
	AlertID, Fingerprint, EventSourceID string
}

// Shape is the closed word for a key's form.
func Shape(value string) string {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ShapeNonHex
		}
	}
	switch len(value) {
	case 32:
		return ShapeHex32
	case 64:
		return ShapeHex64
	}
	if value == "" {
		return ShapeNonHex
	}
	return ShapeHexOther
}

func samplePrefix(value string) string {
	if len(value) <= comparisonPrefix {
		return value
	}
	return value[:comparisonPrefix]
}

// Comparison is the side-by-side view. Nil on a copy that does not read the
// index, where there is no link state to compare against.
func (cache *Cache) Comparison() *Comparison {
	if cache == nil || cache.index == nil {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	result := &Comparison{OwnEventSourceID: cache.index.eventSourceID,
		SentShapes: map[string]int{}, MemberShapes: map[string]int{}, AlertSources: map[string]int{}}

	sent := map[StrategyKey][]string{}
	for m := range cache.added {
		sent[m.key] = append(sent[m.key], m.fingerprint)
		result.Sent++
		result.SentShapes[Shape(m.fingerprint)]++
	}

	sources := map[string]int{}
	for key, entry := range cache.index.entries {
		for value := range entry.index {
			result.MemberShapes[Shape(value)]++
		}
		if !cache.calibrated(entry, now) {
			continue
		}
		ids := make(map[string]struct{}, len(entry.alerts))
		fingerprints := make(map[string]struct{}, len(entry.alerts))
		for _, alert := range entry.alerts {
			source := alert.EventSourceID
			if source == "" {
				source = ComparisonNoSource
			}
			sources[source]++
			ids[alert.AlertID] = struct{}{}
			fingerprints[alert.Fingerprint] = struct{}{}
		}
		for _, value := range sent[key] {
			result.SentInCalibrated++
			if _, ok := ids[value]; ok {
				result.SentMatchingAlertID++
			}
			if _, ok := fingerprints[value]; ok {
				result.SentMatchingFingerprint++
			}
		}
	}
	// The own source is always listed, zero included: "none of the alerts is
	// ours" is the reading, and a missing key would not say it.
	if result.OwnEventSourceID != "" {
		sources[result.OwnEventSourceID] += 0
	}
	result.AlertSources = boundSources(sources, result.OwnEventSourceID)

	// The sample: strategies this process sent to first, since those are
	// the ones the gate asks about, then by key so two reads agree.
	keys := make([]StrategyKey, 0, len(cache.index.entries))
	for key := range cache.index.entries {
		keys = append(keys, key)
	}
	for key := range sent {
		if _, tracked := cache.index.entries[key]; !tracked {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		si, sj := len(sent[keys[i]]) > 0, len(sent[keys[j]]) > 0
		if si != sj {
			return si
		}
		if keys[i].TenantID != keys[j].TenantID {
			return keys[i].TenantID < keys[j].TenantID
		}
		return keys[i].StrategyID < keys[j].StrategyID
	})
	if len(keys) > ComparisonStrategies {
		keys = keys[:ComparisonStrategies]
	}
	for _, key := range keys {
		entry := cache.index.entries[key]
		row := ComparisonStrategy{TenantID: key.TenantID, StrategyID: key.StrategyID, Sent: len(sent[key])}
		row.SentSample = sampleOf(sent[key])
		if entry != nil {
			members := make([]string, 0, len(entry.index))
			for value := range entry.index {
				members = append(members, value)
			}
			row.Members = len(members)
			row.MemberSample = sampleOf(members)
			row.Calibrated = cache.calibrated(entry, now)
			if row.Calibrated {
				row.Alerts = len(entry.alerts)
				alerts := append([]Alert(nil), entry.alerts...)
				sort.Slice(alerts, func(i, j int) bool { return alerts[i].AlertID < alerts[j].AlertID })
				for i := 0; i < len(alerts) && i < ComparisonSamplesPerStrategy; i++ {
					row.AlertSample = append(row.AlertSample, ComparisonAlert{AlertID: samplePrefix(alerts[i].AlertID),
						Fingerprint: samplePrefix(alerts[i].Fingerprint), EventSourceID: alerts[i].EventSourceID})
				}
			}
		}
		result.Strategies = append(result.Strategies, row)
	}
	return result
}

func sampleOf(values []string) []string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	if len(sorted) > ComparisonSamplesPerStrategy {
		sorted = sorted[:ComparisonSamplesPerStrategy]
	}
	for i := range sorted {
		sorted[i] = samplePrefix(sorted[i])
	}
	return sorted
}

// boundSources keeps the own source and the largest others up to the bound;
// the rest are summed under "other", so the map's size never follows the
// link's.
func boundSources(sources map[string]int, own string) map[string]int {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == own) != (names[j] == own) {
			return names[i] == own
		}
		if sources[names[i]] != sources[names[j]] {
			return sources[names[i]] > sources[names[j]]
		}
		return names[i] < names[j]
	})
	result := make(map[string]int, ComparisonSources+1)
	for i, name := range names {
		if i < ComparisonSources && name != ComparisonOtherSource {
			result[name] = sources[name]
			continue
		}
		result[ComparisonOtherSource] += sources[name]
	}
	return result
}
