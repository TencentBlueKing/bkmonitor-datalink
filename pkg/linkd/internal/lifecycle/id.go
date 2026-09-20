// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"hash"
	"strings"
	"time"

	"linkd/internal/domain"
)

const (
	// 哈希域隔离不同用途，领域 ID 本身保持不透明且不携带 schema 版本。
	alertLogIDDomain = "linkd:alert-log"
	hookLogIDDomain  = "linkd:alert-hook-log"
	hookCallIDDomain = "linkd:alert-hook-call"
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// DeterministicAlertIDGenerator 为同一 opening Event 生成稳定且不暴露业务字段的 Alert ID。
type DeterministicAlertIDGenerator struct{}

func (DeterministicAlertIDGenerator) Generate(event domain.Event) (string, error) {
	return domain.GenerateAlertID(event, event.CreateAt)
}

func eventAlertLogID(event domain.Event, alertID string, operation domain.OperationKind) string {
	return digestStrings(alertLogIDDomain, event.BKTenantID, event.EventID, alertID, string(operation))
}

func operationAlertLogID(command CloseAlertCommand) string {
	return digestStrings(alertLogIDDomain, command.BKTenantID, command.OperationID, command.AlertID, string(domain.OperationKindClose))
}

func finalHookLogID(cause AlertChangeCause, alert domain.Alert, outcome ProcessOutcome, result FinalHookResult, reasonCode string) string {
	return digestStrings(hookLogIDDomain, alert.BKTenantID, cause.Type, cause.ID,
		alert.AlertID, string(outcome), result.Name, result.Transport, result.Destination, result.MessageID, reasonCode)
}

func hookInvocationID(cause AlertChangeCause, alert domain.Alert, outcome ProcessOutcome) string {
	return digestStrings(hookCallIDDomain, alert.BKTenantID, cause.Type, cause.ID,
		alert.AlertID, alert.UpdateAt.UTC().Format(time.RFC3339Nano), string(outcome))
}

func digestStrings(values ...string) string {
	digest := sha256.New()
	for _, value := range values {
		writeLengthPrefixed(digest, value)
	}
	return strings.ToLower(idEncoding.EncodeToString(digest.Sum(nil)))
}

func writeLengthPrefixed(destination hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write([]byte(value))
}
