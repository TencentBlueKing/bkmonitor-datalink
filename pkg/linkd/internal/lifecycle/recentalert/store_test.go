// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package recentalert

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type fakeRedis struct {
	values         map[string][]byte
	expirations    map[string]time.Duration
	getErr         error
	setErr         error
	transactionErr error
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{values: map[string][]byte{}, expirations: map[string]time.Duration{}}
}

func (f *fakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	command := redis.NewStringCmd(ctx)
	if f.getErr != nil {
		command.SetErr(f.getErr)
		return command
	}
	value, exists := f.values[key]
	if !exists {
		command.SetErr(redis.Nil)
		return command
	}
	command.SetVal(string(value))
	return command
}

func (f *fakeRedis) Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	command := redis.NewStatusCmd(ctx)
	if f.setErr != nil {
		command.SetErr(f.setErr)
		return command
	}
	f.set(key, value.([]byte), expiration)
	command.SetVal("OK")
	return command
}

func (f *fakeRedis) transaction(
	_ context.Context,
	firstKey string,
	firstValue []byte,
	secondKey string,
	secondValue []byte,
	ttl time.Duration,
) error {
	if f.transactionErr != nil {
		return f.transactionErr
	}
	f.set(firstKey, firstValue, ttl)
	f.set(secondKey, secondValue, ttl)
	return nil
}

func (f *fakeRedis) set(key string, value []byte, expiration time.Duration) {
	f.values[key] = append([]byte(nil), value...)
	f.expirations[key] = expiration
}

type recordingObserver struct{ operations []string }

func (o *recordingObserver) Operation(_ context.Context, operation, outcome string) {
	o.operations = append(o.operations, operation+":"+outcome)
}

func TestConfigTTLTracksRefreshIntervalWithSafetyMargin(t *testing.T) {
	t.Parallel()
	for _, refreshInterval := range []time.Duration{time.Second, 5 * time.Second, 17 * time.Second} {
		config := Config{KeyPrefix: "mailbox:recent-alert", RefreshInterval: refreshInterval}
		if err := config.Validate(); err != nil {
			t.Fatalf("Validate(%s) error=%v", refreshInterval, err)
		}
		if got, want := config.TTL(), refreshInterval+5*time.Second; got != want {
			t.Fatalf("TTL(%s)=%s, want %s", refreshInterval, got, want)
		}
	}
	for _, refreshInterval := range []time.Duration{0, -time.Second, 1500 * time.Millisecond} {
		config := Config{KeyPrefix: "mailbox:recent-alert", RefreshInterval: refreshInterval}
		if err := config.Validate(); err == nil {
			t.Fatalf("Validate(%s) error=nil", refreshInterval)
		}
	}
}

