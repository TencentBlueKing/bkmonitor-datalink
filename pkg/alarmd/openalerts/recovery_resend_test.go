// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// resendFixture is a copy with the production cadences: the set read every
// minute, the local retention a minute, a calibration every half hour. The
// consumer's set is members, and a calibration reports the same, unless
// calibrationFails.
type resendFixture struct {
	c                *clock
	cache            *Cache
	members          []string
	calibrationFails bool
}

func newResendFixture(t *testing.T, members ...string) *resendFixture {
	t.Helper()
	f := &resendFixture{c: &clock{at: time.Unix(1700000000, 0)}, members: members}
	options := indexOptions(f.c)
	options.RefreshInterval, options.IndexInterval, options.LocalRetention = time.Second, time.Minute, time.Minute
	options.ReconcileInterval, options.CalibrationMaxAge = 30*time.Minute, time.Hour
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return f.members, nil })
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		if f.calibrationFails {
			return Reconciliation{}, ErrIncomplete
		}
		return Reconciliation{Members: append([]string(nil), f.members...)}, nil
	})
	f.cache = mustIndex(t, options)
	if err := f.cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	f.cache.Refresh(context.Background())
	if !f.cache.Snapshot(keyA).Calibrated {
		t.Fatal("fixture: the set must start calibrated, the state in which a recovery waited for the next calibration")
	}
	return f
}

// read moves the clock and has the next round read the set, as the
// per-minute read would; no calibration is asked for.
func (f *resendFixture) read(after time.Duration) {
	f.c.advance(after)
	f.cache.indexChanged(keyA)
	f.cache.Refresh(context.Background())
}

// calibrate moves the clock and has the next round calibrate keyA too.
func (f *resendFixture) calibrate(after time.Duration) {
	f.c.advance(after)
	f.cache.indexChanged(keyA)
	f.cache.RequestReconcile(keyA)
	f.cache.Refresh(context.Background())
}

func (f *resendFixture) acknowledge(events ...contract.TriggerEventV1) {
	f.cache.Acknowledged(events)
}

func (f *resendFixture) open(fingerprint string) bool {
	return f.cache.Contains(tenant, keyA.StrategyID, fingerprint)
}

// A recovery the consumer took and did not process leaves the alert in its
// set. Once the local retention has passed, the first read of the set that
// still carries it lets the recovery through again - it used to wait for the
// next calibration, half an hour by default. Within the retention the set has
// not had time to reflect the recovery and nothing is sent; a set that no
// longer carries it has processed the recovery and nothing is sent.
func TestARecoveryIsSentAgainOnTheFirstReadPastTheRetentionThatStillCarriesItsAlert(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		after      time.Duration
		stillInSet bool
		open       bool
	}{
		{name: "within the retention, still in the set", after: 30 * time.Second, stillInSet: true, open: false},
		{name: "past the retention, still in the set", after: time.Minute + time.Second, stillInSet: true, open: true},
		{name: "past the retention, gone from the set", after: time.Minute + time.Second, stillInSet: false, open: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			f := newResendFixture(t, "fp")
			f.acknowledge(abnormal(keyA, "fp"))
			f.c.advance(time.Second)
			f.acknowledge(recovery(keyA, "fp"))
			if !testCase.stillInSet {
				f.members = nil
			}
			f.read(testCase.after)
			if got := f.open("fp"); got != testCase.open {
				t.Fatalf("open = %t, want %t", got, testCase.open)
			}
		})
	}
}

// A recovery sent again waits for a calibration that began after it, however
// many reads still carry its alert: a consumer behind by more than the
// retention holds both copies queued, and each further read would add one
// more. The calibration is the path that backs it. The resend is counted,
// and the departures - counted when the first recovery was taken - are not
// counted again.
func TestARecoverySentAgainWaitsForTheNextCalibration(t *testing.T) {
	f := newResendFixture(t, "fp")
	f.acknowledge(abnormal(keyA, "fp"))
	f.c.advance(time.Second)
	f.acknowledge(recovery(keyA, "fp"))
	f.read(time.Minute + time.Second)
	if !f.open("fp") {
		t.Fatal("fixture: the first read past the retention must let the recovery through")
	}
	departures := f.cache.Stats()
	f.acknowledge(recovery(keyA, "fp"))
	stats := f.cache.Stats()
	if stats.RecoveriesResent != 1 {
		t.Fatalf("recoveries resent = %d, want 1", stats.RecoveriesResent)
	}
	if stats.SentDepartures[DepartureRecoveryAcked] != departures.SentDepartures[DepartureRecoveryAcked] ||
		stats.OwnOpenDepartures[DepartureRecoveryAcked] != departures.OwnOpenDepartures[DepartureRecoveryAcked] ||
		departures.SentDepartures[DepartureRecoveryAcked] != 1 {
		t.Fatalf("departures %v / %v, before the resend %v / %v: the resend was counted as a recovery of its own",
			stats.SentDepartures, stats.OwnOpenDepartures, departures.SentDepartures, departures.OwnOpenDepartures)
	}
	for read := 1; read <= 5; read++ {
		f.read(time.Minute + time.Second)
		if f.open("fp") {
			t.Fatalf("read %d after the resend let it through again without a calibration", read)
		}
	}
	f.calibrate(time.Second)
	if !f.open("fp") {
		t.Fatal("a calibration that began after the resend still carries the alert and the recovery was held")
	}
}

