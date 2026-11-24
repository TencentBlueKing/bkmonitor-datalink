// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package lifecycle

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store/memory"
)

type activeObserver struct {
	alerts []domain.Alert
	err    error
}

func (o *activeObserver) ObserveAlert(_ context.Context, a domain.Alert) error {
	o.alerts = append(o.alerts, a)
	return o.err
}

func TestActiveCacheOnlyObservesMembershipChanges(t *testing.T) {
	for _, failure := range []bool{false, true} {
		repo := memory.New()
		p := newTestProcessor(t, repo, &recordingHook{})
		observer := &activeObserver{}
		if failure {
			observer.err = errors.New("redis offline")
		}
		p.activeAlerts = observer
		first := testEvent("opening", "warning")
		persistAndProcess(t, repo, p, first)
		persistAndProcess(t, repo, p, testEvent("repeat", "warning"))
		if len(observer.alerts) != 1 {
			t.Fatal("continuous firing wrote cache")
		}
		persistAndProcess(t, repo, p, testEvent("upgrade", "critical"))
		if len(observer.alerts) != 2 || observer.alerts[1].Status != domain.AlertStatusActive {
			t.Fatal("rotation deleted membership")
		}
		resolved := testEvent("resolved", "critical")
		resolved.Evaluations[0].Action = domain.EventActionResolved
		persistAndProcess(t, repo, p, resolved)
		if len(observer.alerts) != 3 || observer.alerts[2].Status != domain.AlertStatusRecovered {
			t.Fatal("recovery not observed")
		}
	}
}
