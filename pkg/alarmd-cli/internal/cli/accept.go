// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Verdicts of one acceptance item. FAIL and READ_FAILED fail the run: a
// fact that could not be read is not a pass. UNDECIDED is a fact read whose
// denominator is zero, so it proves neither way; NOT_BUILT is a fact the
// deployment has no reading for yet.
const (
	verdictPass       = "PASS"
	verdictFail       = "FAIL"
	verdictInfo       = "INFO"
	verdictReadFailed = "READ_FAILED"
	verdictNotBuilt   = "NOT_BUILT"
	verdictUndecided  = "UNDECIDED"
)

// acceptOperations are the reads the acceptance runs; each must be listed
// and available.
var acceptOperations = []string{"fleet.get", "k8s.pods", "k8s.events", "k8s.logs", "store.info", "metrics.get",
	"diagnose.environment", "runtime.get", "strategy.get"}

// acceptSurfaceDegradations are the fleet degradations that say the public
// surface restriction is not in force.
var acceptSurfaceDegradations = []string{"METRICS_INTERNAL_LISTEN_MISSING", "CLI_AUTH_UNAVAILABLE"}

// acceptControlDegradations are the fleet degradations that say a strategy
// change does not reach what the fleet executes - in the publication, the
// activation, or the view reaching the workers - each held to the fleet's
// bound: no refresh round has succeeded, the activation is behind the
// publication, nobody holds the leader lease, the round accepts none of the
// strategies the source lists, the cutover held query groups back, the
// leader cannot publish its view, or no worker holds a stream to take it.
// The refresh counters cannot say it: a round that fails is not in
// source_refresh_total, and the rest happen after the round.
var acceptControlDegradations = []string{"CONTROL_SOURCE_STALE", "ACTIVATION_BEHIND", "CONTROL_LEADER_ABSENT", "SOURCE_BLOCKED",
	"ACTIVATION_BLOCKED", "VIEW_PUBLISH_FAILING", "VIEW_STREAM_NO_SESSIONS"}

// acceptHorizonSources are the sources a no-data horizon may be read from.
var acceptHorizonSources = []string{"DEFAULT", "VALUES", "DYNAMIC"}

const (
	// acceptMemoryRatio is the share of maxmemory a Redis may use.
	acceptMemoryRatio = 0.8
	// acceptControlLoopSeconds bounds the slowest control loop turn, read as
	// the upper bound of the first histogram bucket holding every turn.
	acceptControlLoopSeconds = 300
	// acceptDefaultWindow is how far apart the two source refresh reads are.
	acceptDefaultWindow = 5 * time.Minute
	// acceptDetailRunes bounds one item's detail on stdout.
	acceptDetailRunes = 400
)

// Metric families the acceptance reads, as the registry names them.
const (
	metricSourceRefresh   = "bkmonitor_alarmd_source_refresh_total"
	metricScopeClose      = "bkmonitor_alarmd_target_scope_close_total"
	metricLoopTurn        = "bkmonitor_alarmd_loop_turn_duration_seconds"
	metricRecoveryBeside  = "bkmonitor_alarmd_trigger_recovery_beside_level_total"
	metricRecoveryHeld    = "bkmonitor_alarmd_trigger_recovery_held_total"
	metricEventsByKind    = "bkmonitor_alarmd_output_events_by_kind_total"
	metricEventsRejected  = "bkmonitor_alarmd_output_events_rejected_total"
	metricRejectedOverrun = "bkmonitor_alarmd_output_events_rejected_strategies_overflow_total"
)

type acceptItem struct {
	Item    string `json:"item"`
	Verdict string `json:"verdict"`
	Detail  string `json:"detail"`
	// Column is governance for an item that lists what the operators of the
	// strategies have to fix; it never fails the acceptance.
	Column string `json:"column,omitempty"`
}

const columnGovernance = "governance"

// Who a refusal rule of output_events_rejected_total belongs to. alarmd's
// are its own defects -- an invariant of the converter, an encoding, a
// field it failed to carry -- and fail the acceptance. The strategy's are
// the strategy configuration or the data a writer produced, which the
// operators fix: they are listed for governance and do not fail it. A rule
// this table does not name is counted as alarmd's, since nobody can say it
// is anyone else's.
const (
	refusalOwnerAlarmd   = "alarmd"
	refusalOwnerStrategy = "strategy"
)

var refusalOwners = map[string]string{
	"standard_identity_missing":  refusalOwnerAlarmd,
	"standard_action_unknown":    refusalOwnerAlarmd,
	"standard_levels_invalid":    refusalOwnerAlarmd,
	"standard_too_many_levels":   refusalOwnerAlarmd,
	"standard_encode":            refusalOwnerAlarmd,
	"event_invalid":              refusalOwnerAlarmd,
	"format_unsupported":         refusalOwnerAlarmd,
	"legacy_context_missing":     refusalOwnerAlarmd,
	"legacy_conversion_rejected": refusalOwnerAlarmd,
	"legacy_output_invalid":      refusalOwnerAlarmd,
	"producer_refused":           refusalOwnerAlarmd,
	"_other":                     refusalOwnerAlarmd,
	"standard_business_identity": refusalOwnerStrategy,
	"legacy_strategy_invalid":    refusalOwnerStrategy,
	"legacy_payload_too_large":   refusalOwnerStrategy,
}

// refusalOwner is who a rule belongs to, and whether the table names it.
func refusalOwner(rule string) (string, bool) {
	owner, known := refusalOwners[rule]
	if !known {
		return refusalOwnerAlarmd, false
	}
	return owner, true
}

