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
	// Counters since the Leader process started.
	Publications        uint64 `json:"publications"`
	PublicationsSkipped uint64 `json:"publications_skipped"`
	SnapshotChunksSent  uint64 `json:"snapshot_chunks_sent"`
	DeltasSent          uint64 `json:"deltas_sent"`
	EmptyDeltasSent     uint64 `json:"empty_deltas_sent"`
	Refusals            uint64 `json:"refusals"`
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

// ViewStreamLagging is one Worker behind the current version.
type ViewStreamLagging struct {
	WorkerID       string `json:"worker_id"`
	Incarnation    string `json:"incarnation,omitempty"`
	Failure        string `json:"failure,omitempty"`
	ObjectsMissing int    `json:"objects_missing"`
	Connected      bool   `json:"connected"`
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
	line := fmt.Sprintf("视图已装载 %d/%d，版本 %d", facts.Installed, facts.Expected, facts.Revision)
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
