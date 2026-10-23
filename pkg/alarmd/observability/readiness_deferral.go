// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"log/slog"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The bases a Slot's readiness is decided on when it is turned away as not
// ready (QUERY_NOT_READY). Closed.
const (
	// ReadinessBasisSettleWait is a Slot with no read hold: every query that
	// needs data shares one readiness moment, its window's end plus the
	// settling wait, and the Slot comes back then.
	ReadinessBasisSettleWait = "settle_wait"
	// ReadinessBasisReadHold is a Slot with a read hold: it comes back when
	// the last of its queries is ready, the settling wait plus the hold.
	ReadinessBasisReadHold = "read_hold"
	// ReadinessBasisOther keeps the word closed against one minted elsewhere.
	ReadinessBasisOther = "other"
)

// maxReadinessDeferralName bounds the requirement and query names on the
// line: both are digests in practice, and a longer one is cut, not dropped.
const maxReadinessDeferralName = 128

// ReadinessDeferralFacts is what a Slot turned away as not ready was waiting
// for: the query whose readiness set the time it comes back, the requirement
// that query serves, that time, and what the time was decided on. Without
// them a QUERY_NOT_READY line said only that the Slot was early, and two
// rounds of one Slot - turned away at :00, run at :11 - could not be read
// against the readiness they waited on.
type ReadinessDeferralFacts struct {
	Requirement      string
	PhysicalQuery    string
	ReadyAtUnixMilli int64
	Basis            string
	// ReadHoldMillis is the read hold inside ReadyAtUnixMilli; zero without
	// one.
	ReadHoldMillis int64
}

func boundedDeferralName(name string) string {
	if len(name) > maxReadinessDeferralName {
		return name[:maxReadinessDeferralName]
	}
	return name
}

func normalizeReadinessDeferral(observation Observation) *ReadinessDeferralFacts {
	facts := observation.ReadinessDeferral
	if facts == nil || observation.ReasonCode != ReasonCode(contract.ReasonQueryNotReady) ||
		(observation.Stage != StageQueryCompleted && observation.Stage != StageSlotCompleted) {
		return nil
	}
	normalized := *facts
	normalized.Requirement = boundedDeferralName(normalized.Requirement)
	normalized.PhysicalQuery = boundedDeferralName(normalized.PhysicalQuery)
	switch normalized.Basis {
	case ReadinessBasisSettleWait, ReadinessBasisReadHold:
	default:
		normalized.Basis = ReadinessBasisOther
	}
	if normalized.ReadyAtUnixMilli < 0 {
		normalized.ReadyAtUnixMilli = 0
	}
	if normalized.ReadHoldMillis < 0 {
		normalized.ReadHoldMillis = 0
	}
	return &normalized
}

// appendReadinessDeferral writes what a not-ready Slot waits for.
func appendReadinessDeferral(attributes []slog.Attr, facts *ReadinessDeferralFacts) []slog.Attr {
	if facts == nil {
		return attributes
	}
	return append(attributes,
		slog.String("not_ready_requirement", facts.Requirement),
		slog.String("not_ready_query", facts.PhysicalQuery),
		slog.Int64("not_ready_at_ms", facts.ReadyAtUnixMilli),
		slog.String("not_ready_basis", facts.Basis),
		slog.Int64("not_ready_read_hold_ms", facts.ReadHoldMillis),
	)
}
