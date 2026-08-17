// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type InputIdentity struct {
	TenantID              string
	Purpose               string
	StrategyID            string
	ItemID                string
	StrategyContentSHA256 string
	RecordID              string
}

func DeriveInputID(identity InputIdentity) (string, error) {
	if identity.TenantID == "" {
		return "", invalid("tenant_id", "must be non-empty")
	}
	if err := validatePurpose(identity.Purpose); err != nil {
		return "", err
	}
	if !canonicalDecimalPattern.MatchString(identity.StrategyID) {
		return "", invalid("strategy_id", "must use canonical decimal form")
	}
	if !canonicalDecimalPattern.MatchString(identity.ItemID) {
		return "", invalid("item_id", "must use canonical decimal form")
	}
	if !sha256Pattern.MatchString(identity.StrategyContentSHA256) {
		return "", invalid("strategy_content_sha256", "must be 64 lowercase hexadecimal characters")
	}
	if _, _, err := parseRecordID(identity.RecordID); err != nil {
		return "", err
	}

	digest := sha256.New()
	fields := []string{
		identity.TenantID,
		identity.Purpose,
		identity.StrategyID,
		identity.ItemID,
		identity.StrategyContentSHA256,
		identity.RecordID,
	}
	var prefix [4]byte
	for _, field := range fields {
		if !utf8.ValidString(field) {
			return "", invalid("input_id", "canonical fields must contain valid UTF-8")
		}
		if uint64(len(field)) > math.MaxUint32 {
			return "", invalid("input_id", "canonical field exceeds uint32 length")
		}
		binary.BigEndian.PutUint32(prefix[:], uint32(len(field)))
		_, _ = digest.Write(prefix[:])
		_, _ = digest.Write([]byte(field))
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func parseRecordID(recordID string) (string, int64, error) {
	parts := strings.Split(recordID, ".")
	if len(parts) != 2 || !dimensionsMD5Pattern.MatchString(parts[0]) || !canonicalDecimalPattern.MatchString(parts[1]) {
		return "", 0, invalid("record_id", "must use dimensions_md5.source_time canonical form")
	}
	sourceTime, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, invalid("record_id", "source_time exceeds int64")
	}
	return parts[0], sourceTime, nil
}
