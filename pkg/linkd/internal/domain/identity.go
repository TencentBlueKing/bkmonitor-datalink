// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"regexp"
	"strings"
	"time"
)

const (
	// EntityIDMaxBytes 是 EventID 与 AlertID 的统一最大字节数。
	EntityIDMaxBytes    = 160
	entityIDTimeLayout  = "20060102150405"
	entityIDDigestBytes = 8
	eventIDDomain       = "linkd:event"
	alertIDDomain       = "linkd:alert"
)

var (
	identityPartPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	hexDigestPattern    = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// ParsedEntityID 是 EventID 或 AlertID 中可逆解析的路由信息。
// Timestamp 固定按 UTC 秒解释；Digest 是稳定身份摘要，不包含可逆的来源业务 ID。
type ParsedEntityID struct {
	Timestamp     time.Time
	BKTenantID    string
	EventSourceID string
	Digest        string
}

// GenerateEventID 根据稳定接收时间与来源身份生成可解析且可重放的 EventID。
func GenerateEventID(
	bkTenantID, eventSourceID, stableSourceID string,
	receivedAt time.Time,
) (string, error) {
	if err := validateEntityIDInput(bkTenantID, eventSourceID, stableSourceID, receivedAt); err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}
	receivedAt = receivedAt.Round(0).UTC()
	return formatEntityID(
		receivedAt,
		bkTenantID,
		eventSourceID,
		digestEntityID(eventIDDomain, bkTenantID, eventSourceID, stableSourceID, receivedAt.Format(time.RFC3339Nano)),
	)
}

// GenerateAlertID 根据 opening Event 与稳定创建锚点生成可解析且可重放的 AlertID。
func GenerateAlertID(event Event, createAt time.Time) (string, error) {
	if err := event.Validate(); err != nil {
		return "", fmt.Errorf("generate alert id from invalid event: %w", err)
	}
	if createAt.IsZero() {
		return "", fmt.Errorf("generate alert id: create_at must not be zero")
	}
	createAt = createAt.Round(0).UTC()
	return formatEntityID(
		createAt,
		event.BKTenantID,
		event.EventSourceID,
		digestEntityID(alertIDDomain, event.BKTenantID, event.EventSourceID, event.EventID, createAt.Format(time.RFC3339Nano)),
	)
}

// ParseEventID 解析 EventID 中的时间、租户、来源和摘要。
func ParseEventID(value string) (ParsedEntityID, error) {
	parsed, err := parseEntityID(value)
	if err != nil {
		return ParsedEntityID{}, fmt.Errorf("parse event id: %w", err)
	}
	return parsed, nil
}

// ParseAlertID 解析 AlertID 中的时间、租户、来源和摘要。
func ParseAlertID(value string) (ParsedEntityID, error) {
	parsed, err := parseEntityID(value)
	if err != nil {
		return ParsedEntityID{}, fmt.Errorf("parse alert id: %w", err)
	}
	return parsed, nil
}

// ValidateIdentityPart 校验可直接出现在结构化 ID 中的租户或来源片段。
func ValidateIdentityPart(name, value string, maximum int) error {
	if len(value) < 1 || len(value) > maximum {
		return fmt.Errorf("%s length must be between 1 and %d bytes", name, maximum)
	}
	if !identityPartPattern.MatchString(value) {
		return fmt.Errorf("%s has invalid format: %q", name, value)
	}
	return nil
}

func validateEntityIDInput(bkTenantID, eventSourceID, stableSourceID string, timestamp time.Time) error {
	if err := ValidateIdentityPart("bk_tenant_id", bkTenantID, 64); err != nil {
		return err
	}
	if err := ValidateIdentityPart("event_source_id", eventSourceID, 32); err != nil {
		return err
	}
	if stableSourceID == "" {
		return fmt.Errorf("stable source id must not be empty")
	}
	if timestamp.IsZero() {
		return fmt.Errorf("timestamp must not be zero")
	}
	return nil
}

func formatEntityID(timestamp time.Time, bkTenantID, eventSourceID, digest string) (string, error) {
	value := strings.Join([]string{
		timestamp.UTC().Format(entityIDTimeLayout),
		bkTenantID,
		eventSourceID,
		digest,
	}, ".")
	if len(value) > EntityIDMaxBytes {
		return "", fmt.Errorf("entity id exceeds %d bytes", EntityIDMaxBytes)
	}
	return value, nil
}

func parseEntityID(value string) (ParsedEntityID, error) {
	if len(value) < 1 || len(value) > EntityIDMaxBytes {
		return ParsedEntityID{}, fmt.Errorf("entity id length must be between 1 and %d bytes", EntityIDMaxBytes)
	}
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return ParsedEntityID{}, fmt.Errorf("entity id must contain timestamp, tenant, source, and digest")
	}
	timestamp, err := time.ParseInLocation(entityIDTimeLayout, parts[0], time.UTC)
	if err != nil || timestamp.Format(entityIDTimeLayout) != parts[0] {
		return ParsedEntityID{}, fmt.Errorf("entity id timestamp is invalid: %q", parts[0])
	}
	if err := ValidateIdentityPart("bk_tenant_id", parts[1], 64); err != nil {
		return ParsedEntityID{}, err
	}
	if err := ValidateIdentityPart("event_source_id", parts[2], 32); err != nil {
		return ParsedEntityID{}, err
	}
	if !hexDigestPattern.MatchString(parts[3]) {
		return ParsedEntityID{}, fmt.Errorf("entity id digest must contain 16 lowercase hexadecimal characters")
	}
	return ParsedEntityID{
		Timestamp: timestamp, BKTenantID: parts[1], EventSourceID: parts[2], Digest: parts[3],
	}, nil
}

func digestEntityID(values ...string) string {
	digest := sha256.New()
	for _, value := range values {
		writeIdentityPart(digest, value)
	}
	return hex.EncodeToString(digest.Sum(nil)[:entityIDDigestBytes])
}

func writeIdentityPart(destination hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write([]byte(value))
}
