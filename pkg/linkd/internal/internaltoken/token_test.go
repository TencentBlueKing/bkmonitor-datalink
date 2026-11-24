// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package internaltoken

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func header(value string) http.Header { h := http.Header{}; h.Set(HeaderName, value); return h }

func TestClaimsAndHeaderValidation(t *testing.T) {
	now := time.Unix(1800000000, 0)
	toolkit, err := New("test-secret", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		payload jwt.MapClaims
		valid   bool
	}{
		{"Kingeye legacy", jwt.MapClaims{"username": "admin"}, true},
		{"short lived", jwt.MapClaims{"username": "admin", "exp": now.Unix() + 300, "iat": now.Unix()}, true},
		{"missing username", jwt.MapClaims{}, false},
		{"blank username", jwt.MapClaims{"username": "  "}, false},
		{"numeric username", jwt.MapClaims{"username": 1}, false},
		{"expiry boundary", jwt.MapClaims{"username": "admin", "exp": now.Unix()}, false},
		{"epoch expiry", jwt.MapClaims{"username": "admin", "exp": 0}, false},
		{"expired", jwt.MapClaims{"username": "admin", "exp": now.Unix() - 1}, false},
		{"future iat", jwt.MapClaims{"username": "admin", "iat": now.Unix() + 1}, false},
		{"future nbf", jwt.MapClaims{"username": "admin", "nbf": now.Unix() + 1}, false},
		{"nbf boundary", jwt.MapClaims{"username": "admin", "nbf": now.Unix()}, true},
		{"null exp", jwt.MapClaims{"username": "admin", "exp": nil}, false},
		{"string exp", jwt.MapClaims{"username": "admin", "exp": "1800000300"}, false},
		{"null nbf", jwt.MapClaims{"username": "admin", "nbf": nil}, false},
		{"string iat", jwt.MapClaims{"username": "admin", "iat": "1800000000"}, false},
		{"overflow exp", jwt.MapClaims{"username": "admin", "exp": json.Number("1e999")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tc.payload).SignedString([]byte("test-secret"))
			if err != nil {
				t.Fatal(err)
			}
			username, err := toolkit.VerifyHeader(header("Bearer " + raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && username != "admin" {
				t.Fatal(username)
			}
		})
	}
	signed, err := toolkit.Sign("admin")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(signed, "Bearer "), ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"username":"another-user"}`))
	if _, err := toolkit.VerifyHeader(header("Bearer " + strings.Join(parts, "."))); err == nil {
		t.Fatal("modified payload accepted")
	}
	wrong, _ := New("other-secret", nil)
	wrongSignature, _ := wrong.Sign("admin")
	hs512, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{"username": "admin"}).SignedString([]byte("test-secret"))
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"username": "admin"}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	for _, value := range []string{"", "Basic " + strings.TrimPrefix(signed, "Bearer "), "bearer " + strings.TrimPrefix(signed, "Bearer "), signed + " ", signed + "," + signed, "Bearer x.y.z", wrongSignature, "Bearer " + hs512, "Bearer " + none, strings.Repeat("x", MaxHeaderBytes+1)} {
		if _, err := toolkit.VerifyHeader(header(value)); !errors.Is(err, ErrInvalidToken) {
			t.Fatal("invalid header accepted")
		}
	}
	duplicate := header(signed)
	duplicate.Add(HeaderName, signed)
	if _, err := toolkit.VerifyHeader(duplicate); err == nil {
		t.Fatal("duplicate accepted")
	}
	for _, username := range []string{"", "  ", strings.Repeat("x", MaxHeaderBytes)} {
		if _, err := toolkit.Sign(username); err == nil {
			t.Fatal("invalid username signed")
		}
	}
	if _, err := New(" ", nil); err == nil {
		t.Fatal("empty key accepted")
	}
}

func TestInteropAndFreshExpiry(t *testing.T) {
	data, err := os.ReadFile("testdata/interop.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Secret   string `json:"secret_key"`
		Username string `json:"username"`
		Unix     int64  `json:"unix"`
		Legacy   string `json:"python_legacy"`
		Timed    string `json:"python_timed"`
		GoTimed  string `json:"go_timed"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(fixture.Unix, 0)
	toolkit, _ := New(fixture.Secret, func() time.Time { return now })
	for _, raw := range []string{fixture.Legacy, fixture.Timed} {
		username, err := toolkit.VerifyHeader(header("Bearer " + raw))
		if err != nil || username != fixture.Username {
			t.Fatalf("interop: %v", err)
		}
	}
	signed, err := toolkit.Sign(fixture.Username)
	if err != nil || signed != "Bearer "+fixture.GoTimed {
		t.Fatal("Go signing differs from PyJWT fixture")
	}
	now = now.Add(Lifetime)
	if _, err := toolkit.VerifyHeader(header(signed)); err == nil {
		t.Fatal("expired token accepted")
	}
	fresh, err := toolkit.Sign(fixture.Username)
	if err != nil || fresh == signed {
		t.Fatal("token not renewed")
	}
	if _, err := toolkit.VerifyHeader(header(fresh)); err != nil {
		t.Fatal(err)
	}
	if _, err := toolkit.VerifyHeader(header("Bearer " + fixture.Legacy)); err != nil {
		t.Fatal("legacy compatibility lost")
	}
}

func TestConcurrentToolkitAndIdentity(t *testing.T) {
	toolkit, _ := New("test-secret", nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			signed, err := toolkit.Sign("admin")
			if err != nil {
				t.Error(err)
				return
			}
			username, err := toolkit.VerifyHeader(header(signed))
			if err != nil || username != "admin" {
				t.Error("concurrent authentication failed")
			}
		})
	}
	wg.Wait()
	if Username(t.Context()) != "" || Username(WithUsername(t.Context(), "caller")) != "caller" {
		t.Fatal("identity context")
	}
}
