// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"fmt"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	StateSchemaVersion          = "window-state-v1"
	CodecSemanticsVersion       = "window-state-codec-none-v1"
	SourceTimeSemanticsVersion  = "source-time-seconds-position-v1"
	HistoryCellSemanticsVersion = "detect-history-valid-anomalous-v1"
)

// StateSemantics is the M4-owned input used by M3 when it derives the final
// state_compatibility_hash through the M0 canonical digest helper.
type StateSemantics struct {
	StateSchemaVersion          string
	CodecSemanticsVersion       string
	IdentitySchemaDigest        string
	SourceTimeSemanticsVersion  string
	HistoryCellSemanticsVersion string
}

// RuntimeIdentity is the stable identity of one physical time-series state.
// Level is deliberately absent because all dynamic Levels share one value.
type RuntimeIdentity struct {
	TenantID                string
	BusinessID              string
	StrategyID              string
	StateCompatibilityHash  string
	DimensionIdentityDigest string
}

// RuntimeStateSemantics returns the stable M4 semantics consumed by M3. M4
// never derives state_compatibility_hash itself.
func RuntimeStateSemantics() (StateSemantics, error) {
	identityDigest, err := contract.DeriveCanonicalDigestV2("runtime-state-identity-schema-v1", struct {
		TenantID                string `json:"tenant_id"`
		BusinessID              string `json:"business_id"`
		StrategyID              string `json:"strategy_id"`
		StateCompatibilityHash  string `json:"state_compatibility_hash"`
		DimensionIdentityDigest string `json:"dimension_identity_digest"`
	}{
		TenantID:                "non-empty-utf8",
		BusinessID:              "canonical-signed-int64",
		StrategyID:              "canonical-unsigned-uint64",
		StateCompatibilityHash:  "sha256-lower-hex",
		DimensionIdentityDigest: "sha256-lower-hex",
	})
	if err != nil {
		return StateSemantics{}, fmt.Errorf("state: derive identity schema digest: %w", err)
	}
	return StateSemantics{
		StateSchemaVersion:          StateSchemaVersion,
		CodecSemanticsVersion:       CodecSemanticsVersion,
		IdentitySchemaDigest:        identityDigest,
		SourceTimeSemanticsVersion:  SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: HistoryCellSemanticsVersion,
	}, nil
}

func isCanonicalSignedInt64(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && strconv.FormatInt(parsed, 10) == value
}

func isCanonicalUint64(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(parsed, 10) == value
}

// isSHA256Hex reports whether value is a SHA-256 digest spelled as the state
// spells one: 64 lowercase hexadecimal characters. It reads the characters in
// place. Decoding and re-encoding to compare, as it did, allocated two
// buffers per check, and the window checks every point's record id this way
// on every record it applies.
func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if c := value[index]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
