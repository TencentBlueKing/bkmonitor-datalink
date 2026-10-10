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
	"fmt"
	"strings"
	"time"
)

// The view stream, as the Leader serving it accounts for it.
//
// Under decision-016 the Leader pushes each Worker its view of the desired
// set as a stream of versions, and each Worker answers what it did with each
// version: received, installed, switched to. The Leader's ledger of those
// answers is the one place that says whether the fleet is on the current
// version and who is not; before this it lived in the Leader's process and
// its metrics. This is that ledger's current page, on the first screen.

// ViewStreamStallBound is how long a Leader may fail to publish, or have its
// view reach no Worker, before the verdict degrades: a dozen reconcile
// rounds, well past a slow round or a rollout's reconnects. The start of a
// no-session run is noted when the server's stats are read, so the verdict
// can lag the true start by at most one stats read interval.
const ViewStreamStallBound = time.Minute

// ViewStreamFacts is the Leader's account of the stream at one instant. A
// replica that is not the Leader publishes Leading false and nothing else
// meaningful; the aggregate prefers the Leader's.
type ViewStreamFacts struct {
	At      time.Time `json:"at"`
	Leading bool      `json:"leading"`
	// ControlEpoch and Revision identify the current version; Sessions is
	// how many Workers hold a stream open right now.
	ControlEpoch uint64 `json:"control_epoch"`
	Revision     uint64 `json:"revision"`
	Sessions     int    `json:"sessions"`
	// The current version's ledger: Expected receivers, and how many have
	// been Sent it, Acked it, Installed it, Switched to it. Installed over
	// Expected is the reading; Switched stays zero while Workers shadow.
	Expected  int `json:"expected"`
	Sent      int `json:"sent"`
	Acked     int `json:"acked"`
	Installed int `json:"installed"`
	Switched  int `json:"switched"`
	// Ignored is receipts the ledger would not count, by why.
	Ignored ViewStreamIgnored `json:"ignored"`
	// Lagging is every Worker that has not installed the current version,
	// sorted by Worker; Connected false is a Worker with no stream open.
	Lagging []ViewStreamLagging `json:"lagging"`
	// NotSwitched lists the Workers that installed the current version but
	// do not yet execute every one of its Query Groups from it, each with
	// the count it does (decision-016 batch 4). Empty is every installed
	// Worker switched; in the shadow step it is every installed Worker. The
	// batch 4a drill reads this going empty after a rollout and a cutover.
	NotSwitched []ViewStreamLagging `json:"not_switched"`
	// Objects is what the Workers that installed the current version said
	// about their objects, the Leader's account of it: how many probed and
	// reported, how many could not, the missing objects summed over those
	// that probed, and the names of those that could not. It is the only
	// place the fleet can read a probe that failed after one that succeeded:
	// a lagging Worker has not installed anything and so has probed nothing.
	Objects ViewStreamObjects `json:"objects"`
	// Counters since the Leader process started.
	Publications        uint64 `json:"publications"`
	PublicationsSkipped uint64 `json:"publications_skipped"`
	SnapshotChunksSent  uint64 `json:"snapshot_chunks_sent"`
	DeltasSent          uint64 `json:"deltas_sent"`
	EmptyDeltasSent     uint64 `json:"empty_deltas_sent"`
	// DeltasOversized is deltas replaced by a chunked snapshot because one
	// message of them would have exceeded the stream's message bound.
	DeltasOversized uint64 `json:"deltas_oversized"`
	Refusals        uint64 `json:"refusals"`
	// PublishFailures is the current run of desired sets the Leader could
	// not publish, PublishFailureReason the latest one's reason, and
	// PublishFailingSeconds how long the run has lasted, absent while
	// publishing works. NoSessionsSeconds is how long Workers have been
	// expected with none holding a stream, absent otherwise. Each has a
	// BeyondBound flag the verdict reads, set past ViewStreamStallBound.
	PublishFailures           uint64   `json:"publish_failures"`
	PublishFailureReason      string   `json:"publish_failure_reason,omitempty"`
	PublishFailingSeconds     *float64 `json:"publish_failing_seconds,omitempty"`
	PublishFailingBeyondBound bool     `json:"publish_failing_beyond_bound"`
	NoSessionsSeconds         *float64 `json:"no_sessions_seconds,omitempty"`
	NoSessionsBeyondBound     bool     `json:"no_sessions_beyond_bound"`
	// Line is the one sentence for the first screen, composed here so the
	// page and any other reader say the same thing.
	Line string `json:"line"`
}

