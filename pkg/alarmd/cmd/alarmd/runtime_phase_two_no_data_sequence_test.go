// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// What the sink takes, round by round, for the no-data trigger's sequences,
// through the production bundle on the native protocol: the strategy compiled
// from its document, the open-alert gate wired as in production, real Redis.
//
// The expectations are written out per round from the definitions, not read
// from a run. The alert is raised by the continuous-th consecutive absent
// point, the points one period apart; a round whose query was not FULL
// contributes no point and bridges nothing (P9). One round with data closes
// an open alert (P11, one recovery window), and only an open one (the
// open-alert gate). The fixture has no link Console, so the gate answers
// from what this process sent, and a RECOVERY it sent hides nothing: through
// the grace (the calibration interval, 30 minutes against these one-minute
// rounds) the alert still answers open, so every round that decides
// RECOVERY sends it again, an orphan at the consumer, until an ABNORMAL
// clears the grace: at most grace / period of them, 30 here, before the
// record lets the alert go. A new absence after a close starts from nothing. A
// round not FULL is neither an absent point nor a present one: it neither
// raises nor closes, so an open alert stays open across it until data
// actually returns.
func TestNoDataSequencesSendWhatTheTriggerDefines(t *testing.T) {
	const a, r = contract.TriggerEventAbnormal, contract.TriggerEventRecovery
	for _, test := range []struct {
		name       string
		continuous int
		rounds     string // A absent, P present, U query not FULL
		want       []string
	}{
		{"continuous 1 raises on the first absent round", 1, "A", []string{a}},
		{"continuous 2: one short, then raised", 2, "AA", []string{"", a}},
		{"continuous 3 from a clean start: two short, then raised", 3, "AAA", []string{"", "", a}},
		{"a round not FULL after two absent ones restarts the count", 3, "AAUAAA", []string{"", "", "", "", "", a}},
		{"a round with data between absent ones restarts the count and closes nothing", 3, "APAAA", []string{"", "", "", "", a}},
		// The two absent rounds after the close are short of the trigger and
		// decide RECOVERY; within the grace each goes out again.
		{"data closes the alert, the close repeats through the grace, and a new absence starts from nothing", 3, "AAAPAAA", []string{"", "", a, r, r, r, a}},
		{"a round not FULL does not close an open alert, and data after it does", 3, "AAAUP", []string{"", "", a, "", r}},
		// The window after the hole holds two absent points of three, short
		// of the trigger; the hole is not a miss, so it closes nothing.
		{"an absent round after a round not FULL does not close the open alert", 3, "AAAUA", []string{"", "", a, "", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startNoDataFixtureWith(t, config.OutputProtocolNative, test.continuous)
			ctx := context.Background()
			seen := 0
			for index, round := range test.rounds {
				fixture.hasData.Store(round == 'P')
				fixture.partial.Store(round == 'U')
				// runSlot(n) puts the clock half a period past base+n*P, so
				// the Slot evaluated at base+n*P is the one due: n from 0
				// runs one Slot per round.
				fixture.runSlot(ctx, int64(index))
				var kinds []string
				recorded := fixture.events.recorded()
				for _, event := range recorded[seen:] {
					if noDataTagged(event) {
						kinds = append(kinds, event.EventKind)
					}
				}
				seen = len(recorded)
				want := test.want[index]
				if (want == "" && len(kinds) != 0) || (want != "" && (len(kinds) != 1 || kinds[0] != want)) {
					t.Errorf("round %d (%c) sent %v, want %q", index+1, round, kinds, want)
				}
			}
		})
	}
}

// Through the production bundle on the compatible protocol, the whole-item
// no-data alert carries the object the backend writes for the same silence.
// The fixture's item aggregates by its own dimensions and its no-data
// agg_dimension is empty, so every group reduces to the tag alone
// (nodata.py:83-84, :106-107); the anomaly_id starts with count_md5 of
// {"__NO_DATA_DIMENSION__": True}, computed with the backend's own count_md5
// (common_utils.py:452-479), not with this module's hash.
func TestTheCompatibleNoDataIdentityIsTheBackendsWholeItemObject(t *testing.T) {
	fixture := startNoDataFixtureOn(t, config.OutputProtocolLegacy)
	ctx := context.Background()
	var anomalies []contract.TriggerEventV1
	for round := int64(0); round <= noDataContinuous+2 && len(anomalies) == 0; round++ {
		fixture.runSlot(ctx, round)
		anomalies = fixture.noDataAnomalies()
	}
	if len(anomalies) == 0 {
		t.Fatalf("no no-data anomaly after %d absent rounds", noDataContinuous+3)
	}
	id, _ := noDataAnomalyID(t, anomalies[0])
	const want = "3e06a0b6d0560271cafee9f08a6da2d7.1001."
	if !strings.HasPrefix(id, want) {
		t.Fatalf("anomaly_id without its period = %q, want it to start %q: the backend's recovery looks the "+
			"group up by this md5, so any other value opens a new alert every round", id, want)
	}
}
