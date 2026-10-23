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
	"encoding/base32"
	"encoding/binary"
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
	entityIDEncoding    = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
)

// ParsedEntityID 是 EventID 或 AlertID 中按 UTC 秒解释的路由时间。
// 租户和来源使用对象字段及仓储作用域，不从 ID 推断。
type ParsedEntityID struct {
	Timestamp time.Time
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
		"event",
		receivedAt,
		digestEntityID(eventIDDomain, bkTenantID, eventSourceID, stableSourceID, receivedAt.Format(time.RFC3339Nano)),
	), nil
}

// GenerateAlertID 根据 opening Event 与稳定创建锚点生成可解析且可重放的 AlertID。
func GenerateAlertID(event Event, createAt time.Time) (string, error) {
	if err := event.ValidateIdentity(); err != nil {
		return "", fmt.Errorf("generate alert id from invalid event: %w", err)
	}
	if createAt.IsZero() {
		return "", fmt.Errorf("generate alert id: create_at must not be zero")
	}
	createAt = createAt.Round(0).UTC()
	return formatEntityID(
		"alert",
		createAt,
		digestEntityID(alertIDDomain, event.BKTenantID, event.EventSourceID, event.EventID, createAt.Format(time.RFC3339Nano)),
	), nil
}

// ParseEventID 读取 EventID 的 UTC 路由时间，兼容旧点号格式；不验证身份摘要。
func ParseEventID(value string) (ParsedEntityID, error) {
	parsed, err := parseEntityID(value, "event")
	if err != nil {
		return ParsedEntityID{}, fmt.Errorf("parse event id: %w", err)
	}
	return parsed, nil
}

// ParseAlertID 读取 AlertID 的 UTC 路由时间，兼容旧点号格式；不验证身份摘要。
func ParseAlertID(value string) (ParsedEntityID, error) {
	parsed, err := parseEntityID(value, "alert")
	if err != nil {
		return ParsedEntityID{}, fmt.Errorf("parse alert id: %w", err)
	}
	return parsed, nil
}

// ValidateIdentityPart 校验租户、来源等作用域标识的长度和字符范围。
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

func formatEntityID(kind string, timestamp time.Time, digest string) string {
	return kind + "-" + timestamp.UTC().Format(entityIDTimeLayout) + "-" + digest
}

func parseEntityID(value, kind string) (ParsedEntityID, error) {
	if len(value) < 1 || len(value) > EntityIDMaxBytes {
		return ParsedEntityID{}, fmt.Errorf("entity id length must be between 1 and %d bytes", EntityIDMaxBytes)
	}
	// 新 ID 通过对象前缀定位时间；旧 ID 仅保留首段时间的读取能力，供存量索引路由使用。
	// 此处不还原租户或来源，也不校验摘要，避免把路由解析变成业务身份判定。
	timestampText, _, found := strings.Cut(value, ".")
	if rest, ok := strings.CutPrefix(value, kind+"-"); ok {
		timestampText, _, found = strings.Cut(rest, "-")
	}
	if !found {
		return ParsedEntityID{}, fmt.Errorf("entity id must contain a %s prefix or a legacy timestamp", kind)
	}
	timestamp, err := time.ParseInLocation(entityIDTimeLayout, timestampText, time.UTC)
	if err != nil || timestamp.Format(entityIDTimeLayout) != timestampText {
		return ParsedEntityID{}, fmt.Errorf("entity id timestamp is invalid: %q", timestampText)
	}
	return ParsedEntityID{Timestamp: timestamp}, nil
}

func digestEntityID(values ...string) string {
	digest := sha256.New()
	for _, value := range values {
		writeIdentityPart(digest, value)
	}
	return entityIDEncoding.EncodeToString(digest.Sum(nil)[:entityIDDigestBytes])
}

func writeIdentityPart(destination hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write([]byte(value))
}
