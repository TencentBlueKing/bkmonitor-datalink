// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// Readiness boundary values, closed. A Slot's queries either agree on one
// moment when their data becomes readable, disagree about it, or ask for
// nothing that has a readiness time at all. The three are different questions
// and only the first has a single answer to compare an arrival against.
const (
	// ReadinessBoundaryUnified means every query that needs data agrees on when
	// that data is ready, so the Slot has one readiness moment.
	ReadinessBoundaryUnified = "unified"
	// ReadinessBoundaryMixed means they disagree. There is no one moment this
	// Slot was ready, so there is no one number for how late it arrived --
	// stating an average over the disagreement would invent the very boundary
	// this says does not exist.
	ReadinessBoundaryMixed = "mixed"
	// ReadinessBoundaryNone means nothing in this Slot waits on data.
	ReadinessBoundaryNone = "none"
	// ReadinessBoundaryOther keeps the label set closed against a value minted
	// somewhere else.
	ReadinessBoundaryOther = "OTHER"
)

// SlotReadinessFacts is how long the data had been sitting there when this
// execution finally reached Access.
//
// It answers the question no existing signal does: a deferral says an execution
// arrived too early, and nothing at all says it arrived too late. Both are the
// same mistake measured from opposite sides, and a scheduler that stops
// arriving early by arriving late would show every deferral gone and look like
// a success. That is why this is reported beside the deferral count rather than
// instead of it.
type SlotReadinessFacts struct {
	// Boundary is one of the constants above.
	Boundary string
	// SlackSeconds is the arrival time minus the readiness time: how long the
	// data was ready before anything came to read it. Only a unified boundary
	// has one, which Slack says.
	//
	// It is never negative as recorded, because an execution that arrives
	// before a unified boundary is turned away before it reaches this point.
	// Arriving early is already counted as a deferral; this measures the other
	// direction.
	SlackSeconds float64
	// Slack says whether SlackSeconds means anything. A zero without it is "not
	// measured", not "arrived exactly on time", and those read alike.
	Slack bool
}

func normalizeSlotReadiness(facts *SlotReadinessFacts) *SlotReadinessFacts {
	if facts == nil {
		return nil
	}
	switch facts.Boundary {
	case ReadinessBoundaryUnified, ReadinessBoundaryMixed, ReadinessBoundaryNone:
	default:
		facts.Boundary = ReadinessBoundaryOther
	}
	if facts.Boundary != ReadinessBoundaryUnified {
		// A slack figure only means something against one boundary. Carrying a
		// number here for a Slot whose queries disagree would put a precise
		// value on a question that has no single answer.
		facts.SlackSeconds, facts.Slack = 0, false
	}
	if facts.SlackSeconds < 0 {
		// Recorded past the point that turns early arrivals away, so a negative
		// value means the clock moved rather than that the Slot was early.
		facts.SlackSeconds, facts.Slack = 0, false
	}
	return facts
}
