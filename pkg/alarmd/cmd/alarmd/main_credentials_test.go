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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
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

// The link's sets are read through a connection this process already
// holds, copied by value. When that connection took its password from a
// file, the copy carries the password: references are resolved at load,
// before anything is copied - including a platform cache inheriting the
// top-level redis.
func TestTheAdoptedLinkdConnectionCarriesAPasswordReadFromAFile(t *testing.T) {
	for _, name := range []string{"ALARMD_CLI_ADMIN_KEY", "ALARMD_LINKD_CONSOLE_USERNAME", "ALARMD_LINKD_CONSOLE_PASSWORD"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	contents := strings.Replace(validGoAccessApplicationYAML(), "redis:\n  address: 127.0.0.1:6379\n",
		"redis:\n  address: 127.0.0.1:6379\n  password_file: "+write("redis-password", "test-password-1\n")+"\n", 1) +
		"  linkd:\n    console_url: http://console.example.test/local-api\n" +
		"    username_file: " + write("console-username", "reader\n") + "\n" +
		"    password_file: " + write("console-password", "test-password-3\n") + "\n" +
		"platform_cache:\n  dynamic_group_key_prefix: 'groups:'\n  target_group:\n    mode: standalone\n    address: 192.0.2.10:6379\n" +
		"    password_file: " + write("groups-password", "test-password-2\n") + "\n"
	path := write("alarmd.yaml", contents)
	cfg, err := config.LoadWithCredentials(path, nil, config.CredentialsForRoles)
	if err != nil {
		t.Fatal(err)
	}
	discover := func(target openalerts.TargetBinding) discoverLinkdTarget {
		return func(_ context.Context, options openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
			if options.Username != "reader" || options.Password != "test-password-3" {
				return openalerts.TargetBinding{}, errors.New("the Console was asked without the credentials read from files")
			}
			return target, nil
		}
	}
	for _, test := range []struct{ address, password string }{
		{"192.0.2.10:6379", "test-password-2"},
		// The top-level redis, also held through the CMDB cache that
		// inherited it.
		{"127.0.0.1:6379", "test-password-1"},
	} {
		adopted, _ := adoptLinkdLocation(context.Background(), cfg, discover(openalerts.TargetBinding{
			KeyPrefix: "hook:open", Address: test.address, Database: 3}))
		got := adopted.PhaseTwo.Linkd.Connection
		if got == nil || got.Address != test.address || got.Password != test.password || got.PasswordFile != "" || got.DB != 3 {
			t.Fatalf("target at %s: adopted %+v, want its held connection with the password read from the file", test.address, got)
		}
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
