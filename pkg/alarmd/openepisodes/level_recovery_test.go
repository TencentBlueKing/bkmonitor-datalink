// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openepisodes_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// levels is an envelope of series n deciding the given levels, in level
// order, as the trigger writes them: a level left out was not decided.
func levels(kind string, n int, results ...contract.LevelResultV1) contract.TriggerEventV1 {
	event := envelope(kind, n)
	event.LevelResults = results
	return event
}

func level(id uint32, result string) contract.LevelResultV1 {
	return contract.LevelResultV1{LevelID: id, Priority: id, Result: result}
}

// The link keeps one alert per fingerprint, at the most severe level it
// was raised to, and resolves it only on that level's RECOVERY; a RECOVERY
// of another level it answers as event_orphaned, active_alert_not_found and
// the alert stays. With two levels open, a RECOVERY of the level the alert
// was not at took the entry out of the record while the link
// still counted the alert. The record keeps the episode until the RECOVERY
// of the level the alert is at, whatever less severe level is still open.
func TestARecoveryOfOneLevelKeepsTheEpisodeWhileAnotherLevelIsOpen(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	client := server.Client()
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	w := newWorker(t, client, clk)
	const warning, info = 2, 3

	// The series opens at info, then warning too.
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 1, level(info, contract.LevelResultAbnormal)))
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 1,
		level(warning, contract.LevelResultAbnormal), level(info, contract.LevelResultAbnormal)))
	if record := load(t, client); len(record.Entries) != 1 || record.Entries[0].Fingerprint != fp(1) {
		t.Fatalf("the open series is not recorded: %+v", record)
	}

	// Info recovers; the alert is at warning, which is not decided this
	// round and stays open.
	clk.at = clk.at.Add(time.Minute)
	w.acknowledge(t, levels(contract.TriggerEventRecovery, 1, level(info, contract.LevelResultRecovery)))
	if record := load(t, client); len(record.Entries) != 1 || record.Entries[0].Fingerprint != fp(1) {
		t.Fatalf("a RECOVERY of info alone ended the episode while warning is open: %+v", record)
	}

	// Warning recovers: the link resolves the alert, and the episode ends.
	clk.at = clk.at.Add(time.Minute)
	w.acknowledge(t, levels(contract.TriggerEventRecovery, 1, level(warning, contract.LevelResultRecovery)))
	if record := load(t, client); len(record.Entries) != 0 {
		t.Fatalf("the RECOVERY of the alert's level left the episode recorded: %+v", record)
	}

	// Info opens again, then warning: the RECOVERY of warning ends the
	// episode with info still open, as the link resolves the alert.
	clk.at = clk.at.Add(time.Minute)
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 1, level(info, contract.LevelResultAbnormal)))
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 1, level(warning, contract.LevelResultAbnormal)))
	clk.at = clk.at.Add(time.Minute)
	w.acknowledge(t, levels(contract.TriggerEventRecovery, 1, level(warning, contract.LevelResultRecovery)))
	if record := load(t, client); len(record.Entries) != 0 {
		t.Fatalf("the RECOVERY of the alert's level left the episode recorded beside open info: %+v", record)
	}
}

// The record follows the alert the link keeps for a series: raised to a
// more severe Level and never lowered, resolved on that Level's RECOVERY.
// Each case is one series; the link's answer is the expectation.
func TestTheRecordFollowsTheLevelTheLinkKeepsTheAlertAt(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	client := server.Client()
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	w := newWorker(t, client, clk)
	const warning, info = 2, 3
	entry := func(n int) (openepisodes.Entry, bool) {
		for _, e := range load(t, client).Entries {
			if e.Fingerprint == fp(n) {
				return e, true
			}
		}
		return openepisodes.Entry{}, false
	}
	step := func() { clk.at = clk.at.Add(time.Minute) }

	// (a) Opened at info, raised to warning, warning recovers: the link
	// resolves the alert. The info ABNORMAL after it is a new alert there,
	// so a new episode here, opened then.
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 1, level(info, contract.LevelResultAbnormal)))
	step()
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 1, level(warning, contract.LevelResultAbnormal)))
	first, _ := entry(1)
	step()
	w.acknowledge(t, levels(contract.TriggerEventRecovery, 1, level(warning, contract.LevelResultRecovery)))
	if e, ok := entry(1); ok {
		t.Fatalf("(a) warning's RECOVERY left %+v recorded", e)
	}
	step()
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 1, level(info, contract.LevelResultAbnormal)))
	if again, ok := entry(1); !ok || again.OpenedAt <= first.OpenedAt {
		t.Fatalf("(a) the info ABNORMAL after the end is %+v (recorded %t), want a new episode after %d", again, ok, first.OpenedAt)
	}

	// (b) One envelope recovers warning and triggers info: the link ends
	// the warning alert and opens an info one under the same fingerprint;
	// the series stays recorded. The entry is written once a fingerprint
	// (HSETNX), so it keeps the time the warning alert opened.
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 2, level(warning, contract.LevelResultAbnormal)))
	step()
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 2,
		level(warning, contract.LevelResultRecovery), level(info, contract.LevelResultAbnormal)))
	if _, ok := entry(2); !ok {
		t.Fatal("(b) warning's RECOVERY beside info triggered took the series out of the record")
	}
	step()
	w.acknowledge(t, levels(contract.TriggerEventRecovery, 2, level(info, contract.LevelResultRecovery)))
	if e, ok := entry(2); ok {
		t.Fatalf("(b) info's RECOVERY, the alert's Level after the envelope, left %+v recorded", e)
	}

	// (c) The alert is at info and a warning RECOVERY arrives (the gate
	// holds it; acknowledged anyway, as after a restart it may be): the
	// link orphans it, and the series stays recorded.
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 3, level(info, contract.LevelResultAbnormal)))
	step()
	w.acknowledge(t, levels(contract.TriggerEventRecovery, 3, level(warning, contract.LevelResultRecovery)))
	if _, ok := entry(3); !ok {
		t.Fatal("(c) a RECOVERY of a Level the alert is not at took the series out of the record")
	}

	// (d) The alert is at warning and info triggers: nothing changes at
	// the link, nor here - the entry is the one opened at warning, and
	// warning's RECOVERY still ends it.
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 4, level(warning, contract.LevelResultAbnormal)))
	opened, _ := entry(4)
	step()
	w.acknowledge(t, levels(contract.TriggerEventAbnormal, 4, level(info, contract.LevelResultAbnormal)))
	if e, ok := entry(4); !ok || !reflect.DeepEqual(e, opened) {
		t.Fatalf("(d) info triggered under warning made the entry %+v (recorded %t), want %+v unchanged", e, ok, opened)
	}
	step()
	w.acknowledge(t, levels(contract.TriggerEventRecovery, 4, level(warning, contract.LevelResultRecovery)))
	if e, ok := entry(4); ok {
		t.Fatalf("(d) warning's RECOVERY beside info left %+v recorded", e)
	}
}
