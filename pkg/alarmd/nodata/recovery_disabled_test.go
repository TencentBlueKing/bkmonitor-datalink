// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"testing"
)

// With the item's recovery disabled, a group whose data comes back gets no
// verdict - no NORMAL, so its no-data alert is not ended - and is remembered
// as seen, its absence over; with recovery enabled the same round is its
// NORMAL. It is not taken for absent (no anomaly) nor for dropped (no close),
// whether the roster expects it or not, and a later absence starts afresh.
func TestAGroupBackWithRecoveryDisabledGetsNoVerdictAndIsSeen(t *testing.T) {
	expected, unexpected := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.9")
	open := map[string]GroupMemory{
		expected.Key():   {FirstAbsent: absenceRound1, LastSeen: absenceRound1 - absencePeriod},
		unexpected.Key(): {FirstAbsent: absenceRound1, LastSeen: absenceRound1 - absencePeriod},
	}
	roster := staticRoster("v1", expected)
	for _, disabled := range []bool{false, true} {
		input := fullRound(absenceRound2, roster, groupSet(expected, unexpected), open)
		input.RecoveryDisabled = disabled
		result := evaluate(t, input)
		for _, group := range []Group{expected, unexpected} {
			verdict, judged := result.Verdicts[group.Key()]
			if disabled && judged || !disabled && verdict != VerdictNormal {
				t.Fatalf("disabled %v: %s got %q (judged %v), want %s", disabled, group.Key(), verdict, judged,
					map[bool]string{true: "no verdict", false: "NORMAL"}[disabled])
			}
			if entry := result.Memory[group.Key()]; entry.FirstAbsent != 0 || entry.LastSeen != absenceRound2 {
				t.Fatalf("disabled %v: %s remembered %+v, want seen this round with no absence", disabled, group.Key(), entry)
			}
			if result.Closing[group.Key()] {
				t.Fatalf("disabled %v: %s was closed as dropped while it reported", disabled, group.Key())
			}
		}
		if result.Facts.Absent != 0 {
			t.Fatalf("disabled %v: %d groups read absent, want none", disabled, result.Facts.Absent)
		}
	}
}

// The closes that are no recovery keep their NORMAL with recovery disabled:
// a group the roster stopped expecting with its absence open is closed once
// (A8), and a group now in another business is recovered and forgotten (A6).
// They end an alert because the group left what this item watches, not
// because its data came back.
func TestTheClosesThatAreNoRecoveryStayWithRecoveryDisabled(t *testing.T) {
	kept, dropped, moved := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.2"), hostGroup(t, "192.0.2.3")
	open := map[string]GroupMemory{
		dropped.Key(): {FirstAbsent: absenceRound1},
		moved.Key():   {FirstAbsent: absenceRound1},
	}
	input := fullRound(absenceRound2, staticRoster("v2", kept, moved), groupSet(kept), open)
	input.OutOfBusiness = map[string]struct{}{moved.Key(): {}}
	input.RecoveryDisabled = true
	result := evaluate(t, input)
	if result.Verdicts[dropped.Key()] != VerdictNormal || !result.Closing[dropped.Key()] {
		t.Fatalf("the dropped group got %q closing %v, want its one closing NORMAL", result.Verdicts[dropped.Key()], result.Closing[dropped.Key()])
	}
	if result.Verdicts[moved.Key()] != VerdictNormal {
		t.Fatalf("the group in another business got %q, want NORMAL", result.Verdicts[moved.Key()])
	}
	if _, judged := result.Verdicts[kept.Key()]; judged {
		t.Fatalf("the group that reported got %q, want no verdict", result.Verdicts[kept.Key()])
	}
}

// The whole item, with nothing expected, is absent when nothing arrives;
// with recovery disabled the data that comes back forgets its absence and
// says nothing, where with recovery enabled it is the whole item's NORMAL.
func TestTheWholeItemBackWithRecoveryDisabledGetsNoVerdict(t *testing.T) {
	whole, group := WholeItemGroup().Key(), hostGroup(t, "192.0.2.1")
	open := map[string]GroupMemory{whole: {FirstAbsent: absenceRound1}}
	for _, disabled := range []bool{false, true} {
		input := fullRound(absenceRound2, Roster{Version: "v", Source: RosterWhole}, groupSet(group), open)
		input.RecoveryDisabled = disabled
		result := evaluate(t, input)
		verdict, judged := result.Verdicts[whole]
		if disabled && judged || !disabled && verdict != VerdictNormal {
			t.Fatalf("disabled %v: the whole item got %q (judged %v)", disabled, verdict, judged)
		}
		if _, remembered := result.Memory[whole]; remembered {
			t.Fatalf("disabled %v: the whole item's absence is still remembered", disabled)
		}
	}
}
