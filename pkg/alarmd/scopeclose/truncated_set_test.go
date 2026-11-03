// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scopeclose

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// cutLink is the link as the copy reads it: strategy "42" answers its
// calibration cut, listing one alert of this deployment's out of the two its
// set holds; strategy "43" answers no calibration and no read at all.
type cutLink struct{}

var (
	cutKey     = openalerts.StrategyKey{TenantID: tenant, StrategyID: "42"}
	silentKey  = openalerts.StrategyKey{TenantID: tenant, StrategyID: "43"}
	errNoReply = errors.New("no answer")
)

func (cutLink) ReadSet(_ context.Context, k openalerts.StrategyKey) ([]string, error) {
	if k != cutKey {
		return nil, errNoReply
	}
	return []string{fp(1), fp(2)}, nil
}

func (cutLink) Reconcile(_ context.Context, k openalerts.StrategyKey) (openalerts.Reconciliation, error) {
	if k != cutKey {
		return openalerts.Reconciliation{}, errNoReply
	}
	return openalerts.Reconciliation{EventSourceID: ownSrc, Truncated: true, Members: []string{fp(1)},
		Alerts: []openalerts.Alert{{AlertID: "alert-listed", EventSourceID: ownSrc, Fingerprint: fp(1)}}}, nil
}

func (cutLink) Watch(ctx context.Context, ready func(bool), _ func(openalerts.StrategyKey), _ func(openalerts.NoticeRefusal)) error {
	ready(true)
	<-ctx.Done()
	return ctx.Err()
}

type confirmedConsole struct{}

func (confirmedConsole) LocationConfirmed() bool { return true }
func (confirmedConsole) KeyedByAlertID() (bool, time.Time, bool) {
	return true, time.Unix(1700000000, 0), true
}
func (confirmedConsole) KeyingAsked() bool       { return true }
func (confirmedConsole) Refresh(context.Context) {}

// A strategy whose only fresh calibration is cut still has its listed
// alerts closed: the screen clears it, the listed alert closes after two
// Slots, and the alert its set holds that the cut answer did not list is
// never judged - set_unavailable, not not_member, and never closed. A
// strategy with no answer at all waits on its own, set_unavailable, and
// holds up nothing of the other's.
func TestACutCalibrationClosesWhatItListsAndWaitsOnTheRest(t *testing.T) {
	cache, err := openalerts.NewIndex(openalerts.IndexOptions{Source: cutLink{}, Reconciler: cutLink{}, Subscriber: cutLink{}, Facts: confirmedConsole{},
		MaxStrategies: 10, MaxMembers: 100, MaxBytes: 1 << 20, MaxLocalEntries: 100, ReadBatch: 2, ReconcileBatch: 2,
		RefreshInterval: time.Hour, IndexInterval: time.Hour, ReconcileInterval: time.Hour, CalibrationMaxAge: 2 * time.Hour,
		LocalRetention: time.Minute, CycleTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.TrackOwned(cutKey, silentKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = cache.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	testwait.Until(t, "the cut calibration applied with the subscription ready", func() bool {
		_, judged := cache.Holds(cutKey, fp(1))
		return judged && cache.OwnEventSourceID() == ownSrc
	})

	writer := &recordingWriter{}
	closer := New(Options{MaxEntries: 16, Batch: 8})
	closer.Bind(CacheSet(cache), writer)
	slot := func(round int64) {
		if screen := closer.Screen(cutKey); screen != "" {
			t.Fatalf("Screen(cut strategy) = %q, want cleared", screen)
		}
		if screen := closer.Screen(silentKey); screen != OutcomeSetUnavailable {
			t.Fatalf("Screen(unanswered strategy) = %q, want set_unavailable", screen)
		}
		closer.Count(silentKey, "group", round, OutcomeSetUnavailable, 1)
		for _, fingerprint := range []string{fp(1), fp(2)} {
			closer.Observe(Drop{TenantID: tenant, BusinessID: business, StrategyID: cutKey.StrategyID, Fingerprint: fingerprint,
				StrategyRevision: 7, Round: round})
		}
		closer.Step(context.Background())
	}
	slot(1700000000)
	slot(1700000060)
	sent := writer.sent()
	if len(sent) != 1 || sent[0].Fingerprint != fp(1) || sent[0].AlertInstanceID != "alert-listed" {
		t.Fatalf("sent %+v, want the one close of the listed alert", sent)
	}
	stats := closer.Stats()
	if stats[OutcomeNotMember] != 0 {
		t.Fatalf("not_member = %d; an alert a cut answer did not list was judged not held", stats[OutcomeNotMember])
	}
	// Two Slots: the unlisted alert unjudged in each, the unanswered
	// strategy's bulk count in each.
	if stats[OutcomeSetUnavailable] != 4 || stats[OutcomeCloseSent] != 1 {
		t.Fatalf("outcomes %v, want set_unavailable 4 and close_sent 1", stats)
	}
}
