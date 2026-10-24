// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package linkdtoken

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// AdminToken is a token as the admin's cliTokens().issue writes it: the
// header {"alg":"HS256","typ":"cli+jwt"}, the claims v, sub, iss,
// iat, exp, rfa, cv and jti, and an HMAC-SHA256 signature, each part
// base64url without padding. The admin's grant gives 90 days and refreshes
// in the last 7.
func AdminToken(t testing.TB, issued time.Time, jti string) string {
	t.Helper()
	header, _ := json.Marshal(struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}{"HS256", "cli+jwt"})
	exp := issued.Add(90 * 24 * time.Hour).Unix()
	claims, _ := json.Marshal(map[string]any{"v": 1, "sub": "admin", "iss": "admin/", "iat": issued.Unix(),
		"exp": exp, "rfa": exp - 7*86400, "cv": "fingerprint", "jti": jti})
	body := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte("signing-key"))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func testStore(t *testing.T, now *time.Time) (*Store, redis.UniversalClient) {
	t.Helper()
	instance := redistest.Start(t)
	if instance == nil {
		t.Skip("no redis-server")
	}
	client := instance.Client()
	store, err := New(client, "test-alarmd", func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return store, client
}

// The state walks the token's life: none before a hand-over, valid, due
// for replacement in its last seven days, expired after, and back to valid
// with a new token.
func TestTheStateFollowsTheTokensLife(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store, _ := testStore(t, &now)
	ctx := context.Background()
	if answer, err := store.State(ctx); err != nil || answer != (Answer{State: StateNone}) {
		t.Fatalf("before a hand-over: %+v %v", answer, err)
	}
	token := AdminToken(t, now, "first")
	answer, err := store.Put(ctx, token)
	expires := now.Add(90 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if err != nil || answer != (Answer{State: StateValid, ExpiresAt: expires}) {
		t.Fatalf("handed over: %+v %v", answer, err)
	}
	if got, err := store.Token(ctx); err != nil || got != token {
		t.Fatalf("token %q err %v", got, err)
	}
	now = now.Add(84 * 24 * time.Hour)
	if answer, _ := store.State(ctx); answer.State != StateRefreshDue {
		t.Fatalf("in its last seven days: %+v", answer)
	}
	if got, err := store.Token(ctx); err != nil || got != token {
		t.Fatalf("a token due for replacement is still used until the next: %q %v", got, err)
	}
	now = now.Add(7 * 24 * time.Hour)
	if answer, _ := store.State(ctx); answer.State != StateExpired && answer.State != StateNone {
		t.Fatalf("past its expiry: %+v", answer)
	}
	if _, err := store.Token(ctx); !errors.As(err, new(ErrNoToken)) {
		t.Fatalf("an expired token was used: %v", err)
	}
	if answer, err := store.Put(ctx, AdminToken(t, now, "second")); err != nil || answer.State != StateValid {
		t.Fatalf("a new hand-over: %+v %v", answer, err)
	}
}

// A refusal marks the token it was made with, and only that one: a token
// handed over meanwhile stays valid, and the refused one is no longer used.
func TestARefusalMarksOnlyTheTokenItWasMadeWith(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store, _ := testStore(t, &now)
	ctx := context.Background()
	first := AdminToken(t, now, "first")
	if _, err := store.Put(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Rejected(ctx, first); err != nil {
		t.Fatal(err)
	}
	if answer, _ := store.State(ctx); answer.State != StateRejected {
		t.Fatalf("after a refusal: %+v", answer)
	}
	if _, err := store.Token(ctx); !errors.As(err, new(ErrNoToken)) || !strings.Contains(err.Error(), StateRejected) {
		t.Fatalf("a refused token was used: %v", err)
	}
	second := AdminToken(t, now, "second")
	if _, err := store.Put(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.Rejected(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Token(ctx); err != nil || got != second {
		t.Fatalf("a late refusal of the old token touched the new one: %q %v", got, err)
	}
}

// A token handed over to another process is used here within the re-read
// interval, and at once after a refusal here.
func TestATokenHandedOverElsewhereIsReadAgain(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	here, client := testStore(t, &now)
	there, err := New(client, "test-alarmd", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, second := AdminToken(t, now, "first"), AdminToken(t, now, "second")
	if _, err := there.Put(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got, _ := here.Token(ctx); got != first {
		t.Fatalf("first read %q", got)
	}
	if _, err := there.Put(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got, _ := here.Token(ctx); got != first {
		t.Fatalf("read again before the interval: %q", got)
	}
	now = now.Add(reread)
	if got, _ := here.Token(ctx); got != second {
		t.Fatalf("not read again after the interval: %q", got)
	}
	third := AdminToken(t, now, "third")
	if _, err := there.Put(ctx, third); err != nil {
		t.Fatal(err)
	}
	_ = here.Rejected(ctx, second)
	if got, _ := here.Token(ctx); got != third {
		t.Fatalf("not read again after a refusal: %q", got)
	}
}

// Only a token shaped like the admin's, with its times in order and not yet
// expired, is kept; the key lives as long as the token.
func TestOnlyAnAdminShapedLiveTokenIsKept(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store, client := testStore(t, &now)
	ctx := context.Background()
	payload := func(claims string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".c2ln"
	}
	for name, token := range map[string]string{
		"empty":           "",
		"two parts":       "a.b",
		"not base64":      "a.!!.c",
		"no exp":          payload(`{"iat":1,"rfa":2}`),
		"refresh after":   payload(`{"iat":1,"rfa":5,"exp":4}`),
		"expired":         AdminToken(t, now.Add(-91*24*time.Hour), "old"),
		"over 4096 bytes": strings.Repeat("a", 4097),
	} {
		if _, err := store.Put(ctx, token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s token kept: %v", name, err)
		}
	}
	if _, err := store.Put(ctx, AdminToken(t, now, "live")); err != nil {
		t.Fatal(err)
	}
	ttl := client.PTTL(ctx, "test-alarmd:linkd-token").Val()
	if want := 90 * 24 * time.Hour; ttl > want || ttl < want-time.Minute {
		t.Fatalf("the key lives %v, the token %v", ttl, want)
	}
}

// A store that does not answer fails the call, even while this process
// still holds a token it read before: that token may have been refused by
// another process since.
func TestAStoreThatDoesNotAnswerFailsTheCallInsteadOfTheCachedToken(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store, client := testStore(t, &now)
	ctx := context.Background()
	if _, err := store.Put(ctx, AdminToken(t, now, "first")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Token(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(reread)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Token(ctx); err == nil || got != "" || errors.As(err, new(ErrNoToken)) {
		t.Fatalf("an unanswered store gave token %q, err %v", got, err)
	}
}
