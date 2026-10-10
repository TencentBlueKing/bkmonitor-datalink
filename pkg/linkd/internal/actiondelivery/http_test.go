// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"linkd/internal/internaltoken"
)

func TestActionHTTPSenderRequiresMatchingCeleryReceipt(t *testing.T) {
	q := actionTask(t, "tenant", 1).Request
	ok, _ := json.Marshal(confirmed(q))
	invisible := confirmed(q)
	invisible.TaskID = ""
	notVisible, _ := json.Marshal(invisible)
	foreign := confirmed(q)
	foreign.TenantID = "foreign"
	foreignJSON, _ := json.Marshal(foreign)
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
		retry      bool
	}{
		{"queued", 200, string(ok), "", false}, {"missing task", 200, string(notVisible), "response_invalid", false},
		{"foreign receipt", 200, string(foreignJSON), "response_invalid", false}, {"empty", 200, `{}`, "response_invalid", false},
		{"extra JSON", 200, string(ok) + `{}`, "response_invalid", false}, {"large", 200, strings.Repeat("x", 65537), "response_too_large", false},
		{"unauthorized", 401, "private-token", "remote_unauthorized", false}, {"identity conflict", 409, "private", "identity_conflict", false},
		{"unavailable", 503, "private", "remote_unavailable", true}, {"not confirmed accepted", 202, string(ok), "remote_rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier, err := internaltoken.New("secret", nil)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				username, err := verifier.VerifyHeader(r.Header)
				if err != nil || username != "admin" || r.Method != "POST" || len(r.Header.Values("X-Bk-Tenant-Id")) != 0 || r.Header.Get("Authorization") != "" {
					t.Error("protocol or authentication mismatch")
				}
				var got Request
				if json.NewDecoder(r.Body).Decode(&got) != nil || got.Hash() != q.Hash() {
					t.Error("request not frozen")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			sender, e := NewHTTPSender()
			if e != nil {
				t.Fatal(e)
			}
			defer sender.Close()
			ack, e := sender.Send(t.Context(), Destination{Endpoint: server.URL, JWTSecretKey: "secret"}, q)
			if tc.code == "" {
				if e != nil || ack.ValidateFor(q) != nil {
					t.Fatal(e)
				}
				return
			}
			var f Failure
			if !errors.As(e, &f) || f.Code != tc.code || f.Retryable != tc.retry || strings.Contains(e.Error(), "private") {
				t.Fatal(f, e)
			}
		})
	}
}

func TestActionHTTPSenderSignsEachAttempt(t *testing.T) {
	q := actionTask(t, "tenant", 1).Request
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC).Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	verifier, err := internaltoken.New("private-signing-key", now)
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int64
	headers := make(chan http.Header, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, err := verifier.VerifyHeader(r.Header)
		if err != nil || username != "linkd" {
			t.Error("invalid freshly signed identity")
		}
		var got Request
		if json.NewDecoder(r.Body).Decode(&got) != nil || got.Hash() != q.Hash() {
			t.Error("signing changed frozen action")
		}
		headers <- r.Header.Clone()
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if err := json.NewEncoder(w).Encode(confirmed(q)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	sender, err := NewHTTPSender()
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	sender.now = now
	destination := Destination{Endpoint: server.URL, JWTSecretKey: "private-signing-key", JWTUsername: "linkd"}
	_, err = sender.Send(t.Context(), destination, q)
	var failure Failure
	if !errors.As(err, &failure) || !failure.Retryable {
		t.Fatal("first attempt must be retryable", err)
	}
	first := <-headers
	clock.Add(int64(internaltoken.Lifetime/time.Second) + 1)
	if _, err := verifier.VerifyHeader(first); err == nil {
		t.Fatal("first JWT must expire before retry")
	}
	if receipt, err := sender.Send(t.Context(), destination, q); err != nil || receipt.ValidateFor(q) != nil {
		t.Fatal("retry did not use a fresh JWT", err)
	}
	second := <-headers
	if first.Get(internaltoken.HeaderName) == second.Get(internaltoken.HeaderName) {
		t.Fatal("retry reused expired JWT")
	}
	token, err := jwt.Parse(strings.TrimPrefix(second.Get(internaltoken.HeaderName), "Bearer "), func(*jwt.Token) (any, error) {
		return []byte(destination.JWTSecretKey), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(now))
	if err != nil {
		t.Fatal("JWT is not compatible with HS256 verification")
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["username"] != "linkd" || claims["iat"] != float64(clock.Load()) || claims["exp"] != float64(clock.Load()+int64(internaltoken.Lifetime/time.Second)) {
		t.Fatal("JWT identity or lifetime differs")
	}
}

func TestActionHTTPSenderRejectsInvalidSigningCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid credentials reached receiver")
	}))
	defer server.Close()
	sender, err := NewHTTPSender()
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	for _, d := range []Destination{
		{JWTSecretKey: ""}, {JWTSecretKey: " \t\n"}, {JWTSecretKey: strings.Repeat("x", 16<<10+1)},
		{JWTSecretKey: "private-key", JWTUsername: " \t"}, {JWTSecretKey: "private-key", JWTUsername: strings.Repeat("x", 257)},
	} {
		d.Endpoint = server.URL
		_, err := sender.Send(t.Context(), d, actionTask(t, "tenant", 1).Request)
		var failure Failure
		if !errors.As(err, &failure) || failure.Code != "target_unavailable" || failure.Retryable || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid signing credentials accepted or leaked", err)
		}
	}
}

func TestActionHTTPSenderSupportsConcurrentSigning(t *testing.T) {
	verifier, err := internaltoken.New("shared-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := actionTask(t, "tenant", 1).Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if username, err := verifier.VerifyHeader(r.Header); err != nil || username != "admin" {
			t.Error("concurrent signing failed")
		}
		if err := json.NewEncoder(w).Encode(confirmed(q)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	sender, err := NewHTTPSender()
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, err := sender.Send(t.Context(), Destination{Endpoint: server.URL, JWTSecretKey: "shared-key"}, q); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
}

func TestActionHTTPSenderDoesNotFollowRedirectsOrLeakCredentials(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	sender, e := NewHTTPSender()
	if e != nil {
		t.Fatal(e)
	}
	defer sender.Close()
	q := actionTask(t, "tenant", 1).Request
	if _, e = sender.Send(t.Context(), Destination{Endpoint: redirect.URL, JWTSecretKey: "secret"}, q); e == nil || followed.Load() {
		t.Fatal("redirect followed", e)
	}
	for _, endpoint := range []string{"file:///private", target.URL + "?token=secret", target.URL + "#fragment", "http://user:secret@localhost/"} {
		if _, e = sender.Send(t.Context(), Destination{Endpoint: endpoint, JWTSecretKey: "secret"}, q); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal("invalid destination accepted or leaked", e)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = sender.Send(ctx, Destination{Endpoint: target.URL, JWTSecretKey: "secret"}, q); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