// acceptRun is one acceptance: the session it reads with, the catalog
// revision every invoke names, the items decided and the answers read.
type acceptRun struct {
	app      *App
	p        Profile
	revision string
	items    []acceptItem
	answers  map[string]any
	// fatal is a call that could not be made at all; the run stops there.
	fatal error
}

func (run *acceptRun) add(item, verdict, detail string) {
	run.items = append(run.items, acceptItem{Item: item, Verdict: verdict, Detail: detail})
}

// read invokes one operation. An answer with a result is read whatever its
// status: metrics.get answers partial when an asked-for family is absent,
// and the families it has are still read. No result, an error status or a
// refused call is a read failure, named.
func (run *acceptRun) read(name, operation string, params map[string]any) (map[string]any, string) {
	if params == nil {
		params = map[string]any{}
	}
	m, status, err := run.app.channelRequest("invoke", operation, params, run.revision, run.p)
	if err == nil && status == http.StatusUnauthorized && run.p.RefreshToken != "" {
		if renewed, renewErr := run.app.ensureSession(run.p.EnvironmentID, true); renewErr == nil {
			run.p = renewed
			m, status, err = run.app.channelRequest("invoke", operation, params, run.revision, run.p)
		}
	}
	if err != nil {
		return nil, err.Error()
	}
	run.answers[name] = redact(m, []string{run.p.AccessToken, run.p.RefreshToken})
	result := objectField(m, "result")
	if status < 200 || status >= 300 || stringField(m, "status") == "error" || result == nil {
		failure := objectField(m, "error")
		if code := stringField(failure, "code"); code != "" {
			return nil, code + ": " + stringField(failure, "message")
		}
		return nil, fmt.Sprintf("HTTP %d, status %s, no result", status, stringField(m, "status"))
	}
	return m, ""
}

// accept runs every acceptance item against the environment and writes one
// record: each item's verdict and the answers it was decided from. The exit
// is 1 when any item is FAIL or READ_FAILED.
func (a *App) accept(p Profile, expectBuild string, window time.Duration) int {
	run := &acceptRun{app: a, p: p, answers: map[string]any{}}
	fmt.Fprintln(a.Err, "Reading the operation catalog...")
	discovered, status, err := a.channelRequest("discover", "", nil, "", p)
	if err != nil {
		return a.fail("request_failed", err.Error(), 1)
	}
	if status < 200 || status >= 300 || stringField(discovered, "status") != "ok" {
		return a.emitOrLapsed(p, discovered, []string{p.AccessToken, p.RefreshToken}, status)
	}
	run.revision = stringField(objectField(discovered, "meta"), "catalog_revision")
	run.checkCatalog(discovered, expectBuild)
	fmt.Fprintln(a.Err, "Reading the fleet and the workload...")
	replicas := run.checkFleet()
	pods := run.checkWorkload()
	targets := pods
	if len(targets) == 0 {
		targets = replicas
	}
	fmt.Fprintln(a.Err, "Reading the control source refresh counts, first of two...")
	refreshStarted := time.Now()
	before := run.refreshCounts("refresh-before", targets)
	fmt.Fprintln(a.Err, "Reading the Redis servers...")
	run.checkStore()
	fmt.Fprintln(a.Err, "Reading each replica's counters...")
	run.checkMetrics(targets)
	fmt.Fprintln(a.Err, "Diagnosing every strategy...")
	run.checkDiagnosis()
	fmt.Fprintln(a.Err, "Reading the public surface without a session...")
	run.checkPublicSurface()
	run.checkRefresh(before, refreshStarted, window, targets)
	return a.acceptRecord(run)
}

func (run *acceptRun) checkCatalog(discovered map[string]any, expectBuild string) {
	build := stringField(objectField(discovered, "meta"), "build")
	switch {
	case expectBuild == "":
		run.add("session and build", verdictInfo, "answering process build="+build+"; no --expect-build given")
	case strings.HasPrefix(build, expectBuild):
		run.add("session and build", verdictPass, "build="+build+" starts with "+expectBuild)
	default:
		run.add("session and build", verdictFail, "build="+build+", expected "+expectBuild)
	}
	listed := map[string]map[string]any{}
	operations, _ := objectField(discovered, "result")["operations"].([]any)
	for _, raw := range operations {
		row, _ := raw.(map[string]any)
		listed[stringField(row, "operation")] = objectField(row, "availability")
	}
	var missing, down []string
	for _, operation := range acceptOperations {
		availability, ok := listed[operation]
		switch {
		case !ok:
			missing = append(missing, operation)
		case availability["available"] != true:
			down = append(down, operation+"("+stringField(availability, "reason")+")")
		}
	}
	verdict := verdictPass
	if len(missing)+len(down) > 0 {
		verdict = verdictFail
	}
	run.add("operations listed and available", verdict, fmt.Sprintf("missing %s; unavailable %s", listOrNone(missing), listOrNone(down)))
}

