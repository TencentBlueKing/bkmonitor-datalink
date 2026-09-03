// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package mailbox 定义 Cleaner 与 Lifecycle 之间的 fingerprint Mailbox 协议。
package mailbox

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"
)

// SignalSchemaVersion 是当前首版 Mailbox 唤醒信号的 schema 版本。
const SignalSchemaVersion = 1

var correlationEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Signal 只表示一个 Mailbox 当前存在待处理 Event，不绑定具体 Event。
type Signal struct {
	SchemaVersion int       `json:"schema_version"`
	MessageID     string    `json:"message_id"`
	BKTenantID    string    `json:"bk_tenant_id"`
	EventSourceID string    `json:"event_source_id"`
	Fingerprint   string    `json:"fingerprint"`
	MailboxID     string    `json:"mailbox_id"`
	EnqueuedAt    time.Time `json:"enqueued_at"`
}

// NewSignal 构造一个可重复产生、身份稳定的 Mailbox 唤醒信号。
func NewSignal(bkTenantID, eventSourceID, fingerprint string, enqueuedAt time.Time) Signal {
	mailboxID := CorrelationKey(bkTenantID, eventSourceID, fingerprint)
	return Signal{
		SchemaVersion: SignalSchemaVersion,
		MessageID:     mailboxID, BKTenantID: bkTenantID, EventSourceID: eventSourceID,
		Fingerprint: fingerprint, MailboxID: mailboxID, EnqueuedAt: enqueuedAt.Round(0).UTC(),
	}
}

func EncodeSignal(signal Signal) ([]byte, error) {
	signal.EnqueuedAt = signal.EnqueuedAt.Round(0).UTC()
	if err := signal.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(signal)
	if err != nil {
		return nil, fmt.Errorf("encode mailbox signal: %w", err)
	}
	return body, nil
}

func DecodeSignal(data []byte) (Signal, error) {
	var signal Signal
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&signal); err != nil {
		return Signal{}, fmt.Errorf("decode mailbox signal: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Signal{}, fmt.Errorf("decode mailbox signal: trailing JSON data")
		}
		return Signal{}, fmt.Errorf("decode mailbox signal trailing data: %w", err)
	}
	if err := signal.Validate(); err != nil {
		return Signal{}, err
	}
	return signal, nil
}

func (s Signal) Validate() error {
	if s.SchemaVersion != SignalSchemaVersion {
		return fmt.Errorf("mailbox signal schema_version must be %d: %d", SignalSchemaVersion, s.SchemaVersion)
	}
	for name, value := range map[string]string{
		"message_id": s.MessageID, "bk_tenant_id": s.BKTenantID, "event_source_id": s.EventSourceID,
		"fingerprint": s.Fingerprint, "mailbox_id": s.MailboxID,
	} {
		if value == "" {
			return fmt.Errorf("mailbox signal %s must not be empty", name)
		}
	}
	if s.EnqueuedAt.IsZero() {
		return fmt.Errorf("mailbox signal enqueued_at must not be zero")
	}
	want := CorrelationKey(s.BKTenantID, s.EventSourceID, s.Fingerprint)
	if s.MailboxID != want || s.MessageID != want {
		return fmt.Errorf("mailbox signal identity does not match tenant/source/fingerprint")
	}
	return nil
}

// CorrelationKey 返回租户与来源作用域下的稳定 Mailbox 身份。
// Signal schema 可以独立演进，但不能改变同一关联键的 Mailbox 身份。
func CorrelationKey(bkTenantID, eventSourceID, fingerprint string) string {
	digest := sha256.New()
	for _, value := range []string{"linkd:lifecycle:correlation", bkTenantID, eventSourceID, fingerprint} {
		writeLengthPrefixed(digest, value)
	}
	return strings.ToLower(correlationEncoding.EncodeToString(digest.Sum(nil)))
}

func writeLengthPrefixed(destination hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write([]byte(value))
}
