// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type shieldEventReadFailure struct {
	store.Repository
	eventID string
	err     error
}

func (r *shieldEventReadFailure) GetEvent(ctx context.Context, tenant, id string) (store.StoredEvent, error) {
	if id == r.eventID && r.err != nil {
		return store.StoredEvent{}, r.err
	}
	return r.Repository.GetEvent(ctx, tenant, id)
}

func TestShieldTimerReleasesMissingSourceEvent(t *testing.T) {
	for _, mode := range []string{"found", "missing", "wrapped-missing", "timeout", "canceled", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			repo := &shieldEventReadFailure{Repository: memory.New()}
			clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
			state, action := &shieldHook{}, &shieldHook{}
			p, _ := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, state, action)
			opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
			id := opening.Event.RelatedAlertIDs[0]
			before, err := repo.GetAlert(t.Context(), "tenant", id)
			if err != nil {
				t.Fatal(err)
			}
			repo.eventID = opening.Event.EventID
			switch mode {
			case "missing":
				repo.err = store.ErrNotFound
			case "wrapped-missing":
				repo.err = fmt.Errorf("read source event: %w", store.ErrNotFound)
			case "timeout":
				repo.err = context.DeadlineExceeded
			case "canceled":
				repo.err = context.Canceled
			case "unavailable":
				repo.err = errors.New("storage unavailable")
			}
			clock.at = clock.at.Add(time.Second) // 策略仍在生效，不能靠到期掩盖源事件缺失。
			report, err := p.RecheckShield(t.Context(), "tenant", id, 0)
			after, readErr := repo.GetAlert(t.Context(), "tenant", id)
			if readErr != nil {
				t.Fatal(readErr)
			}
			missing := mode == "missing" || mode == "wrapped-missing"
			if mode == "timeout" || mode == "canceled" || mode == "unavailable" {
				if err == nil || report.Changed || !after.Alert.Shield.Active || after.Alert.Revision != before.Alert.Revision || len(state.calls) != 1 {
					t.Fatal("read failure changed shield", report, err)
				}
				return
			}
			if err != nil || report.Changed != missing || after.Alert.Shield.Active == missing || len(action.calls) != 0 {
				t.Fatal("incorrect shield cleanup", report, err)
			}
			if !missing {
				return
			}
			if report.Outcome != "changed" || report.RemainingBindings != 0 || after.Alert.Shield.NextCheckAt != nil || after.Alert.Status != domain.AlertStatusActive || len(state.calls) != 2 {
				t.Fatal("cleanup did not publish unshielded state", report)
			}
			steps := report.Decision.Steps
			if len(steps) != 1 || steps[0].Outcome != "released" || steps[0].ReasonCode != "binding_event_missing" {
				t.Fatal("missing cleanup diagnostic", report.Decision)
			}
			logs, err := repo.ListAlertLogs(t.Context(), "tenant", id, store.PageRequest{})
			if err != nil {
				t.Fatal(err)
			}
			unshields := 0
			for _, log := range logs.Logs {
				if log.OperationKind == domain.OperationKindUnshield {
					unshields++
				}
			}
			if unshields != 1 {
				t.Fatalf("unshield logs=%d", unshields)
			}
			repeated, err := p.RecheckShield(t.Context(), "tenant", id, 0)
			if err != nil || repeated.Changed || repeated.Outcome != "inactive" || len(state.calls) != 2 || len(action.calls) != 0 {
				t.Fatal("cleanup replay created side effects", repeated, err)
			}
		})
	}
}

func TestShieldMissingEventOnlyReleasesAffectedBinding(t *testing.T) {
	for _, path := range []string{"timer", "event"} {
		for _, missingFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing-first=%t", path, missingFirst), func(t *testing.T) {
				repo := memory.New()
				clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
				p, s := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, &shieldHook{}, &shieldHook{})
				opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
				stored, err := repo.GetAlert(t.Context(), "tenant", opening.Event.RelatedAlertIDs[0])
				if err != nil {
					t.Fatal(err)
				}
				alert := stored.Alert.Clone()
				valid := alert.Shield.Bindings[0]
				missing := valid
				missing.SourceEventID = shieldEvent("missing", clock).EventID
				missing.BindingID = shieldBindingID("tenant", alert.AlertID, missing)
				alert.Shield.Bindings = []domain.ShieldBinding{valid, missing}
				if missingFirst {
					alert.Shield.Bindings = []domain.ShieldBinding{missing, valid}
				}
				reads := map[string]int{}
				s.Events = func(ctx context.Context, tenant, id string) (store.StoredEvent, error) {
					reads[id]++
					return repo.GetEvent(ctx, tenant, id)
				}
				at := clock.at.Add(time.Second)
				var result lifecycle.ShieldEvaluation
				if path == "timer" {
					result, err = s.Recheck(t.Context(), alert, at, nil)
				} else {
					snapshot, snapshotErr := (Snapshotter{Catalog: s.Loader.Catalog}).Snapshot(t.Context(), opening.Event, at)
					if snapshotErr != nil {
						t.Fatal(snapshotErr)
					}
					result, err = s.Check(t.Context(), opening.Event, opening.Event.Evaluations[0], snapshot, alert, nil)
				}
				if err != nil || !result.Shield.Active || len(result.Shield.Bindings) != 1 || result.Shield.Bindings[0].BindingID != valid.BindingID || result.Decision == nil {
					t.Fatal("missing event blocked or removed valid binding", result.Shield, err)
				}
				released := 0
				for _, step := range result.Decision.Steps {
					if step.BindingID == missing.BindingID && step.Outcome == "released" && step.ReasonCode == "binding_event_missing" {
						released++
					}
				}
				if released != 1 || reads[missing.SourceEventID] != 1 || reads[valid.SourceEventID] != 1 || len(alert.Shield.Bindings) != 2 {
					t.Fatal("incorrect release, repeated event reads or mutated input", released, reads)
				}
			})
		}
	}
}

func TestDependencyShieldMissingSourceEvent(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
			p := dependencyProcessor(t, repo, clock, s, state, action)
			admittedMainFixture(t, repo, clock, "main", s)
			opening := mustProcessAggregation(t, repo, p, dependencyEvent("child", "source", "child", clock))
			current, err := repo.GetAlert(t.Context(), "tenant", opening.Event.RelatedAlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			binding := current.Alert.Shield.Bindings[0]
			frozen, err := s.Loader.load(t.Context(), "tenant", store.PolicyReleaseRef{Kind: "shield", ID: binding.Policy.ID, Version: binding.Policy.Version, Digest: binding.Policy.Digest})
			if err != nil {
				t.Fatal(err)
			}
			s.Events = func(context.Context, string, string) (store.StoredEvent, error) {
				if missing {
					return store.StoredEvent{}, store.ErrNotFound
				}
				return store.StoredEvent{}, errors.New("event storage unavailable")
			}
			keep, reason, err := s.keepDependencyBinding(t.Context(), "tenant", current.Alert, binding, frozen, clock.at, nil)
			if err != nil || keep == missing || (missing && reason != "binding_event_missing") {
				t.Fatal("incorrect missing event handling", keep, reason, err)
			}
		})
	}
}
