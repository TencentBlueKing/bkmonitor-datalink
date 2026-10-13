// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

func TestTheCandidacyClassIsReadOffTheRolesAndNothingElse(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		roles roles.Set
		blank bool
		want  string
	}{
		{name: "default all-roles form", want: ownership.ControlCandidacyPreferred},
		{name: "control only", roles: roles.Set{roles.Control}, want: ownership.ControlCandidacyPreferred},
		{name: "control and worker", roles: roles.Set{roles.Control, roles.Worker}, want: ownership.ControlCandidacyPreferred},
		{name: "worker only", roles: roles.Set{roles.Worker}, want: ownership.ControlCandidacyFallback},
		{name: "worker and channel", roles: roles.Set{roles.Worker, roles.Channel}, want: ownership.ControlCandidacyFallback},
		// A Worker configured without a strategy source is a valid Worker
		// that could not compile anything if it led: it is no candidate.
		{name: "worker without control inputs", roles: roles.Set{roles.Worker}, blank: true, want: ""},
		{name: "channel only", roles: roles.Set{roles.Channel}, want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			cfg.Roles = testCase.roles
			if testCase.blank {
				cfg.PhaseTwo.Control.StrategyCachePrefix = ""
			}
			if got := phaseTwoControlCandidacy(cfg); got != testCase.want {
				t.Fatalf("phaseTwoControlCandidacy() = %q, want %q", got, testCase.want)
			}
		})
	}
	cfg := validGoAccessRuntimeConfig()
	if window := controlCandidacyWindow(cfg); window != cfg.PhaseTwo.Worker.RegistrationTTL.Duration() || window <= 0 {
		t.Fatalf("candidacy window = %s, want the instance record TTL %s", window, cfg.PhaseTwo.Worker.RegistrationTTL.Duration())
	}
}

