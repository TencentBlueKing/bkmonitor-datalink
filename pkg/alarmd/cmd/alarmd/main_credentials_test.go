// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// credentialApplicationYAML is the application configuration with the
// top-level redis taking its password from a file, plus extra lines under
// redis.
func credentialApplicationYAML(t *testing.T, redisLines ...string) string {
	t.Helper()
	const anchor = "redis:\n  address: 127.0.0.1:6379\n"
	base := validGoAccessApplicationYAML()
	if !strings.Contains(base, anchor) {
		t.Fatal("the application configuration no longer states redis as expected")
	}
	extra := ""
	for _, line := range redisLines {
		extra += "  " + line + "\n"
	}
	secret := filepath.Join(t.TempDir(), "linkd-jwt-secret")
	if err := os.WriteFile(secret, []byte("example-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base = strings.Replace(base, applicationLinkdJWTSecretFile, secret, 1)
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte(strings.Replace(base, anchor, anchor+extra, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --check-config opens no credential file: release checks run it where no
// Secret is mounted. --resolve-credentials reads them and reports the one
// that fails by its field and reason. Neither prints a credential.
func TestCheckConfigOpensCredentialFilesOnlyWhenAskedTo(t *testing.T) {
	for _, name := range []string{"ALARMD_CLI_ADMIN_KEY", "ALARMD_LINKD_CONSOLE_USERNAME", "ALARMD_LINKD_CONSOLE_PASSWORD"} {
		t.Setenv(name, "")
	}
	absent := credentialApplicationYAML(t, "password_file: "+filepath.Join(t.TempDir(), "absent"))
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--check-config", "--config", absent}, &stdout, &stderr); code != 0 || stdout.Len() == 0 {
		t.Fatalf("--check-config with an unmounted reference: code %d, stderr %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := run(context.Background(), []string{"--check-config", "--resolve-credentials", "--config", absent}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "redis.password_file") || !strings.Contains(stderr.String(), "not_found") {
		t.Fatalf("--resolve-credentials with an absent file: code %d, stderr %q", code, stderr.String())
	}

	secretFile := filepath.Join(t.TempDir(), "redis-password")
	if err := os.WriteFile(secretFile, []byte("test-password-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	present := credentialApplicationYAML(t, "password_file: "+secretFile)
	for _, args := range [][]string{{"--check-config"}, {"--check-config", "--resolve-credentials"}} {
		stdout.Reset()
		stderr.Reset()
		code := run(context.Background(), append(args, "--config", present), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("%v: code %d, stderr %q", args, code, stderr.String())
		}
		if strings.Contains(stdout.String()+stderr.String(), "test-password-1") {
			t.Fatalf("%v printed the credential", args)
		}
	}
}

// The one-source rule needs no file, so --check-config enforces it.
func TestCheckConfigRefusesACredentialWithTwoSources(t *testing.T) {
	path := credentialApplicationYAML(t, "password: test-password-1", "password_file: /etc/alarmd/secrets/redis-password")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "redis.password and redis.password_file are both set") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "test-password-1") {
		t.Fatal("the refusal printed the credential")
	}
}

func TestResolveCredentialsGoesWithCheckConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--resolve-credentials", "--config", "/missing/config.yaml"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--resolve-credentials is used with --check-config") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
}

// The link's control plane is signed with a secret read from a file: the
// path is resolved at load, the client built from the loaded configuration
// signs every search with that secret (HS256 over header.claims), and the
// file's value is never left in the path field.
func TestTheLinkdControlPlaneSecretIsReadFromItsFile(t *testing.T) {
	t.Setenv("ALARMD_CLI_ADMIN_KEY", "")
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	tokens := make(chan string, 1)
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case tokens <- r.Header.Get("Internal-Token"):
		default:
		}
		_, _ = w.Write([]byte(`{"items":[],"next":""}`))
	}))
	defer plane.Close()
	contents := strings.Replace(validGoAccessApplicationYAML(), applicationLinkdControlPlaneYAML,
		"  linkd:\n    control_plane:\n      url: "+plane.URL+"\n"+
			"      jwt_secret_file: "+write("linkd-secret", "test-secret-3\n")+"\n", 1)
	cfg, err := config.LoadWithCredentials(write("alarmd.yaml", contents), nil, config.CredentialsForRoles)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PhaseTwo.Linkd.ControlPlane.JWTSecret; got != "test-secret-3" {
		t.Fatalf("secret length %d, want the file's value", len(got))
	}
	client, err := newLinkdManage(cfg, time.Now)
	if err != nil || client == nil {
		t.Fatalf("client %v (%v)", client, err)
	}
	if _, err := client.Search(context.Background(), "system", json.RawMessage(`{"match_all":{}}`), "", 1); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(<-tokens, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	mac := hmac.New(sha256.New, []byte("test-secret-3"))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("the search was not signed with the secret read from the file")
	}
}

// The file-free check catches the credential itself written where its path
// belongs, and says so without printing it.
func TestCheckConfigRefusesACredentialWrittenAsItsPathWithoutPrintingIt(t *testing.T) {
	const token = "test-password-placeholder-00000000000000"
	path := credentialApplicationYAML(t, "password_file: "+token)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "redis.password_file") || !strings.Contains(stderr.String(), "invalid_path") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), token) {
		t.Fatal("the refusal printed the value")
	}
}
