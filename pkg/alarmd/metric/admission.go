// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import "strconv"

// Series admission is on the hot path - once per series per plan - so it is
// counted directly rather than through an observation per decision. Only the
// filter name, the outcome and a bounded reason are recorded; no host, plan or
// strategy identity reaches the labels.
var admissionResults = map[string]struct{}{"admitted": {}, "rejected": {}}

// The reason vocabulary is closed on purpose: it is a metric label, and a
// free-form reason turns one series into as many as there are strings.
var admissionFilters = map[string]struct{}{"target_scope": {}, "host_status": {}, "none": {}}
var admissionReasons = map[string]struct{}{
	"in_scope": {}, "out_of_scope": {}, "scope_empty": {}, "plan_not_indexed": {}, "none": {},
	// Target scope on object identity: a record that built no identity is a
	// defect on the writing or querying side every time, and an identity the
	// target did not name is either a record outside the target or a target
	// written in another representation than the data; the two are named
	// apart from out_of_scope so the first is alertable and the second is
	// readable against in_scope.
	"object_identity_missing": {}, "object_identity_unmatched": {},
	// Host status: the reasons matter separately because they call for
	// different actions - a disabled host is the filter working, an unknown
	// host is a CMDB gap, and unavailable facts mean it is not filtering.
	// The two indexes enrichment consults are written by different jobs, so
	// each names its own unavailability.
	"monitoring_disabled": {}, "host_unknown": {}, "host_identity_invalid": {},
	"host_facts_unavailable": {}, "service_instance_facts_unavailable": {},
}

// RecordSeriesAdmission counts one admission decision.
func (r *Recorder) RecordSeriesAdmission(filter, result, reason string) {
	if r == nil {
		return
	}
	if _, known := admissionResults[result]; !known {
		return
	}
	if filter == "" {
		filter = "none"
	}
	if reason == "" {
		reason = "none"
	}
	if _, known := admissionFilters[filter]; !known {
		filter = "none"
	}
	if _, known := admissionReasons[reason]; !known {
		reason = "none"
	}
	r.phaseTwo.seriesAdmission.WithLabelValues(filter, result, reason).Inc()
}

var cmdbIndexReasons = map[string]struct{}{
	"none": {}, "never_loaded": {}, "index_stale": {}, "index_empty": {}, "no_store": {},
}

// SetCMDBHostIndex publishes what the target filter is currently deciding on.
//
// The numbers matter together: a filter deciding on an empty or long-stale
// index is not filtering correctly, and without the age it is impossible to
// tell a correct "out of scope" from one caused by a cache that stopped being
// refreshed.
func (r *Recorder) SetCMDBHostIndex(hosts int, ageSeconds float64, sourceAgeSeconds float64, degraded bool, reason string) {
	if r == nil {
		return
	}
	r.phaseTwo.cmdbIndexHosts.Set(float64(hosts))
	r.phaseTwo.cmdbIndexAge.WithLabelValues("index").Set(ageSeconds)
	r.phaseTwo.cmdbIndexAge.WithLabelValues("source").Set(sourceAgeSeconds)
	if reason == "" {
		reason = "none"
	}
	value := 0.0
	if degraded {
		value = 1
	}
	if _, known := cmdbIndexReasons[reason]; !known {
		reason = "none"
	}
	r.phaseTwo.cmdbIndexDegraded.Reset()
	r.phaseTwo.cmdbIndexDegraded.WithLabelValues(reason).Set(value)
}

// SetCMDBServiceInstanceIndex publishes how many service instances the index
// holds. Zero beside a non-zero service_instance_facts_unavailable count is
// the signature of an instance cache nobody writes.
func (r *Recorder) SetCMDBServiceInstanceIndex(instances int) {
	if r == nil {
		return
	}
	r.phaseTwo.cmdbIndexServiceInstances.Set(float64(instances))
}

// RecordUnmappedSeverity counts one event whose alert level had no name in
// this build. The level is a small bounded number stated in strategy
// configuration, so it is safe as a label; anything outside that is folded.
func (r *Recorder) RecordUnmappedSeverity(level uint32) {
	if r == nil {
		return
	}
	name := "other"
	if level <= 64 {
		name = strconv.FormatUint(uint64(level), 10)
	}
	r.phaseTwo.unmappedSeverity.WithLabelValues(name).Inc()
}

// SetHostDisableMonitorStates publishes how many host states the access path
// treats as not monitored. Zero means the filter is not installed.
func (r *Recorder) SetHostDisableMonitorStates(states int) {
	if r == nil {
		return
	}
	r.phaseTwo.hostDisableMonitorStates.Set(float64(states))
}