func TestOnlyAReadyCandidateContendsAndAFallbackOnlyForAFreeUnclaimedLease(t *testing.T) {
	const preferred, fallback = ownership.ControlCandidacyPreferred, ownership.ControlCandidacyFallback
	for _, testCase := range []struct {
		name                               string
		class                              string
		ready, leaseLive, preferredPresent bool
		want                               bool
	}{
		{name: "no candidate", class: "", ready: true},
		{name: "preferred still starting or stopping", class: preferred},
		{name: "fallback still starting or stopping", class: fallback},
		{name: "preferred, ready", class: preferred, ready: true, want: true},
		// The script refuses a preferred candidate while the lease is live;
		// it does not wait for preferred processes, being one.
		{name: "preferred, another preferred present", class: preferred, ready: true, preferredPresent: true, want: true},
		{name: "preferred, lease live", class: preferred, ready: true, leaseLive: true},
		{name: "fallback, lease live", class: fallback, ready: true, leaseLive: true},
		{name: "fallback, free lease, a preferred present", class: fallback, ready: true, preferredPresent: true},
		{name: "fallback, free lease, no preferred", class: fallback, ready: true, want: true},
		{name: "unknown class", class: "standby", ready: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := controlContentionAllowed(testCase.class, testCase.ready, testCase.leaseLive, testCase.preferredPresent); got != testCase.want {
				t.Fatalf("controlContentionAllowed() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func preferredRecordForTest(id, incarnation string, ready bool, expiresAt time.Time) ownership.InstanceRegistration {
	return ownership.InstanceRegistration{InstanceID: id, Roles: roles.Set{roles.Control}, Incarnation: incarnation,
		ExpiresAt: expiresAt, ControlCandidacy: ownership.ControlCandidacyPreferred, ControlCandidacyReady: ready}
}

// readySinceForTest is a preferred record that has said ready since the
// time given.
func readySinceForTest(record ownership.InstanceRegistration, since time.Time) ownership.InstanceRegistration {
	record.ControlCandidacyReadySince = &since
	return record
}

func TestOnlyAStablePreferredProcessHoldsAFallbackOff(t *testing.T) {
	const window = time.Minute
	at := time.Unix(1_800_000_000, 0)
	stable := func(id string, ready bool, expiresAt time.Time) ownership.InstanceRegistration {
		return readySinceForTest(preferredRecordForTest(id, "1", ready, expiresAt), at.Add(-window))
	}
	for _, testCase := range []struct {
		name       string
		candidates []ownership.InstanceRegistration
		want       bool
	}{
		{name: "none registered"},
		{name: "live, ready for the whole window", candidates: []ownership.InstanceRegistration{stable("c", true, at.Add(time.Second))}, want: true},
		// A process that keeps restarting is ready for a moment in each
		// incarnation and never for a window: it holds nobody off.
		{name: "live, ready for less than the window", candidates: []ownership.InstanceRegistration{
			readySinceForTest(preferredRecordForTest("c", "1", true, at.Add(time.Minute)), at.Add(-window+time.Second))}},
		{name: "ready with no ready-since time, as an older record", candidates: []ownership.InstanceRegistration{
			preferredRecordForTest("c", "1", true, at.Add(time.Minute))}},
		{name: "live, not ready", candidates: []ownership.InstanceRegistration{preferredRecordForTest("c", "1", false, at.Add(time.Minute))}},
		{name: "stable, expired", candidates: []ownership.InstanceRegistration{stable("c", true, at)}},
		{name: "itself", candidates: []ownership.InstanceRegistration{stable("self", true, at.Add(time.Minute))}},
		{name: "a control record from before candidacy is not ready", candidates: []ownership.InstanceRegistration{
			{InstanceID: "old", Roles: roles.Set{roles.Control}, Incarnation: "1", ExpiresAt: at.Add(time.Minute)}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := preferredCandidatePresent(testCase.candidates, "self", at, window); got != testCase.want {
				t.Fatalf("preferredCandidatePresent() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestAPreferredProcessThatIsNotYetStableLeavesAFreeLeaseToAReadyFallback(t *testing.T) {
	for _, testCase := range []struct {
		name                             string
		stable, leaseLive, fallbackReady bool
		want                             bool
	}{
		{name: "stable, free lease, a ready fallback", stable: true, fallbackReady: true, want: true},
		{name: "stable, free lease, no fallback", stable: true, want: true},
		// The default all-roles form: no fallback exists, so a process
		// that has just started takes a free lease at once, as before.
		{name: "not yet stable, free lease, no ready fallback", want: true},
		{name: "not yet stable, free lease, a ready fallback", fallbackReady: true},
		{name: "stable, lease live", stable: true, leaseLive: true},
		{name: "not yet stable, lease live", leaseLive: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := preferredContentionAllowed(testCase.stable, testCase.leaseLive, testCase.fallbackReady); got != testCase.want {
				t.Fatalf("preferredContentionAllowed() = %v, want %v", got, testCase.want)
			}
		})
	}
	at := time.Unix(1_800_000_000, 0)
	const window = time.Minute
	stableSince, youngSince := at.Add(-window), at.Add(-window+time.Second)
	fallback := func(id string, ready bool, expiresAt time.Time, since *time.Time) ownership.InstanceRegistration {
		return ownership.InstanceRegistration{InstanceID: id, Roles: roles.Set{roles.Worker}, Incarnation: "1", ExpiresAt: expiresAt,
			ControlCandidacy: ownership.ControlCandidacyFallback, ControlCandidacyReady: ready, ControlCandidacyReadySince: since}
	}
	for _, testCase := range []struct {
		name       string
		candidates []ownership.InstanceRegistration
		want       bool
	}{
		{name: "none registered"},
		{name: "live and ready for a window", candidates: []ownership.InstanceRegistration{fallback("w", true, at.Add(time.Second), &stableSince)}, want: true},
		{name: "live and ready, younger than a window", candidates: []ownership.InstanceRegistration{fallback("w", true, at.Add(time.Minute), &youngSince)}},
		{name: "live, not ready", candidates: []ownership.InstanceRegistration{fallback("w", false, at.Add(time.Minute), &stableSince)}},
		{name: "ready, expired", candidates: []ownership.InstanceRegistration{fallback("w", true, at, &stableSince)}},
		{name: "itself", candidates: []ownership.InstanceRegistration{fallback("self", true, at.Add(time.Minute), &stableSince)}},
		{name: "a preferred record is no fallback", candidates: []ownership.InstanceRegistration{preferredRecordForTest("c", "1", true, at.Add(time.Minute))}},
	} {
		t.Run("fallback "+testCase.name, func(t *testing.T) {
			if got := stableFallbackPresent(testCase.candidates, "self", at, window); got != testCase.want {
				t.Fatalf("stableFallbackPresent() = %v, want %v", got, testCase.want)
			}
		})
	}
	young := preferredRecordForTest("c", "1", true, at.Add(time.Minute))
	young.ControlCandidacyReadySince = &youngSince
	for _, testCase := range []struct {
		name       string
		candidates []ownership.InstanceRegistration
		want       bool
	}{
		{name: "none registered"},
		{name: "ready however young", candidates: []ownership.InstanceRegistration{young}, want: true},
		{name: "not ready", candidates: []ownership.InstanceRegistration{preferredRecordForTest("c", "1", false, at.Add(time.Minute))}},
		{name: "ready, expired", candidates: []ownership.InstanceRegistration{preferredRecordForTest("c", "1", true, at)}},
		{name: "itself", candidates: []ownership.InstanceRegistration{preferredRecordForTest("self", "1", true, at.Add(time.Minute))}},
		{name: "a fallback record is no preferred", candidates: []ownership.InstanceRegistration{fallback("w", true, at.Add(time.Minute), &stableSince)}},
	} {
		t.Run("preferred "+testCase.name, func(t *testing.T) {
			if got := readyPreferredPresent(testCase.candidates, "self", at); got != testCase.want {
				t.Fatalf("readyPreferredPresent() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestAHandbackWaitsForAWholeWindowOfUnbrokenReadiness(t *testing.T) {
	const window = time.Minute
	start := time.Unix(1_800_000_000, 0)
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	ready := func(seconds int, incarnation string) []ownership.InstanceRegistration {
		return []ownership.InstanceRegistration{preferredRecordForTest("control", incarnation, true, at(seconds).Add(window))}
	}
	notReady := func(seconds int) []ownership.InstanceRegistration {
		return []ownership.InstanceRegistration{preferredRecordForTest("control", "1", false, at(seconds).Add(window))}
	}
	type step struct {
		seconds    int
		candidates []ownership.InstanceRegistration
		reset      bool
		want       bool
	}
	// Rounds that returned early read nothing: the watch has gone 40 s
	// without a reading at +60, past two reconcile rounds, so the readiness
	// it last saw at 0 is not counted as unbroken through them; read every
	// round from +60, the window is due a full window later.
	gapSteps := []step{{seconds: 0, candidates: ready(0, "1")}, {seconds: 60, candidates: ready(60, "1")}}
	for seconds := 65; seconds < 120; seconds += 5 {
		gapSteps = append(gapSteps, step{seconds: seconds, candidates: ready(seconds, "1")})
	}
	gapSteps = append(gapSteps, step{seconds: 120, candidates: ready(120, "1"), want: true})
	for _, testCase := range []struct {
		name   string
		maxGap time.Duration
		steps  []step
	}{
		{name: "rounds nobody read are a break", maxGap: 10 * time.Second, steps: gapSteps},
		{name: "ready for the whole window", steps: []step{
			{seconds: 0, candidates: ready(0, "1")}, {seconds: 30, candidates: ready(30, "1")},
			{seconds: 59, candidates: ready(59, "1")}, {seconds: 60, candidates: ready(60, "1"), want: true},
		}},
		{name: "a flap shorter than the window starts it again", steps: []step{
			{seconds: 0, candidates: ready(0, "1")}, {seconds: 30, candidates: notReady(30)},
			{seconds: 35, candidates: ready(35, "1")}, {seconds: 60, candidates: ready(60, "1")},
			{seconds: 90, candidates: ready(90, "1")}, {seconds: 95, candidates: ready(95, "1"), want: true},
		}},
		{name: "a round that does not see the record starts it again", steps: []step{
			{seconds: 0, candidates: ready(0, "1")}, {seconds: 30},
			{seconds: 60, candidates: ready(60, "1")}, {seconds: 119, candidates: ready(119, "1")},
			{seconds: 120, candidates: ready(120, "1"), want: true},
		}},
		{name: "a restart is a new process with a window of its own", steps: []step{
			{seconds: 0, candidates: ready(0, "1")}, {seconds: 50, candidates: ready(50, "2")},
			{seconds: 60, candidates: ready(60, "2")}, {seconds: 110, candidates: ready(110, "2"), want: true},
		}},
		{name: "an unreadable round starts it again", steps: []step{
			{seconds: 0, candidates: ready(0, "1")}, {seconds: 30, reset: true},
			{seconds: 60, candidates: ready(60, "1")}, {seconds: 90, candidates: ready(90, "1")},
			{seconds: 120, candidates: ready(120, "1"), want: true},
		}},
		{name: "an expired record is not ready", steps: []step{
			{seconds: 0, candidates: ready(0, "1")},
			{seconds: 61, candidates: []ownership.InstanceRegistration{preferredRecordForTest("control", "1", true, at(61))}},
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var watch controlHandbackWatch
			for _, step := range testCase.steps {
				if step.reset {
					watch.reset()
					continue
				}
				successor, due := watch.observe(step.candidates, "fallback-self", at(step.seconds), window, testCase.maxGap)
				if due != step.want || (due && successor != "control") {
					t.Fatalf("at +%ds: observe() = (%q, %v), want due %v", step.seconds, successor, due, step.want)
				}
			}
		})
	}
}

// Closing the alerts of a strategy that no longer exists is alert
// correctness, the core control a fallback Leader carries: every candidate
// runs the loop when the link's Console is configured, so a Worker that
// leads while the control process is gone closes them too. A process that
// cannot lead, or a deployment without the Console, does not.
func TestEveryControlCandidateRunsTheDeletedStrategyClose(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		roles   roles.Set
		blank   bool
		console bool
		want    bool
	}{
		{name: "default all-roles form", console: true, want: true},
		{name: "control only", roles: roles.Set{roles.Control}, console: true, want: true},
		{name: "worker only, a fallback candidate", roles: roles.Set{roles.Worker}, console: true, want: true},
		{name: "worker without control inputs", roles: roles.Set{roles.Worker}, blank: true, console: true, want: false},
		{name: "channel only", roles: roles.Set{roles.Channel}, console: true, want: false},
		{name: "no Console", roles: roles.Set{roles.Worker}, console: false, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			cfg.Roles = testCase.roles
			if testCase.blank {
				cfg.PhaseTwo.Control.StrategyCachePrefix = ""
			}
			if got := absentCloseWired(cfg, testCase.console); got != testCase.want {
				t.Fatalf("absentCloseWired() = %t, want %t", got, testCase.want)
			}
		})
	}
}

// The gap a handback watch allows between readings is two reconcile rounds.
func TestTheHandbackGapIsTwoReconcileRounds(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	if got, want := controlHandbackMaxGap(cfg), 2*cfg.PhaseTwo.Control.ReconcileInterval.Duration(); got != want || got <= 0 {
		t.Fatalf("controlHandbackMaxGap() = %s, want %s", got, want)
	}
}
