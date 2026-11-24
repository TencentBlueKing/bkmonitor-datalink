// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type failedCompletionRepository struct {
	store.Repository
	fail, afterCommit bool
}

func (r *failedCompletionRepository) CompleteAlertChange(ctx context.Context, current store.StoredAlert, field store.AlertChangeField) (store.StoredAlert, error) {
	if r.fail && !r.afterCommit {
		r.fail = false
		return store.StoredAlert{}, errors.New("completion unavailable")
	}
	next, err := store.CompletedAlertChange(current, field)
	if err != nil {
		return store.StoredAlert{}, err
	}
	updated, err := r.CompareAndSetAlert(ctx, current.Alert.BKTenantID, current.Alert.AlertID, current.Version, next)
	if err == nil && r.fail {
		r.fail = false
		return store.StoredAlert{}, errors.New("completion response lost")
	}
	return updated, err
}

func TestMergeCompletionFailureRetriesStableOutput(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before commit", true: "lost response"}[after], func(t *testing.T) {
			base := memory.New()
			r := &failedCompletionRepository{Repository: base, fail: true, afterCommit: after}
			a := releaseAlert()
			if _, err := base.CreateAlert(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			hook := &recordingHook{}
			p := newTestProcessor(t, r, NoopFinalHook{})
			p.finalHooks = []NamedFinalHook{{Name: "action", Purpose: "action", Hook: hook}}
			if _, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID); err == nil {
				t.Fatal("completion failure hidden")
			}
			pending, err := base.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			if (pending.Alert.MergeChange == nil) != after {
				t.Fatal("incorrect durable intent")
			}
			if _, err := p.FinishMergeChanges(t.Context(), a.BKTenantID, a.AlertID); err != nil {
				t.Fatal(err)
			}
			want := 2
			if after {
				want = 1
			}
			if len(hook.inputs) != want {
				t.Fatalf("outputs=%d want=%d", len(hook.inputs), want)
			}
			if !after && (hook.inputs[0].Cause != hook.inputs[1].Cause || !reflect.DeepEqual(hook.inputs[0].Alert, hook.inputs[1].Alert)) {
				t.Fatal("retry changed output identity or snapshot")
			}
			final, err := base.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil || final.Alert.MergeChange != nil || final.Alert.Revision != pending.Alert.Revision {
				t.Fatal("confirmation changed business revision", err)
			}
		})
	}
}
