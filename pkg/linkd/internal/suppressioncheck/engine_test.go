// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package suppressioncheck

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type testWindows struct {
	window             redisstate.SuppressionWindow
	found              bool
	readErr, deleteErr error
	reads, deletes     int
	beforeDelete       func()
}

func (w *testWindows) ReadSuppressionWindow(ctx context.Context, _, _, _ string) (redisstate.SuppressionWindow, bool, error) {
	w.reads++
	if ctx.Err() != nil {
		return w.window, false, ctx.Err()
	}
	return w.window, w.found, w.readErr
}

func (w *testWindows) DeleteSuppressionWindow(ctx context.Context, expected redisstate.SuppressionWindow) (bool, error) {
	w.deletes++
	if w.beforeDelete != nil {
		w.beforeDelete()
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if w.deleteErr != nil {
		return false, w.deleteErr
	}
	if !w.found || w.window.Epoch != expected.Epoch || w.window.OwnerAlertID != expected.OwnerAlertID {
		return false, nil
	}
	w.found = false
	return true, nil
}

func fixture(t *testing.T, kind string) (Command, *testWindows, store.StoredAlert) {
	t.Helper()
	a := storetest.Alert("tenant", "owner", "opening", "fingerprint", "warning")
	now := time.Now()
	n := 1
	p := domain.PolicyVersion{ID: "p", Version: 1, Digest: strings.Repeat("f", 64)}
	w := redisstate.SuppressionWindow{TenantID: a.BKTenantID, Kind: kind, Policy: p, Epoch: "first", OwnerAlertID: a.AlertID, ObservedAtMillis: now.UnixMilli(), RetentionMillis: 60000, DurationSeconds: 60, MemberCount: 1}
	if kind == "clip" {
		subject := digest("identity", a.EventSourceID, a.Fingerprint)
		w.ID = subject + ":" + digest("clip", p.ID, "1", p.Digest, subject)
		w.SourceID = a.EventSourceID
		w.Fingerprint = a.Fingerprint
		w.Count = &n
		w.Threshold = 1
		w.State = "retained"
		w.LastEvaluatedAtMillis = now.UnixMilli()
	} else {
		w.GroupKey = strings.Repeat("a", 64)
		w.ID = digest("aggregation", p.ID, "1", p.Digest, w.GroupKey)
		w.OwnerEventID = w.Epoch
		w.OwnerSourceID = a.EventSourceID
		w.OwnerFingerprint = a.Fingerprint
		w.StartedAtMillis = now.UnixMilli()
		w.ExpiresAtMillis = w.StartedAtMillis + 60000
		w.State = "admitted"
		a.Admission = domain.AlertAdmission{AdmittedAt: &a.UpdateAt, Severity: a.Severity, CauseType: "source_event", CauseID: a.TriggerEventID}
	}
	if w.Validate() != nil || a.Validate() != nil {
		t.Fatal("invalid fixture", w.Validate(), a.Validate())
	}
	c := Command{TenantID: a.BKTenantID, Kind: kind, WindowID: w.ID, ExpectedEpoch: w.Epoch, ExpectedOwner: w.OwnerAlertID, OperationID: "manual", OperatorID: "operator", Reason: "核对终态残余"}
	return c, &testWindows{window: w, found: true}, store.StoredAlert{Alert: a, Version: store.NewVersionToken("1")}
}

func makeEngine(t *testing.T, w *testWindows, a store.StoredAlert, readErr error, inside func()) *Engine {
	t.Helper()
	locked := false
	e, err := NewEngine(w, func(_ context.Context, tenant, id string) (store.StoredAlert, error) {
		if !locked {
			t.Fatal("owner read without formal lease")
		}
		if tenant != a.Alert.BKTenantID || id != a.Alert.AlertID {
			t.Fatal("owner scope changed")
		}
		return a, readErr
	}, func(_ context.Context, tenant, source, fp string, run func() error) error {
		if tenant != a.Alert.BKTenantID || source != a.Alert.EventSourceID || fp != a.Alert.Fingerprint {
			t.Fatal("wrong owner lease")
		}
		locked = true
		defer func() { locked = false }()
		if inside != nil {
			inside()
		}
		return run()
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCheckRetainsLiveOwnersAndClearsOnlyVerifiedStaleWindow(t *testing.T) {
	for _, kind := range []string{"clip", "aggregation"} {
		for _, state := range []string{"active", "terminal", "missing", "unadmitted"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				c, w, a := fixture(t, kind)
				var readErr error
				switch state {
				case "terminal":
					a.Alert.Status = domain.AlertStatusRecovered
					a.Alert.EndAt = &a.Alert.UpdateAt
					a.Alert.EndType = domain.AlertEndTypeSource
				case "missing":
					readErr = store.ErrNotFound
				case "unadmitted":
					a.Alert.Admission = domain.AlertAdmission{}
				}
				e := makeEngine(t, w, a, readErr, nil)
				result, err := e.Check(t.Context(), c)
				removed := state == "terminal" || state == "missing" || (state == "unadmitted" && kind == "aggregation")
				if err != nil || result.Validate(c) != nil || result.Changed != removed || (w.deletes == 1) != removed {
					t.Fatal(result, err, w.deletes)
				}
				if !removed && result.Outcome != "retained" {
					t.Fatal(result)
				}
			})
		}
	}
}

func TestCheckRechecksAfterLeaseAndUsesConditionalDelete(t *testing.T) {
	for _, when := range []string{"before_lease", "before_delete"} {
		t.Run(when, func(t *testing.T) {
			c, w, a := fixture(t, "clip")
			a.Alert.Status = domain.AlertStatusClosed
			a.Alert.EndAt = &a.Alert.UpdateAt
			a.Alert.EndType = domain.AlertEndTypeUser
			change := func() { w.window.Epoch = "new-event"; w.window.OwnerAlertID = "new-owner" }
			var inside func()
			if when == "before_lease" {
				inside = change
			} else {
				w.beforeDelete = change
			}
			result, err := makeEngine(t, w, a, nil, inside).Check(t.Context(), c)
			if err != nil || result.Outcome != "superseded" || result.Changed || !w.found || result.Validate(c) != nil {
				t.Fatal(result, err)
			}
		})
	}
}

func TestCheckUnboundAndMissingDoNotTakeOwnerLease(t *testing.T) {
	for _, missing := range []bool{false, true} {
		c, w, a := fixture(t, "clip")
		w.found = !missing
		w.window.OwnerAlertID = ""
		c.ExpectedOwner = ""
		e := makeEngine(t, w, a, nil, func() { t.Fatal("unbound counter took owner lease") })
		result, err := e.Check(t.Context(), c)
		if err != nil || result.Validate(c) != nil || w.deletes != 0 {
			t.Fatal(result, err)
		}
		if missing && result.Outcome != "absent" {
			t.Fatal(result)
		}
	}
}

func TestCheckFailuresDoNotClaimNoEffectsOrSwallowLeaseError(t *testing.T) {
	for _, failure := range []string{"window", "owner", "mixed_missing", "scope", "delete", "release", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			c, w, a := fixture(t, "aggregation")
			a.Alert.Status = domain.AlertStatusClosed
			a.Alert.EndAt = &a.Alert.UpdateAt
			a.Alert.EndType = domain.AlertEndTypeUser
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			down := errors.New("private dependency failure")
			var readErr error
			if failure == "window" {
				w.readErr = down
			}
			if failure == "owner" {
				readErr = down
			}
			if failure == "mixed_missing" {
				readErr = errors.Join(store.ErrNotFound, down)
			}
			if failure == "scope" {
				w.window.TenantID = "other"
			}
			if failure == "delete" {
				w.deleteErr = down
			}
			if failure == "cancel" {
				w.beforeDelete = cancel
			}
			e := makeEngine(t, w, a, readErr, nil)
			if failure == "release" {
				old := e.lease
				e.lease = func(ctx context.Context, tenant, source, fp string, run func() error) error {
					return errors.Join(old(ctx, tenant, source, fp, run), down)
				}
			}
			result, err := e.Check(ctx, c)
			if err == nil || result.Validate(c) != nil || result.Outcome != "failed" {
				t.Fatal(result, err)
			}
			if (failure == "release") != result.Changed {
				t.Fatal("confirmed effect lost or fabricated", result)
			}
		})
	}
	if CanDefer(errors.Join(scheduler.ErrLockBusy, errors.New("release failed"))) || !CanDefer(scheduler.ErrLockBusy) || !CanDefer(policy.ErrPreviewCapacity) {
		t.Fatal("deferred lease failure")
	}
}

func TestCheckPreservesLiveCandidateReservation(t *testing.T) {
	c, w, a := fixture(t, "aggregation")
	w.window.State = "pending"
	w.window.PendingUntilMillis = w.window.ObservedAtMillis
	engine := makeEngine(t, w, a, store.ErrNotFound, func() { t.Fatal("live candidate should not be checked as missing owner") })
	result, err := engine.Check(t.Context(), c)
	if err != nil || result.Validate(c) != nil || result.Reason != "candidate_pending" || w.deletes != 0 {
		t.Fatal(result, err)
	}
	w.window.ObservedAtMillis++
	result, err = makeEngine(t, w, a, store.ErrNotFound, nil).Check(t.Context(), c)
	if err != nil || result.Outcome != "cleared" || result.Reason != "owner_missing" || !result.Changed {
		t.Fatal(result, err)
	}
}