// checkFleet reads the fleet: the public surface degradations, the control
// source's, the no-data horizon, and the replicas it knows.
func (run *acceptRun) checkFleet() []string {
	m, failure := run.read("fleet", "fleet.get", nil)
	if m == nil {
		for _, item := range []string{"public surface not degraded", "control source publishes and activates", "no-data horizon"} {
			run.add(item, verdictReadFailed, "fleet.get: "+failure)
		}
		return nil
	}
	result := objectField(m, "result")
	kinds := map[string]bool{}
	degradations, _ := result["degradations"].([]any)
	for _, raw := range degradations {
		kinds[stringField(raw.(map[string]any), "kind")] = true
	}
	var surface []string
	for _, kind := range acceptSurfaceDegradations {
		if kinds[kind] {
			surface = append(surface, kind)
		}
	}
	if len(surface) > 0 {
		run.add("public surface not degraded", verdictFail, "fleet degradations include "+strings.Join(surface, ", "))
	} else {
		run.add("public surface not degraded", verdictPass, "none of "+strings.Join(acceptSurfaceDegradations, ", "))
	}
	var control []string
	for _, raw := range degradations {
		degradation := raw.(map[string]any)
		kind := stringField(degradation, "kind")
		if !contains(acceptControlDegradations, kind) {
			continue
		}
		words := kind
		if replica := stringField(degradation, "replica"); replica != "" {
			words += " on " + replica
		}
		if stage := stringField(degradation, "stage"); stage != "" {
			words += " at " + stage
		}
		control = append(control, words)
	}
	if len(control) > 0 {
		run.add("control source publishes and activates", verdictFail, "fleet degradations include "+strings.Join(control, "; "))
	} else {
		run.add("control source publishes and activates", verdictPass, "none of "+strings.Join(acceptControlDegradations, ", "))
	}
	horizon := objectField(result, "no_data_horizon")
	source := stringField(horizon, "source")
	switch {
	case horizon == nil:
		run.add("no-data horizon", verdictFail, "fleet.get has no no_data_horizon: not in effect, or no replica reported it")
	case contains(acceptHorizonSources, source):
		run.add("no-data horizon", verdictPass, fmt.Sprintf("seconds=%v source=%s replica=%s", horizon["seconds"], source, stringField(horizon, "replica")))
	default:
		run.add("no-data horizon", verdictFail, "source="+source+", not one of "+strings.Join(acceptHorizonSources, ", "))
	}
	var replicas []string
	rows, _ := result["per_replica"].([]any)
	for _, raw := range rows {
		if replica := stringField(raw.(map[string]any), "replica"); replica != "" {
			replicas = append(replicas, replica)
		}
	}
	return replicas
}

// checkWorkload reads the Pods and their events. Restarts and warnings are
// reported, not judged: the acceptance is of the release, and a restart's
// cause is read from its termination.
func (run *acceptRun) checkWorkload() []string {
	m, failure := run.read("pods", "k8s.pods", nil)
	var names []string
	if m == nil {
		run.add("pods", verdictReadFailed, "k8s.pods: "+failure)
	} else {
		pods, _ := objectField(m, "result")["pods"].([]any)
		var states, restarted []string
		for _, raw := range pods {
			pod, _ := raw.(map[string]any)
			name := stringField(pod, "name")
			names = append(names, name)
			restarts := 0
			containers, _ := pod["containers"].([]any)
			for _, rawContainer := range containers {
				container, _ := rawContainer.(map[string]any)
				restarts += number(container["restarts"])
				if last := objectField(container, "last_termination"); number(container["restarts"]) > 0 && last != nil {
					restarted = append(restarted, fmt.Sprintf("%s/%s last ended %s (%s, exit %v)", name, stringField(container, "name"),
						stringField(last, "state"), stringField(last, "reason"), last["exit_code"]))
				}
			}
			states = append(states, fmt.Sprintf("%s %s restarts=%d", name, stringField(pod, "phase"), restarts))
		}
		if len(names) == 0 {
			run.add("pods", verdictFail, "k8s.pods lists no Pod")
		} else {
			run.add("pods", verdictPass, strings.Join(states, "; "))
		}
		if len(restarted) > 0 {
			run.add("pod restarts", verdictInfo, strings.Join(restarted, "; "))
		}
	}
	m, failure = run.read("events", "k8s.events", nil)
	if m == nil {
		run.add("workload events", verdictReadFailed, "k8s.events: "+failure)
		return names
	}
	events, _ := objectField(m, "result")["events"].([]any)
	warnings := 0
	latest := ""
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		if stringField(event, "type") == "Warning" {
			if warnings == 0 {
				latest = "; newest Warning " + stringField(event, "reason") + ": " + stringField(event, "message")
			}
			warnings++
		}
	}
	run.add("workload events", verdictInfo, fmt.Sprintf("%d events, %d Warning%s", len(events), warnings, latest))
	return names
}

