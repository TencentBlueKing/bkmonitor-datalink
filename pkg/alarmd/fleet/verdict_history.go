// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"sync"
	"time"
)

// The deployment verdict is decided on every read and kept nowhere, so a
// verdict someone saw and did not act on at once -- a screenshot of
// DEGRADED an hour later -- has no cause left to read: the degradations
// and counts that decided it are gone with the view. The service keeps the
// last changes of the verdict it decided, each with what decided it.
//
// It is this process's own record, in memory: every replica decides the
// verdict for the reads it answers and the scrapes of its metrics, so each
// keeps its own (VerdictHistoryReplica names whose), and a restart starts
// it again (VerdictHistorySince says from when). The first verdict decided
// is recorded too, so a reader always sees where the record starts.
//
// It is a sample, not every change: the verdict is only decided when the
// health route is read or the verdict metric scraped, so a verdict that
// flipped and flipped back between two decisions is not in it. The two
// deciders take their times independently; a decision that started before
// the last one recorded is dropped rather than recorded out of order.

// MaxVerdictChanges bounds the record.
const MaxVerdictChanges = 32

// VerdictChange is one change of the deployment verdict and what decided
// the new one: its degradations and gaps by kind, and the counts the rule
// reads. From is empty on the first verdict of the record.
type VerdictChange struct {
	At           time.Time         `json:"at"`
	From         Health            `json:"from,omitempty"`
	To           Health            `json:"to"`
	Degradations []DegradationKind `json:"degradations,omitempty"`
	Gaps         []GapKind         `json:"gaps,omitempty"`
	Ours         int               `json:"ours"`
	Unattributed int               `json:"unattributed"`
	Anomalies    int               `json:"anomalies"`
	Covered      int               `json:"covered"`
	Determined   int               `json:"determined"`
}

type verdictHistory struct {
	mu      sync.Mutex
	replica string
	since   time.Time
	lastAt  time.Time
	last    Health
	changes [MaxVerdictChanges]VerdictChange
	next    int
	full    bool
}

// RecordVerdict notes the verdict just decided on view, keeping it when it
// differs from the last one noted. Callers call it after Decide on the
// paths that decide the verdict for a reader: the health route and the
// verdict metric's scrape.
func (service *Service) RecordVerdict(view *View, at time.Time) {
	if service == nil || view == nil {
		return
	}
	history := &service.verdicts
	history.mu.Lock()
	defer history.mu.Unlock()
	if history.since.IsZero() {
		history.since = at
	} else if at.Before(history.lastAt) {
		return
	}
	history.lastAt = at
	if view.Health == history.last {
		return
	}
	change := VerdictChange{At: at, From: history.last, To: view.Health,
		Degradations: distinctDegradations(view.Degradations), Gaps: distinctGaps(view.Gaps),
		Ours: OursCount(view.Anomalies), Unattributed: UnattributedCount(view.Anomalies),
		Anomalies: view.AnomaliesTotal, Covered: view.Covered, Determined: view.Determined}
	history.last = view.Health
	history.changes[history.next] = change
	history.next = (history.next + 1) % MaxVerdictChanges
	if history.next == 0 {
		history.full = true
	}
}

// SetReplica names the replica whose record this is.
func (service *Service) SetReplica(replica string) {
	if service == nil {
		return
	}
	service.verdicts.mu.Lock()
	service.verdicts.replica = replica
	service.verdicts.mu.Unlock()
}

// VerdictReplica is the replica whose record VerdictHistory is.
func (service *Service) VerdictReplica() string {
	if service == nil {
		return ""
	}
	service.verdicts.mu.Lock()
	defer service.verdicts.mu.Unlock()
	return service.verdicts.replica
}

// VerdictHistory is the recorded changes, oldest first, and since when the
// record runs; zero when nothing has been decided yet.
func (service *Service) VerdictHistory() ([]VerdictChange, time.Time) {
	if service == nil {
		return nil, time.Time{}
	}
	history := &service.verdicts
	history.mu.Lock()
	defer history.mu.Unlock()
	var out []VerdictChange
	if history.full {
		out = append(out, history.changes[history.next:]...)
	}
	out = append(out, history.changes[:history.next]...)
	for i := range out {
		out[i].Degradations = append([]DegradationKind(nil), out[i].Degradations...)
		out[i].Gaps = append([]GapKind(nil), out[i].Gaps...)
	}
	return out, history.since
}

func distinctDegradations(degradations []Degradation) []DegradationKind {
	seen := map[DegradationKind]bool{}
	var kinds []DegradationKind
	for _, degradation := range degradations {
		if !seen[degradation.Kind] {
			seen[degradation.Kind] = true
			kinds = append(kinds, degradation.Kind)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

func distinctGaps(gaps []Gap) []GapKind {
	seen := map[GapKind]bool{}
	var kinds []GapKind
	for _, gap := range gaps {
		if !seen[gap.Kind] {
			seen[gap.Kind] = true
			kinds = append(kinds, gap.Kind)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}