func TestStoreCachesCurrentAndTerminalBeyondRefreshWindow(t *testing.T) {
	client := newFakeRedis()
	observer := &recordingObserver{}
	config := Config{KeyPrefix: "mailbox:recent-alert", RefreshInterval: 5 * time.Second}
	cache, err := newStore(client, client.transaction, config, observer)
	if err != nil {
		t.Fatal(err)
	}
	active := cachedAlert("event-1", domain.AlertStatusActive, "")
	if err := cache.PutCurrent(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	key := activeKey(active.Alert)
	currentKey, _ := cache.currentKey(key)
	if client.expirations[currentKey] != 10*time.Second {
		t.Fatalf("current ttl=%s", client.expirations[currentKey])
	}
	got, found, err := cache.GetCurrent(context.Background(), key)
	if err != nil || !found || got.Version != active.Version || got.Alert.AlertID != active.Alert.AlertID {
		t.Fatalf("GetCurrent()=%#v,%t,%v", got, found, err)
	}

	terminal := cachedAlert("event-2", domain.AlertStatusRecovered, domain.AlertEndTypeSource)
	if err := cache.PutTerminal(context.Background(), terminal); err != nil {
		t.Fatal(err)
	}
	endedKey, _ := cache.endedKey(terminal.Alert.BKTenantID, terminal.Alert.LatestEventID)
	if client.expirations[currentKey] != config.TTL() || client.expirations[endedKey] != config.TTL() {
		t.Fatalf("terminal expirations=%#v", client.expirations)
	}
	ended, found, err := cache.GetEndedByEvent(
		context.Background(), terminal.Alert.BKTenantID, terminal.Alert.LatestEventID,
	)
	if err != nil || !found || ended.Alert.Status != domain.AlertStatusRecovered {
		t.Fatalf("GetEndedByEvent()=%#v,%t,%v", ended, found, err)
	}
	for _, expected := range []string{"put_current:succeeded", "get_current:hit", "put_terminal:succeeded", "get_ended:hit"} {
		if !contains(observer.operations, expected) {
			t.Fatalf("operations=%v missing %q", observer.operations, expected)
		}
	}
}

func TestStoreMissAndFailuresAreDistinct(t *testing.T) {
	client := newFakeRedis()
	observer := &recordingObserver{}
	cache, err := newStore(client, client.transaction, Config{KeyPrefix: "mailbox:recent-alert", RefreshInterval: 5 * time.Second}, observer)
	if err != nil {
		t.Fatal(err)
	}
	key := store.ActiveAlertKey{BKTenantID: "tenant-1", EventSourceID: "source", Fingerprint: "fingerprint-1"}
	if _, found, err := cache.GetCurrent(context.Background(), key); err != nil || found {
		t.Fatalf("missing GetCurrent() found=%t err=%v", found, err)
	}
	client.getErr = errors.New("redis unavailable")
	if _, _, err := cache.GetCurrent(context.Background(), key); err == nil || !strings.Contains(err.Error(), "redis unavailable") {
		t.Fatalf("failed GetCurrent() error=%v", err)
	}
	client.getErr = nil
	currentKey, _ := cache.currentKey(key)
	client.values[currentKey] = []byte(`{"schema_version":1}`)
	if _, _, err := cache.GetCurrent(context.Background(), key); err == nil {
		t.Fatal("malformed cache entry was accepted")
	}
	if !contains(observer.operations, "get_current:miss") ||
		!contains(observer.operations, "get_current:failed") ||
		!contains(observer.operations, "get_current:decode_failed") {
		t.Fatalf("operations=%v", observer.operations)
	}
}

func TestStoreTerminalTransactionFailureDoesNotPartiallyWrite(t *testing.T) {
	client := newFakeRedis()
	client.transactionErr = errors.New("transaction failed")
	cache, err := newStore(client, client.transaction, Config{KeyPrefix: "mailbox:recent-alert", RefreshInterval: 5 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	terminal := cachedAlert("event-2", domain.AlertStatusClosed, domain.AlertEndTypeSeverityUpgrade)
	if err := cache.PutTerminal(context.Background(), terminal); err == nil {
		t.Fatal("PutTerminal() error=nil")
	}
	if len(client.values) != 0 {
		t.Fatalf("partial terminal cache values=%#v", client.values)
	}
}

func TestStoreRejectsMismatchedCurrentIdentity(t *testing.T) {
	client := newFakeRedis()
	cache, err := newStore(client, client.transaction, Config{KeyPrefix: "mailbox:recent-alert", RefreshInterval: 5 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored := cachedAlert("event-1", domain.AlertStatusActive, "")
	payload, err := encode(stored)
	if err != nil {
		t.Fatal(err)
	}
	wanted := store.ActiveAlertKey{BKTenantID: "tenant-1", EventSourceID: "source", Fingerprint: "other"}
	key, _ := cache.currentKey(wanted)
	client.values[key] = payload
	if _, _, err := cache.GetCurrent(context.Background(), wanted); err == nil ||
		!strings.Contains(err.Error(), "mismatched identity") {
		t.Fatalf("GetCurrent() error=%v", err)
	}
}

func cachedAlert(eventID string, status domain.AlertStatus, endType domain.AlertEndType) store.StoredAlert {
	alert := storetest.Alert("tenant-1", "alert-1", eventID, "fingerprint-1", "warning")
	if status.Terminal() {
		alert.Status = status
		alert.UpdateAt = alert.UpdateAt.Add(time.Minute)
		alert.LastOccurredAt = alert.LastOccurredAt.Add(time.Minute)
		endAt := alert.LastOccurredAt
		alert.EndAt = &endAt
		alert.EndType = endType
	}
	return store.StoredAlert{Alert: alert, Version: store.NewVersionToken("version-1")}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
