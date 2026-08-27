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
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

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

// Key returns a bounded Redis String key. Business and strategy IDs remain
// explicit for isolation and diagnosis; all other unbounded fields are
// represented by fixed-size digests.
func (identity RuntimeIdentity) Key(prefix string) (string, error) {
	if strings.TrimSpace(prefix) == "" || !utf8.ValidString(prefix) {
		return "", fmt.Errorf("state: key prefix must be non-empty UTF-8")
	}
	if identity.TenantID == "" || !utf8.ValidString(identity.TenantID) {
		return "", fmt.Errorf("state: tenant_id must be non-empty UTF-8")
	}
	if !isCanonicalSignedInt64(identity.BusinessID) {
		return "", fmt.Errorf("state: business_id must use canonical signed int64 form")
	}
	if !isCanonicalUint64(identity.StrategyID) {
		return "", fmt.Errorf("state: strategy_id must use canonical unsigned uint64 form")
	}
	if !isSHA256Hex(identity.StateCompatibilityHash) {
		return "", fmt.Errorf("state: state_compatibility_hash must be 64 lowercase hexadecimal characters")
	}
	if !isSHA256Hex(identity.DimensionIdentityDigest) {
		return "", fmt.Errorf("state: dimension_identity_digest must be 64 lowercase hexadecimal characters")
	}
	tenantDigest, err := contract.DeriveCanonicalDigestV2("runtime-state-tenant-v1", identity.TenantID)
	if err != nil {
		return "", fmt.Errorf("state: derive tenant digest: %w", err)
	}
	return strings.Join([]string{
		prefix, "runtime", "v1", "w", tenantDigest[:32], identity.BusinessID, identity.StrategyID,
		identity.StateCompatibilityHash[:32], identity.DimensionIdentityDigest[:32],
	}, ":"), nil
}

func isCanonicalSignedInt64(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && strconv.FormatInt(parsed, 10) == value
}

func isCanonicalUint64(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(parsed, 10) == value
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