// checkStore reads every Redis alarmd uses: each must answer, evict
// nothing and keep below acceptMemoryRatio of its ceiling.
func (run *acceptRun) checkStore() {
	m, failure := run.read("store", "store.info", nil)
	if m == nil {
		for _, item := range []string{"redis read", "redis noeviction and nothing evicted", "redis memory"} {
			run.add(item, verdictReadFailed, "store.info: "+failure)
		}
		return
	}
	result := objectField(m, "result")
	servers, _ := result["servers"].([]any)
	var unread, policy, evicted, full, sizes []string
	for _, raw := range servers {
		server, _ := raw.(map[string]any)
		roles := joinAny(server["roles"])
		fields := objectField(server, "fields")
		if stringField(server, "status") != "ok" {
			unread = append(unread, roles+" status="+stringField(server, "status"))
			continue
		}
		if missing := joinAny(server["missing"]); missing != "" {
			unread = append(unread, roles+" missing "+missing)
		}
		if value := stringField(fields, "maxmemory_policy"); value != "noeviction" {
			policy = append(policy, roles+"="+value)
		}
		if value := stringField(fields, "evicted_keys"); value != "0" {
			evicted = append(evicted, roles+"="+value)
		}
		ceiling, ceilingErr := strconv.ParseFloat(stringField(fields, "maxmemory"), 64)
		used, usedErr := strconv.ParseFloat(stringField(fields, "used_memory"), 64)
		switch {
		case ceilingErr != nil || usedErr != nil:
			unread = append(unread, roles+" memory unreadable")
		case ceiling == 0:
			sizes = append(sizes, fmt.Sprintf("%s used %.0f, no ceiling", roles, used))
		default:
			sizes = append(sizes, fmt.Sprintf("%s %.1f%%", roles, 100*used/ceiling))
			if used/ceiling > acceptMemoryRatio {
				full = append(full, fmt.Sprintf("%s %.1f%%", roles, 100*used/ceiling))
			}
		}
	}
	if len(servers) == 0 || len(unread) > 0 || result["complete"] != true {
		run.add("redis read", verdictReadFailed, fmt.Sprintf("%d servers; unread %s", len(servers), listOrNone(unread)))
	} else {
		run.add("redis read", verdictPass, fmt.Sprintf("%d servers read whole", len(servers)))
	}
	if len(policy)+len(evicted) > 0 {
		run.add("redis noeviction and nothing evicted", verdictFail, "not noeviction "+listOrNone(policy)+"; evicted "+listOrNone(evicted))
	} else {
		run.add("redis noeviction and nothing evicted", verdictPass, "every server read is noeviction with evicted_keys=0")
	}
	if len(full) > 0 {
		run.add("redis memory", verdictFail, fmt.Sprintf("above %.0f%%: %s", 100*acceptMemoryRatio, strings.Join(full, ", ")))
	} else {
		run.add("redis memory", verdictPass, strings.Join(sizes, ", "))
	}
}

// metricsOf reads metric families from one replica; an empty replica is
// the answering one.
func (run *acceptRun) metricsOf(name string, names []string, target map[string]any) (map[string][]map[string]any, string) {
	params := map[string]any{"names": toAny(names)}
	for key, value := range target {
		params[key] = value
	}
	m, failure := run.read(name, "metrics.get", params)
	if m == nil {
		return nil, failure
	}
	result := objectField(m, "result")
	if gather := stringField(result, "gather_error"); gather != "" {
		return nil, "gather_error: " + gather
	}
	families := map[string][]map[string]any{}
	list, _ := result["families"].([]any)
	for _, raw := range list {
		family, _ := raw.(map[string]any)
		var series []map[string]any
		rows, _ := family["series"].([]any)
		for _, row := range rows {
			series = append(series, row.(map[string]any))
		}
		families[stringField(family, "name")] = series
	}
	return families, ""
}

func replicaTarget(replica string) map[string]any {
	if replica == "" {
		return nil
	}
	return map[string]any{"replica": replica}
}

// sum adds the values of the series whose labels hold every pair given.
func sum(series []map[string]any, labels ...string) float64 {
	total := 0.0
	for _, s := range series {
		matched := true
		for index := 0; index+1 < len(labels); index += 2 {
			matched = matched && stringField(objectField(s, "labels"), labels[index]) == labels[index+1]
		}
		if matched {
			total += float(s["value"])
		}
	}
	return total
}

func float(value any) float64 {
	switch v := value.(type) {
	case json.Number:
		f, _ := v.Float64()
		return f
	case float64:
		return v
	}
	return 0
}

