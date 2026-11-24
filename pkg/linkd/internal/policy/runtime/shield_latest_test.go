// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store/memory"
)

type shieldLatestOnlyReader struct{ calls int }

func (r *shieldLatestOnlyReader) GetRelease(context.Context, policy.Scope, string, int64) (policy.Release, error) {
	r.calls++
	return policy.Release{}, errors.New("historical releases are unavailable")
}

func nextShieldRelease(t *testing.T, current policy.Release, edit func(*policy.ShieldSpec)) policy.Release {
	t.Helper()
	var spec policy.ShieldSpec
	if err := json.Unmarshal(current.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	edit(&spec)
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	current.Version++
	current.Spec, current.Compiled = compiled.Canonical, compiled.Summary
	return current
}

func TestShieldRecheckUsesLatestPublishedPolicy(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "automatic"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			for _, change := range []string{"unchanged", "shortened", "extended", "condition", "target", "type", "disabled", "deleted", "unavailable"} {
				t.Run(change, func(t *testing.T) {
					repo := memory.New()
					clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 1, 0, time.UTC)}
					old := timeShieldRelease(t)
					directory := &shieldDirectory{}
					if !manual {
						directory.releases = []policy.Release{old}
					}
					p, s := newShieldProcessor(t, repo, clock, directory, &shieldHook{}, &shieldHook{})
					opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
					id := opening.Event.RelatedAlertIDs[0]
					current, err := repo.GetAlert(t.Context(), "tenant", id)
					if err != nil {
						t.Fatal(err)
					}
					if manual {
						directory.releases = []policy.Release{old}
						_, err := p.BindShield(t.Context(), lifecycle.ShieldCommand{TenantID: "tenant", AlertID: id, OperationID: "manual", OperatorID: "admin", ExpectedRevision: current.Alert.Revision, Policy: domain.PolicyVersion{ID: old.ID, Version: old.Version, Digest: old.Compiled.Digest}, EffectiveAt: clock.at})
						if err != nil {
							t.Fatal(err)
						}
						current, err = repo.GetAlert(t.Context(), "tenant", id)
						if err != nil {
							t.Fatal(err)
						}
						s.Events = nil // 指定告警的手动绑定不重新执行目标和条件匹配。
					}
					binding := current.Alert.Shield.Bindings[0]
					want := true
					at := clock.at.Add(6 * time.Second)
					next := nextShieldRelease(t, old, func(spec *policy.ShieldSpec) {
						switch change {
						case "shortened":
							spec.Times[0].CloseOnce = "2026-09-30T00:00:05Z"
							want = false
						case "extended":
							spec.Times[0].CloseOnce = "2026-09-30T00:00:30Z"
							at = clock.at.Add(10 * time.Second)
						case "condition":
							spec.Policy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"another"}}`)
							want = manual
						case "target":
							spec.Targets.Selectors[0].Instances[0].InstanceID = "102"
							spec.Targets.Selectors[0].Instances[0].EntityUID = "cw-Host|102"
							want = manual
						case "type":
							spec.ShieldType, spec.ShieldMode = "rely_shield", "custom_shield"
							spec.Times = nil
							spec.RelyPolicy = spec.Policy
							want = false
						case "disabled":
							spec.Enabled = false
							want = false
						}
					})
					// 只保留 V2，并清除目录中的 V1，证明复查不需要历史配置。
					directory.releases = []policy.Release{next}
					if change == "deleted" {
						directory.releases = nil
						want = false
					}
					if change == "unavailable" {
						directory.err = errors.New("directory unavailable")
					}
					s.Loader.Catalog = policy.NewCatalog(directory)
					reader := &shieldLatestOnlyReader{}
					s.Loader.Releases = reader
					result, err := s.Recheck(t.Context(), current.Alert, at, nil)
					if change == "unavailable" && !manual {
						if !errors.Is(err, policy.ErrUnavailable) || reader.calls != 0 {
							t.Fatalf("directory failure was not propagated: %v, historical_reads=%d", err, reader.calls)
						}
						return
					}
					if err != nil || result.Shield.Active != want {
						t.Fatalf("active=%v want=%v error=%v steps=%+v", result.Shield.Active, want, err, result.Decision)
					}
					if reader.calls != 0 {
						t.Fatalf("recheck read historical policy %d times", reader.calls)
					}
					if change != "deleted" && change != "unavailable" && result.Decision.Steps[0].Policy.Version != next.Version {
						t.Fatal("diagnostic did not record evaluated version", result.Decision.Steps)
					}
					if want && (len(result.Shield.Bindings) != 1 || result.Shield.Bindings[0].BindingID != binding.BindingID || result.Shield.Bindings[0].Policy != binding.Policy) {
						t.Fatal("recheck rewrote binding history", result.Shield)
					}
				})
			}
		})
	}
}

func TestDependencyShieldRecheckUsesLatestConditions(t *testing.T) {
	for _, change := range []string{"unchanged", "main", "child", "target"} {
		t.Run(change, func(t *testing.T) {
			repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
			p := dependencyProcessor(t, repo, clock, s, state, action)
			admittedMainFixture(t, repo, clock, "main", s)
			opening := mustProcessAggregation(t, repo, p, dependencyEvent("child", "source", "child", clock))
			current, err := repo.GetAlert(t.Context(), "tenant", opening.Event.RelatedAlertIDs[0])
			if err != nil || !current.Alert.Shield.Active {
				t.Fatal("dependency binding missing", err)
			}
			next := nextShieldRelease(t, dependencyRelease(t, "custom_shield"), func(spec *policy.ShieldSpec) {
				switch change {
				case "main":
					spec.Policy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"another"}}`)
				case "child":
					spec.RelyPolicy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"another"}}`)
				case "target":
					spec.Targets.Selectors[0].Instances[0].InstanceID = "102"
					spec.Targets.Selectors[0].Instances[0].EntityUID = "cw-Host|102"
				}
			})
			s.Loader.Catalog = policy.NewCatalog(&shieldDirectory{releases: []policy.Release{next}})
			reader := &shieldLatestOnlyReader{}
			s.Loader.Releases = reader
			result, err := s.Recheck(t.Context(), current.Alert, clock.at.Add(time.Second), nil)
			if err != nil || result.Shield.Active != (change == "unchanged") || reader.calls != 0 {
				t.Fatalf("shield=%+v historical_reads=%d error=%v", result.Shield, reader.calls, err)
			}
		})
	}
}