// ViewStreamIgnored is receipts the ledger did not count, by reason.
type ViewStreamIgnored struct {
	UnknownVersion     int `json:"unknown_version"`
	UnexpectedReceiver int `json:"unexpected_receiver"`
	DigestMismatch     int `json:"digest_mismatch"`
	StaleIncarnation   int `json:"stale_incarnation"`
}

// ViewStreamLagging is one Worker behind the current version. It carries
// nothing about the Worker's objects: a Worker short of installed has
// probed nothing, and the pair of object fields that used to sit here was
// false and null for every entry -- a probe that never happened, written as
// if it had. The installed Workers' objects are ViewStreamObjects.
type ViewStreamLagging struct {
	WorkerID    string `json:"worker_id"`
	Incarnation string `json:"incarnation,omitempty"`
	Failure     string `json:"failure,omitempty"`
	Connected   bool   `json:"connected"`
	// SwitchedQueryGroups is the Worker's latest count of Query Groups it
	// executes from the view; meaningful on NotSwitched, where it is how far
	// short of switched the Worker is.
	SwitchedQueryGroups int `json:"switched_query_groups,omitempty"`
}

// ViewStreamObjects is the installed Workers' word on their objects. Probed
// and Unprobed count Workers; Missing counts objects over the probed ones
// only, so it is a number about Probed Workers and says nothing about the
// Unprobed -- a Worker whose probe failed has no count, and the count it
// had from an earlier probe that succeeded is not one now. UnprobedWorkers
// names them, sorted, since the page's question is which Worker cannot
// tell; it is an empty list rather than null when none.
type ViewStreamObjects struct {
	Probed          int      `json:"probed"`
	Unprobed        int      `json:"unprobed"`
	Missing         int      `json:"missing"`
	UnprobedWorkers []string `json:"unprobed_workers"`
}

// viewStreamLineLagging is how many lagging Workers the line names before
// it counts the rest.
const viewStreamLineLagging = 2

// ViewStreamLine composes the first-screen sentence from the facts: installed
// over expected and the revision, then the lagging Workers by name -- the
// first two, each with why (not connected, or its last failure word) -- and a
// count of the rest. A replica that is not the Leader says only that.
func ViewStreamLine(facts *ViewStreamFacts) string {
	if facts == nil {
		return ""
	}
	if !facts.Leading {
		return "非 leader，不服务视图流"
	}
	if facts.Expected == 0 {
		return fmt.Sprintf("视图流：尚无接收方（版本 %d，%d 条连接）", facts.Revision, facts.Sessions)
	}
	line := fmt.Sprintf("视图已装载 %d/%d，版本 %d", facts.Installed, facts.Expected, facts.Revision) + viewStreamObjectsWords(facts.Objects)
	if len(facts.Lagging) == 0 {
		return line
	}
	named := make([]string, 0, viewStreamLineLagging)
	for index, lagging := range facts.Lagging {
		if index == viewStreamLineLagging {
			break
		}
		why := "未连接"
		if lagging.Connected {
			why = lagging.Failure
			if why == "" {
				why = "未回执"
			}
		}
		named = append(named, lagging.WorkerID+"（"+why+"）")
	}
	line += "；落后：" + strings.Join(named, "、")
	if rest := len(facts.Lagging) - len(named); rest > 0 {
		line += fmt.Sprintf(" 等 %d 个", len(facts.Lagging))
	}
	return line
}

// viewStreamObjectsWords is the objects clause of the first-screen line:
// nothing when every installed Worker probed and found its objects, the
// missing count when some are missing, and the Workers that could not
// probe by name -- "cannot tell" said as such, apart from "nothing
// missing", which is what a silent clause would otherwise claim for them.
func viewStreamObjectsWords(objects ViewStreamObjects) string {
	words := ""
	if objects.Missing > 0 {
		words += fmt.Sprintf("；缺对象 %d（%d 个副本探到）", objects.Missing, objects.Probed)
	}
	if objects.Unprobed > 0 {
		named := objects.UnprobedWorkers
		if len(named) > viewStreamLineLagging {
			named = named[:viewStreamLineLagging]
		}
		words += fmt.Sprintf("；%d 个副本未探到对象", objects.Unprobed)
		if len(named) > 0 {
			words += "（" + strings.Join(named, "、") + "）"
		}
	}
	return words
}