// checkMetrics reads each replica's counters and decides the items on
// their sums. A replica that could not be read fails the read item; the
// sums of the rest are still read.
func (run *acceptRun) checkMetrics(targets []string) {
	names := []string{metricSourceRefresh, metricScopeClose, metricLoopTurn, metricRecoveryBeside, metricRecoveryHeld,
		metricEventsByKind, metricEventsRejected, metricRejectedOverrun}
	if len(targets) == 0 {
		targets = []string{""}
	}
	var failures, held []string
	var refreshed, recoveries, closeSendFailed, overflow float64
	beside := map[string]float64{}
	closes := map[string]float64{}
	rejectedBy := map[string]map[string]float64{refusalOwnerAlarmd: {}, refusalOwnerStrategy: {}}
	rejectedSum := map[string]float64{}
	var unnamedRules []string
	// A build that predates legacy_strategy_invalid counts the strategy's
	// compatible-protocol refusals under legacy_conversion_rejected; every
	// rule has its cell from startup, so the missing cell says which build.
	strategyRuleCounted := false
	rejectedRead := false
	worst, worstReplica := 0.0, ""
	read := 0
	for index, replica := range targets {
		families, failure := run.metricsOf(fmt.Sprintf("metrics-%d", index), names, replicaTarget(replica))
		if families == nil {
			failures = append(failures, replica+": "+failure)
			continue
		}
		read++
		if _, ok := families[metricRecoveryHeld]; ok {
			held = append(held, replica)
		}
		refreshed += sum(families[metricSourceRefresh])
		recoveries += sum(families[metricEventsByKind], "event_kind", "RECOVERY")
		for _, s := range families[metricRecoveryBeside] {
			beside[stringField(objectField(s, "labels"), "beside")] += float(s["value"])
		}
		for _, s := range families[metricScopeClose] {
			closes[stringField(objectField(s, "labels"), "outcome")] += float(s["value"])
		}
		closeSendFailed += sum(families[metricScopeClose], "outcome", "send_failed")
		if _, ok := families[metricEventsRejected]; ok {
			rejectedRead = true
			for _, s := range families[metricEventsRejected] {
				labels := objectField(s, "labels")
				rule := stringField(labels, "rule")
				strategyRuleCounted = strategyRuleCounted || rule == "legacy_strategy_invalid"
				owner, known := refusalOwner(rule)
				if value := float(s["value"]); value > 0 {
					rejectedBy[owner][rule+"/"+stringField(labels, "strategy")] += value
					rejectedSum[owner] += value
					if !known && !contains(unnamedRules, rule) {
						unnamedRules = append(unnamedRules, rule)
					}
				}
			}
			overflow += sum(families[metricRejectedOverrun])
		}
		for _, s := range families[metricLoopTurn] {
			if stringField(objectField(s, "labels"), "loop") != "control" || number(s["count"]) == 0 {
				continue
			}
			bound := slowestBound(s)
			if bound > worst {
				worst, worstReplica = bound, replica
			}
		}
	}
	if len(failures) > 0 {
		run.add("metrics read on every replica", verdictReadFailed, strings.Join(failures, "; "))
	} else {
		run.add("metrics read on every replica", verdictPass, fmt.Sprintf("%d replicas read", read))
	}
	if read == 0 {
		for _, item := range []string{"source refresh counted", "held recovery counter removed", "recovery beside another Level",
			"target out of scope closes", "control loop slowest turn", "output events refused by alarmd"} {
			run.add(item, verdictReadFailed, "no replica's metrics were read")
		}
		return
	}
	run.add("source refresh counted", passIf(refreshed > 0), fmt.Sprintf("%s summed over replicas %g", metricSourceRefresh, refreshed))
	if len(held) > 0 {
		run.add("held recovery counter removed", verdictFail, metricRecoveryHeld+" still registered on "+strings.Join(held, ", "))
	} else {
		run.add("held recovery counter removed", verdictPass, metricRecoveryHeld+" absent on every replica read")
	}
	run.checkBeside(beside, recoveries)
	// The alerts of a target that left the strategy's scope are closed, and the closes are sent: how many
	// went out is what the deployment did, not a fault. A close the producer would not take is.
	scopeVerdict := verdictInfo
	if closeSendFailed > 0 {
		scopeVerdict = verdictFail
	}
	run.add("target out of scope closes", scopeVerdict, fmt.Sprintf("send_failed %g (want 0); by outcome %s", closeSendFailed, floats(closes)))
	switch {
	case worst == 0:
		run.add("control loop slowest turn", verdictUndecided, "no control loop turn observed on any replica read")
	case worst > acceptControlLoopSeconds:
		run.add("control loop slowest turn", verdictFail, fmt.Sprintf("slowest turn at most %s s on %s, above %d s", boundText(worst), worstReplica, acceptControlLoopSeconds))
	default:
		run.add("control loop slowest turn", verdictPass, fmt.Sprintf("slowest turn at most %s s on %s; bound %d s", boundText(worst), worstReplica, acceptControlLoopSeconds))
	}
	run.checkRefusals(rejectedRead, rejectedBy, rejectedSum, unnamedRules, strategyRuleCounted, overflow)
}

// checkRefusals decides the output refusals by who they belong to: alarmd's
// fail the acceptance, the strategy's are listed for governance.
func (run *acceptRun) checkRefusals(read bool, by map[string]map[string]float64, sums map[string]float64, unnamed []string, strategyRuleCounted bool, overflow float64) {
	const own, governed = "output events refused by alarmd", "output events refused by strategy configuration or data"
	if !read {
		run.add(own, verdictNotBuilt, metricEventsRejected+" is not registered by this build")
		return
	}
	switch alarmd := sums[refusalOwnerAlarmd]; {
	case alarmd > 0:
		detail := fmt.Sprintf("%g events refused by rule/strategy %s", alarmd, floats(by[refusalOwnerAlarmd]))
		if len(unnamed) > 0 {
			detail += "; rules not in the ownership table, counted as alarmd's: " + strings.Join(unnamed, ", ")
		}
		if by[refusalOwnerAlarmd] != nil && !strategyRuleCounted && hasRule(by[refusalOwnerAlarmd], "legacy_conversion_rejected") {
			detail += "; this build has no legacy_strategy_invalid and counts the strategy's compatible-protocol refusals under legacy_conversion_rejected: read the refusal detail in the logs before taking it as alarmd's"
		}
		run.add(own, verdictFail, detail)
	default:
		run.add(own, verdictPass, "no refusal under a rule alarmd owns on any replica read")
	}
	detail := "none"
	if governed := sums[refusalOwnerStrategy]; governed > 0 {
		detail = fmt.Sprintf("%g events refused by rule/strategy %s", governed, floats(by[refusalOwnerStrategy]))
	}
	if overflow > 0 {
		detail += fmt.Sprintf("; %g refusals past the strategy label bound are under strategy _other", overflow)
	}
	run.items = append(run.items, acceptItem{Item: governed, Verdict: verdictInfo, Detail: detail, Column: columnGovernance})
}

// hasRule is whether any rule/strategy key of counts is under rule.
func hasRule(counts map[string]float64, rule string) bool {
	for key := range counts {
		if strings.HasPrefix(key, rule+"/") {
			return true
		}
	}
	return false
}

