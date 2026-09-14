// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package openalerts keeps this process's copy of the alert consumer's open
// alert set: which series, per strategy, the consumer still holds an alert
// on. The trigger asks it before a RECOVERY envelope goes (contract.OpenAlertSet).
//
// The copy has three states, and telling them apart is most of the package.
// Authoritative: the consumer's publication was read and is fresh, and the
// copy answers from it. Self-maintained: the publication is missing, stale,
// unreadable or under another fingerprint algorithm, and the copy answers
// from the last publication it did read plus what this process itself has
// sent since. Never loaded: no publication has been read since the process
// started. An absent publication is never read as an empty one: on a live
// deployment there are always open alerts, so "nothing there" is far more
// likely the publisher than the alerts, and reading it as empty would hold
// every recovery with nothing to show for it.
package openalerts

import (
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The key contract with the publisher. It is pinned here and nowhere else
// on this side; the publisher's requirement document is its other half and
// names the same keys and fields.
//
// The prefix is fixed rather than derived from this deployment's state
// prefix: the writer is another service, and a contract that made it learn
// this process's configuration would fail the first time the two were
// deployed with different values, and fail as "no open alerts".
const (
	// KeyPrefix is what every key of the contract starts with. It says who
	// the data is for, not who writes it.
	KeyPrefix = "alarmd:open_alerts:"
	// HeartbeatKey is a HASH the publisher rewrites every cycle. Its fields
	// are below. A hash rather than a string so that a field can be added
	// without breaking an older reader.
	HeartbeatKey = KeyPrefix + "heartbeat"
	// HeartbeatPublishedAt is the unix second the last complete publication
	// cycle finished.
	HeartbeatPublishedAt = "published_at"
	// HeartbeatCycleSeconds is the publisher's cycle length. The reader's
	// staleness bound is expressed in cycles and computed from this, so the
	// bound follows the publisher's cadence instead of living as a second
	// constant that stops being true when the first one changes.
	HeartbeatCycleSeconds = "cycle_seconds"
	// HeartbeatFingerprintVersion is the fingerprint algorithm the published
	// members were computed under. A reader under another version treats the
	// publication as unavailable: with the wrong algorithm every lookup would
	// miss, which reads exactly like "no open alert" and would close the gate
	// without a trace.
	HeartbeatFingerprintVersion = "fingerprint_version"

	// FingerprintVersion is the algorithm this reader computes fingerprints
	// under, the one the publisher has to echo.
	FingerprintVersion = contract.MonitorDedupeMD5Version

	// StalenessCycles is how many publication cycles may pass without a fresh
	// heartbeat before the publication is stale. This is the staleness bound
	// we accept, and it is also how long a recovery that was sent but lost on
	// the way stays held before the authoritative publication corrects the
	// copy (the copy subtracts on send, not on confirmation). Widening it is
	// not fewer alerts; it is a longer exposure for that class of held alert.
	StalenessCycles = 3
	// LocalRetentionCycles is how many cycles a fingerprint this process sent
	// stays in the copy after an authoritative publication that does not carry
	// it: long enough for the publisher's lag, no longer, or the copy would
	// keep saying "open" about alerts the consumer has closed.
	LocalRetentionCycles = 2
)

// StrategyKey identifies one published set.
type StrategyKey struct {
	TenantID   string
	StrategyID string
}

// SetKey is the SET the publisher keeps for one strategy: its members are
// the fingerprints of the series the consumer holds an open alert on. The
// publisher rewrites it every cycle and gives it a TTL, so an absent key
// means "not written this cycle", never "written once and then missed".
func SetKey(key StrategyKey) string {
	return KeyPrefix + key.TenantID + ":" + key.StrategyID
}

// Heartbeat is what the publisher says about its last cycle.
type Heartbeat struct {
	PublishedAt        time.Time
	Cycle              time.Duration
	FingerprintVersion string
}

// ParseHeartbeat reads the heartbeat hash. A missing or malformed field is
// an error, not a default: a heartbeat that cannot be read is an unavailable
// publication, and a default cycle or version would turn that into a fresh
// one.
func ParseHeartbeat(fields map[string]string) (Heartbeat, error) {
	published, err := strconv.ParseInt(fields[HeartbeatPublishedAt], 10, 64)
	if err != nil || published <= 0 {
		return Heartbeat{}, &HeartbeatError{Field: HeartbeatPublishedAt, Value: fields[HeartbeatPublishedAt]}
	}
	cycle, err := strconv.ParseInt(fields[HeartbeatCycleSeconds], 10, 64)
	if err != nil || cycle <= 0 {
		return Heartbeat{}, &HeartbeatError{Field: HeartbeatCycleSeconds, Value: fields[HeartbeatCycleSeconds]}
	}
	version := fields[HeartbeatFingerprintVersion]
	if version == "" {
		return Heartbeat{}, &HeartbeatError{Field: HeartbeatFingerprintVersion, Value: version}
	}
	return Heartbeat{PublishedAt: time.Unix(published, 0), Cycle: time.Duration(cycle) * time.Second, FingerprintVersion: version}, nil
}

// HeartbeatError names the heartbeat field that could not be read.
type HeartbeatError struct {
	Field string
	Value string
}

func (err *HeartbeatError) Error() string {
	return "alarmd openalerts: heartbeat field " + err.Field + " unreadable: " + strconv.Quote(err.Value)
}