// With no current calibration to wait for, a recovery sent again waits as
// long as one would take, then the next read.
func TestARecoverySentAgainWithoutACalibrationWaitsAsLongAsOneWouldTake(t *testing.T) {
	f := newResendFixture(t, "fp")
	f.calibrationFails = true
	f.calibrate(time.Hour + time.Second)
	if f.cache.Snapshot(keyA).Calibrated {
		t.Fatal("fixture: the calibration must have lapsed")
	}
	f.acknowledge(recovery(keyA, "fp"))
	f.read(time.Minute + time.Second)
	if !f.open("fp") {
		t.Fatal("fixture: the first read past the retention must let the recovery through")
	}
	f.acknowledge(recovery(keyA, "fp"))
	f.read(30*time.Minute - time.Second)
	if f.open("fp") {
		t.Fatal("a resent recovery went out again inside the calibration interval with no calibration")
	}
	f.read(2 * time.Second)
	if !f.open("fp") {
		t.Fatal("a resent recovery was held past the calibration interval with no calibration to wait for")
	}
}

// While the sets carry none of the alerts this process opened, what they say
// about an alert is not trusted, and a recovery keeps waiting for the next
// calibration as it did before resends.
func TestADisjointCopyDoesNotSendARecoveryAgainOnARead(t *testing.T) {
	f := newResendFixture(t, "fp")
	f.acknowledge(abnormal(keyA, "ours-0"))
	f.c.advance(time.Second)
	f.acknowledge(recovery(keyA, "fp"))
	f.read(SentConfirmAfter + time.Second)
	if !f.cache.Stats().Disjoint {
		t.Fatal("fixture: the copy must be disjoint, the one alert this process opened missing from the set")
	}
	if f.open("fp") {
		t.Fatal("a disjoint copy sent a recovery again on a read")
	}
}

// A calibration past the retention, with no read of the set since the
// recovery, still carrying the alert, lets the recovery through again just
// the same: it prunes the ledger entry.
func TestACalibrationAloneCanBeTheReadThatSendsARecoveryAgain(t *testing.T) {
	f := newResendFixture(t, "fp")
	f.cache.index.options.IndexInterval = time.Hour
	readBefore := f.cache.Snapshot(keyA).IndexReadAt
	f.c.advance(time.Second)
	f.acknowledge(recovery(keyA, "fp"))
	f.c.advance(time.Minute + time.Second)
	f.cache.RequestReconcile(keyA)
	f.cache.Refresh(context.Background())
	if snapshot := f.cache.Snapshot(keyA); !snapshot.IndexReadAt.Equal(readBefore) || !snapshot.Calibrated {
		t.Fatalf("fixture: want a calibration and no read of the set since the recovery, got %+v", snapshot)
	}
	if !f.open("fp") {
		t.Fatal("a calibration past the retention still carrying the alert did not let the recovery through")
	}
}

// Only a read taken once the retention has passed counts. A read from inside
// it may predate the consumer processing the recovery, so the alert it
// still carried says nothing; the next read, past the retention, decides.
func TestAReadFromInsideTheRetentionDoesNotSendARecoveryAgain(t *testing.T) {
	f := newResendFixture(t, "fp")
	f.acknowledge(abnormal(keyA, "fp"))
	f.c.advance(time.Second)
	f.acknowledge(recovery(keyA, "fp"))
	f.read(30 * time.Second)
	f.c.advance(31 * time.Second)
	if f.open("fp") {
		t.Fatal("a read from inside the retention sent the recovery again")
	}
	f.read(time.Second)
	if !f.open("fp") {
		t.Fatal("the first read past the retention still carrying the alert did not send the recovery again")
	}
}