// checkBeside decides whether a RECOVERY was ever decided beside another
// Level: both kinds above zero pass. A zero is not a failure -- the counter
// counts occurrences, and none may have happened -- so with no RECOVERY
// written since start there is no denominator, and with some the zero still
// only says none of them met another Level in that state.
func (run *acceptRun) checkBeside(beside map[string]float64, recoveries float64) {
	unavailable, recovering := beside["level_unavailable"], beside["level_recovering"]
	detail := fmt.Sprintf("level_unavailable %g, level_recovering %g; RECOVERY events written since start %g", unavailable, recovering, recoveries)
	switch {
	case unavailable > 0 && recovering > 0:
		run.add("recovery beside another Level", verdictPass, detail)
	case recoveries == 0:
		run.add("recovery beside another Level", verdictUndecided, detail+": no RECOVERY written, nothing to count yet")
	default:
		run.add("recovery beside another Level", verdictUndecided, detail+": a zero here counts occurrences, not errors; read again after more recoveries")
	}
}

// slowestBound is the upper bound of the first bucket holding every
// observation: the slowest turn was at most that. Past the last finite
// bucket it is +Inf.
func slowestBound(series map[string]any) float64 {
	count := number(series["count"])
	best := -1.0
	for le, raw := range objectField(series, "buckets") {
		bound, err := strconv.ParseFloat(le, 64)
		if err != nil || number(raw) < count {
			continue
		}
		if best < 0 || bound < best {
			best = bound
		}
	}
	if best < 0 {
		return float64(1 << 62)
	}
	return best
}

func boundText(bound float64) string {
	if bound >= float64(1<<62) {
		return "+Inf"
	}
	return strconv.FormatFloat(bound, 'g', -1, 64)
}

func (run *acceptRun) checkDiagnosis() {
	var diagnosis diagnosisRun
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		diagnosis, run.p, err = run.app.diagnosisPages(run.p, run.revision)
		if err != nil || diagnosis.failed != nil || !diagnosis.changed {
			break
		}
	}
	switch {
	case err != nil:
		run.add("diagnosis covers every strategy", verdictReadFailed, err.Error())
	case diagnosis.failed != nil:
		failure := objectField(diagnosis.failed, "error")
		run.add("diagnosis covers every strategy", verdictReadFailed, stringField(failure, "code")+": "+stringField(failure, "message"))
	default:
		record := diagnosis.result()
		coverage := objectField(objectField(record, "result"), "coverage")
		run.answers["diagnosis"] = map[string]any{"status": record["status"], "summary": record["summary"], "coverage": coverage,
			"by_verdict": objectField(record, "result")["by_verdict"]}
		verdict := verdictFail
		if record["status"] == "ok" && coverage["holds"] == true {
			verdict = verdictPass
		}
		run.add("diagnosis covers every strategy", verdict, stringField(record, "summary"))
		run.checkDetecting(diagnosis, verdict == verdictPass)
	}
}

// acceptListedNotDetecting bounds how many strategies that are not detecting
// the item's detail names; the record keeps every one.
const acceptListedNotDetecting = 5

// checkDetecting reads what the diagnosis says of each strategy, beside its
// coverage: a diagnosis can cover every strategy of a set none of which
// detects, and that passed as a healthy deployment. Every strategy not
// DETECTING is listed with its reason and the refusals it carries - scope,
// field path and the compiler's own words - so one read names every layer a
// strategy stopped at rather than one release uncovering the next. A set
// that lists strategies and detects none fails; otherwise the table is
// information.
//
// On a diagnosis that did not cover every strategy the table is only what was
// read: it is information, never a failure decided on part of the set.
func (run *acceptRun) checkDetecting(diagnosis diagnosisRun, covered bool) {
	total, detecting := 0, diagnosis.byVerdict["DETECTING"]
	words := make([]string, 0, len(diagnosis.byVerdict))
	for word, n := range diagnosis.byVerdict {
		total += n
		if n > 0 {
			words = append(words, fmt.Sprintf("%s=%d", word, n))
		}
	}
	sort.Strings(words)
	type refusal struct {
		Scope     string `json:"scope,omitempty"`
		LevelID   any    `json:"level_id,omitempty"`
		Reason    string `json:"reason,omitempty"`
		FieldPath string `json:"field_path,omitempty"`
		Detail    string `json:"detail,omitempty"`
	}
	type notDetecting struct {
		StrategyID string    `json:"strategy_id"`
		Verdict    string    `json:"verdict"`
		Reason     string    `json:"reason,omitempty"`
		Refusals   []refusal `json:"refusals,omitempty"`
	}
	var listed []notDetecting
	for _, raw := range diagnosis.rows {
		row, _ := raw.(map[string]any)
		if stringField(row, "verdict") == "DETECTING" {
			continue
		}
		entry := notDetecting{StrategyID: stringField(row, "strategy_id"), Verdict: stringField(row, "verdict"), Reason: stringField(row, "reason")}
		dispositions, _ := row["dispositions"].([]any)
		for _, rawDisposition := range dispositions {
			disposition, _ := rawDisposition.(map[string]any)
			if word := stringField(disposition, "disposition"); word == "ACCEPTED" || word == "CONFIG_NOTED" {
				continue
			}
			entry.Refusals = append(entry.Refusals, refusal{Scope: stringField(disposition, "scope"), LevelID: disposition["level_id"],
				Reason: stringField(disposition, "reason"), FieldPath: stringField(disposition, "field_path"), Detail: stringField(disposition, "detail")})
		}
		listed = append(listed, entry)
	}
	run.answers["not_detecting"] = listed
	var named []string
	for _, entry := range listed {
		if len(named) == acceptListedNotDetecting {
			named = append(named, fmt.Sprintf("and %d more in the record", len(listed)-acceptListedNotDetecting))
			break
		}
		line := entry.StrategyID + " " + entry.Verdict
		if entry.Reason != "" {
			line += " " + entry.Reason
		}
		for _, one := range entry.Refusals {
			line += " [" + strings.TrimSpace(one.Scope+" "+one.Reason+" "+one.FieldPath) + "]"
			if one.Detail != "" {
				line += " " + one.Detail
			}
		}
		named = append(named, line)
	}
	detail := fmt.Sprintf("%d of %d detecting; %s", detecting, total, listOrNone(words))
	if len(named) > 0 {
		detail += "; not detecting: " + strings.Join(named, "; ")
	}
	verdict := verdictInfo
	switch {
	case !covered:
		detail = "coverage does not hold, so this is what was read, not the set: " + detail
	case total > 0 && detecting == 0:
		verdict = verdictFail
	}
	run.add("strategies detecting", verdict, detail)
}

