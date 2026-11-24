// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

// Each word a job can end with has the class its meaning gives it: done,
// got somewhere, may clear by itself, or answered the same way by the same
// command. Every other outcome - item, record, pending and edit words,
// never a job's word - and a word not known are held, so no caller sends a
// job again at its own pace on a word nobody classified.
func TestEachJobWordSaysWhatTheCallerDoesNext(t *testing.T) {
	want := map[string]Next{
		OutcomeJobDone:            NextSettled,
		OutcomeNothingMatched:     NextSettled,
		OutcomeJobDoneRederived:   NextAgain,
		OutcomeJobIncomplete:      NextAgain,
		OutcomeLinkBusy:           NextTransient,
		OutcomeLinkFailed:         NextTransient,
		OutcomeRecordUnavailable:  NextTransient,
		OutcomeCommandUnavailable: NextTransient,
		OutcomeJobDoneRefused:     NextHeld,
		OutcomeLinkRefused:        NextHeld,
		OutcomeExpectExceeded:     NextHeld,
		OutcomeLinkAnswerTooLarge: NextHeld,
		OutcomeFilterRefused:      NextHeld,
		OutcomeTargetListTooLarge: NextHeld,
		OutcomeUnsupported:        NextHeld,
		"a_word_nobody_wrote":     NextHeld,
	}
	for _, word := range Outcomes {
		if _, named := want[word]; !named {
			want[word] = NextHeld
		}
	}
	for word, next := range want {
		if got := NextAfter(word); got != next {
			t.Errorf("%s: %s, want %s", word, got, next)
		}
	}
}

// A decision closed in two parts - the alerts since the record's start,
// checked against it, and the older ones alert by alert - answers the
// part its caller must wait on: a part the link refused, or a link that
// failed it, before a part that has more to close. Otherwise a caller
// that runs the decision again on progress (the inactive close, a step a
// second) sends the refused part again with it every time.
func TestADecisionInTwoPartsAnswersThePartToWaitOn(t *testing.T) {
	older := fakeRecordStart.Add(-24 * time.Hour)
	for _, tc := range []struct {
		name               string
		recent, olderFault string
		olderSearchBusy    bool
		noRecent           bool
		want               string
	}{
		{name: "recent found again, older refused", recent: "busy", olderFault: "invalid", want: OutcomeJobDoneRefused},
		{name: "recent refused, older found again", recent: "invalid", olderFault: "busy", want: OutcomeJobDoneRefused},
		{name: "recent found again, older's search busy", recent: "busy", olderSearchBusy: true, want: OutcomeLinkBusy},
		{name: "both closed", want: OutcomeJobDone},
		{name: "recent matched nothing, older closed", noRecent: true, want: OutcomeJobDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for i := 0; i < 5; i++ {
				h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}, created: older})
			}
			for i := 5; i < 10 && !tc.noRecent; i++ {
				h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			}
			h.opened(t, "41", "3", nil, fingerprints(5, 5)...)
			h.link.itemFault = func(id string, _ int) string {
				for i := 0; i < 5; i++ {
					if id == alertID(i) {
						return tc.olderFault
					}
				}
				return tc.recent
			}
			if tc.olderSearchBusy {
				h.link.requestBusy = func(route string, _ int) bool { return route == linkdmanage.RouteSearch }
			}
			closer := h.closer(t)
			if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != tc.want {
				t.Fatalf("word %s, want %s (stats %v)", word, tc.want, nonZero(closer.Stats()))
			}
		})
	}
}

// The batches of an alert-by-alert close combine the same way: a first
// batch whose items came back busy (found again) and a second the link
// refused answer the refusal, not the progress.
func TestAnAlertByAlertCloseAnswersTheBatchToWaitOn(t *testing.T) {
	const n = linkdmanage.MaxLimit + 1
	h := newHarness(t)
	for i := 0; i < n; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.link.itemFault = func(id string, _ int) string {
		if id == alertID(n-1) {
			return "invalid"
		}
		return "busy"
	}
	closer := h.closer(t)
	// No record: every alert decided one by one, closed by its id in
	// batches of linkdmanage.MaxLimit.
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDoneRefused {
		t.Fatalf("word %s, want job_done_refused (stats %v)", word, nonZero(closer.Stats()))
	}
}