// checkPublicSurface reads the public surface as anyone can, without a
// session: the restricted reads refuse and point at the login page, the
// health summary carries no coordinates, the metrics are not served and the
// login page is.
func (run *acceptRun) checkPublicSurface() {
	get := func(path string) (int, string, string, error) {
		client, base, release, err := run.app.environmentClient(run.p)
		defer release()
		if err != nil {
			return 0, "", "", err
		}
		base.Path += path
		request, err := http.NewRequest(http.MethodGet, base.String(), nil)
		if err != nil {
			return 0, "", "", err
		}
		request.Header.Set("User-Agent", "alarmd-cli/"+run.app.Version)
		response, err := client.Do(request)
		if err != nil {
			return 0, "", "", err
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return response.StatusCode, response.Header.Get("Content-Type"), string(body), nil
	}
	status, _, body, err := get("api/objects")
	switch {
	case err != nil:
		run.add("public: restricted read refused", verdictReadFailed, err.Error())
	default:
		var refusal map[string]any
		_ = json.Unmarshal([]byte(body), &refusal)
		// The server's refusal (datalink publicsurface.Refuse) names itself
		// and the login page inside error, not at the top level.
		failure := objectField(refusal, "error")
		refused := status == http.StatusForbidden && stringField(failure, "code") == "public_surface_restricted" &&
			run.reachesLoginPage("api/objects", stringField(failure, "login_href"))
		run.add("public: restricted read refused", passIf(refused), fmt.Sprintf("GET api/objects: %d %s", status, body))
	}
	status, _, body, err = get("api/health")
	switch {
	case err != nil:
		run.add("public: health carries no coordinates", verdictReadFailed, err.Error())
	default:
		var health map[string]any
		_ = json.Unmarshal([]byte(body), &health)
		var leaks []string
		for _, word := range []string{"address", "sentinel", "broker"} {
			if strings.Contains(body, word) {
				leaks = append(leaks, word)
			}
		}
		run.add("public: health carries no coordinates", passIf(status == http.StatusOK && health["restricted"] == true && len(leaks) == 0),
			fmt.Sprintf("GET api/health: %d restricted=%v coordinate words %s", status, health["restricted"], listOrNone(leaks)))
	}
	status, _, _, err = get("metrics")
	if err != nil {
		run.add("public: metrics not served", verdictReadFailed, err.Error())
	} else {
		run.add("public: metrics not served", passIf(status == http.StatusForbidden), fmt.Sprintf("GET metrics: %d", status))
	}
	status, contentType, _, err := get("cli")
	if err != nil {
		run.add("public: login page served", verdictReadFailed, err.Error())
	} else {
		run.add("public: login page served", passIf(status == http.StatusOK && strings.HasPrefix(contentType, "text/html")), fmt.Sprintf("GET cli: %d %s", status, contentType))
	}
}

// reachesLoginPage is whether href, a link the server wrote into its answer
// to path, leads to this environment's login page. The server computes it
// relative to the path it saw, so the link is resolved against the URL that
// was asked rather than compared as text: an entry whose prefix the server
// sees too writes another relative path to the same page.
func (run *acceptRun) reachesLoginPage(path, href string) bool {
	base, err := baseURL(run.p.PublicBaseURL)
	if err != nil || href == "" {
		return false
	}
	asked := *base
	asked.Path += path
	link, err := url.Parse(href)
	if err != nil {
		return false
	}
	login := *base
	login.Path += "cli"
	resolved := asked.ResolveReference(link)
	return resolved.Scheme == login.Scheme && resolved.Host == login.Host && resolved.Path == login.Path
}

// refreshReading is source_refresh_total by status as the Control Leader
// counts it, and which Leader term it was read from.
type refreshReading struct {
	byStatus map[string]float64
	leader   string
	failure  string
}

// read adds one answer's families to the reading.
func (reading *refreshReading) read(families map[string][]map[string]any) {
	for _, s := range families[metricSourceRefresh] {
		reading.byStatus[stringField(objectField(s, "labels"), "status")] += float(s["value"])
	}
}

// refreshCounts reads source_refresh_total from the Control Leader, which
// alone moves it. A server that cannot target the Leader is read on every
// replica and summed.
func (run *acceptRun) refreshCounts(name string, targets []string) refreshReading {
	reading := refreshReading{byStatus: map[string]float64{}}
	names := []string{metricSourceRefresh}
	families, failure := run.metricsOf(name, names, map[string]any{"control_leader": true})
	if families != nil {
		answer := objectField(objectField(run.answers[name].(map[string]any), "meta"), "control_leader")
		reading.leader = fmt.Sprintf("%s@%v", stringField(answer, "owner_id"), answer["owner_epoch"])
		reading.read(families)
		return reading
	}
	if !strings.HasPrefix(failure, "invalid_input") {
		reading.failure = "control_leader: " + failure
		return reading
	}
	if len(targets) == 0 {
		targets = []string{""}
	}
	var failures []string
	for index, replica := range targets {
		families, failure := run.metricsOf(fmt.Sprintf("%s-%d", name, index), names, replicaTarget(replica))
		if families == nil {
			failures = append(failures, replica+": "+failure)
			continue
		}
		reading.read(families)
	}
	reading.failure = strings.Join(failures, "; ")
	return reading
}

// checkRefresh reads the refresh counts a window after the first read and
// reports the increase by status: how many rounds the Control Leader
// published a change on, found unchanged, or lost to another writer over the
// window. Every round that reads a change publishes it, so the increase is a
// reading and not a verdict. A Leader change between the reads leaves no
// increase to read.
func (run *acceptRun) checkRefresh(before refreshReading, started time.Time, window time.Duration, targets []string) {
	const rounds, conflicts = "control source rounds over the window", "control source publication conflicts"
	if before.failure != "" {
		run.add(rounds, verdictReadFailed, before.failure)
		run.add(conflicts, verdictReadFailed, before.failure)
		return
	}
	if window <= 0 {
		run.add(rounds, verdictUndecided, "--window 0: the increase was not read")
	} else {
		if wait := window - time.Since(started); wait > 0 {
			fmt.Fprintf(run.app.Err, "Waiting %s for the second control source refresh read...\n", wait.Round(time.Second))
			time.Sleep(wait)
		}
		after := run.refreshCounts("refresh-after", targets)
		elapsed := time.Since(started).Round(time.Second)
		switch {
		case after.failure != "":
			run.add(rounds, verdictReadFailed, after.failure)
		case after.leader != before.leader:
			run.add(rounds, verdictUndecided, fmt.Sprintf("the Control Leader changed between the reads (%s, then %s)", before.leader, after.leader))
		default:
			delta := map[string]float64{}
			for status, value := range after.byStatus {
				delta[status] = value - before.byStatus[status]
			}
			run.add(rounds, verdictInfo, fmt.Sprintf("increase over %s: %s", elapsed, floats(delta)))
		}
		before = after
	}
	conflict := before.byStatus["PUBLICATION_CONFLICT"]
	run.add(conflicts, passIf(conflict == 0), fmt.Sprintf("PUBLICATION_CONFLICT since start %g", conflict))
}

// acceptRecord saves the whole run -- items and the answers they were
// decided from -- in one evidence file, and prints the items.
func (a *App) acceptRecord(run *acceptRun) int {
	counts := map[string]int{}
	failed := 0
	for index, item := range run.items {
		counts[item.Verdict]++
		if item.Verdict == verdictFail || item.Verdict == verdictReadFailed {
			failed++
		}
		if runes := []rune(item.Detail); len(runes) > acceptDetailRunes {
			run.items[index].Detail = string(runes[:acceptDetailRunes]) + "…"
		}
	}
	status, summary := "ok", fmt.Sprintf("验收通过：%d 项，无 FAIL 或 READ_FAILED", len(run.items))
	if failed > 0 {
		status, summary = "failed", fmt.Sprintf("验收未通过：%d 项中 %d 项为 FAIL 或 READ_FAILED", len(run.items), failed)
	}
	record := map[string]any{"status": status, "summary": summary,
		"result":    map[string]any{"items": run.items, "counts": counts, "answers": run.answers},
		"evidence":  map[string]any{"complete": failed == 0, "limitations": []any{}},
		"next_call": []any{}, "meta": map[string]any{"operation": "accept", "catalog_revision": run.revision}}
	path, err := a.saveEvidence(redact(record, []string{run.p.AccessToken, run.p.RefreshToken}).(map[string]any))
	if err != nil {
		return a.fail("evidence_write_failed", err.Error(), 1)
	}
	out := map[string]any{"status": status, "summary": summary, "result": map[string]any{"items": run.items, "counts": counts},
		"meta": map[string]any{"operation": "accept", "result_file": path}}
	if data, _ := json.Marshal(out); len(data)+1 > stdoutBudget {
		// Every verdict still fits; the details are in the saved record.
		bare := make([]acceptItem, len(run.items))
		for index, item := range run.items {
			bare[index] = acceptItem{Item: item.Item, Verdict: item.Verdict, Column: item.Column}
		}
		out["result"] = map[string]any{"items": bare, "counts": counts}
		out["result_omitted"] = true
	}
	if a.print(out) != 0 {
		return 1
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func passIf(ok bool) string {
	if ok {
		return verdictPass
	}
	return verdictFail
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

func joinAny(value any) string {
	var parts []string
	for _, raw := range asList(value) {
		parts = append(parts, fmt.Sprint(raw))
	}
	return strings.Join(parts, ",")
}

func toAny(values []string) []any {
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = value
	}
	return out
}

func floats(values map[string]float64) string {
	if len(values) == 0 {
		return "no series"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for index, key := range keys {
		parts[index] = fmt.Sprintf("%s=%g", key, values[key])
	}
	return strings.Join(parts, " ")
}
