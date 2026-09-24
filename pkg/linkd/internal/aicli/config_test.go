// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigProfilesAndPrivatePersistence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "linkd-cli", "config.yaml")
	for _, name := range []string{"test", "prod"} {
		code, out, errOut := runCLI(t, "super-private-password\n", "config", "set", name, "--url", "https://console.example/linkd/", "--username", "operator", "--password-stdin")
		if code != 0 || strings.Contains(out+errOut, "super-private-password") {
			t.Fatalf("unsafe config set %d %s %s", code, out, errOut)
		}
	}
	for name, mode := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions %s %v %v", name, info, err)
		}
	}
	cfg, err := readSettings(path)
	if err != nil || len(cfg.Profiles) != 2 || cfg.CurrentProfile != "test" || cfg.Profiles["test"].Password != "super-private-password" || cfg.Profiles["test"].TimeoutSeconds != 30 {
		t.Fatalf("saved config incorrect: %v", err)
	}
	code, _, errOut := runCLI(t, "", "config", "use", "prod")
	if code != 0 {
		t.Fatal(errOut)
	}
	selected, p, err := (&options{}).selected()
	if err != nil || selected != "prod" || p.URL != "https://console.example/linkd" {
		t.Fatalf("selection %s %v", selected, err)
	}
	selected, _, err = (&options{profile: "test"}).selected()
	if err != nil || selected != "test" {
		t.Fatal("explicit selection failed")
	}
	code, out, errOut := runCLI(t, "", "config", "show")
	if code != 0 || !strings.Contains(out, `"profile":"prod"`) || strings.Contains(out, "super-private-password") {
		t.Fatalf("show %d %s %s", code, out, errOut)
	}
	code, _, errOut = runCLI(t, "", "config", "delete", "prod")
	if code != 0 {
		t.Fatal(errOut)
	}
	cfg, err = readSettings(path)
	if err != nil || cfg.CurrentProfile != "" || len(cfg.Profiles) != 1 {
		t.Fatal("delete incorrectly selected another environment", err)
	}
	if _, _, err = (&options{}).selected(); err == nil {
		t.Fatal("missing active profile silently fell back")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("temporary credential files left behind")
	}
}

func TestConfigDefaultLocationAndOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	path, err := configLocation("")
	if err != nil || path != filepath.Join(home, ".config", "linkd-cli", "config.yaml") {
		t.Fatalf("wrong default %s %v", path, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := configLocation(""); err == nil {
		t.Fatal("relative XDG accepted")
	}
	path, err = configLocation("explicit.yaml")
	if err != nil || path != "explicit.yaml" {
		t.Fatal("explicit path not honored")
	}
}

func TestConfigRejectsUnsafeOrBrokenInputs(t *testing.T) {
	base := profile{URL: "https://example.com/monitor", Username: "user", Password: "valid-secret", TimeoutSeconds: 30}
	for _, address := range []string{"http://user:pass@example.com", "http://example.com?a=1", "http://example.com#x", "file:///tmp/x", "http://example.com/../x", "http://example.com/%2e%2e", "http://example.com//x"} {
		p := base
		p.URL = address
		if err := validateProfile(p); err == nil {
			t.Errorf("unsafe URL accepted %s", address)
		}
	}
	for _, modify := range []func(*profile){func(p *profile) { p.Username = "x:y" }, func(p *profile) { p.Password = "a\nb" }, func(p *profile) { p.TimeoutSeconds = 0 }, func(p *profile) { p.TimeoutSeconds = 121 }} {
		p := base
		modify(&p)
		if err := validateProfile(p); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	for _, raw := range []string{"password: hidden-secret", "profiles: [hidden-secret", "profiles: {}\n---\nprofiles: {}", "current_profile: absent\nprofiles: {}", "profiles: {}\nallow_write: true"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := runCLI(t, "", "--config", path, "config", "list")
		if code == 0 || out != "" || strings.Contains(errOut, "hidden-secret") {
			t.Fatalf("unsafe parse failure %d %s %s", code, out, errOut)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("profiles: {}"), 0644); err != nil { //nolint:gosec // 故意创建不安全权限，验证客户端会拒绝读取。
		t.Fatal(err)
	}
	if _, err := readSettings(path); err == nil {
		t.Fatal("public credential file accepted")
	}
	link := filepath.Join(t.TempDir(), "symlink.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readSettings(link); err == nil {
		t.Fatal("credential symlink accepted")
	}
}

func TestConfigFailedUpdatePreservesExistingProfiles(t *testing.T) {
	path := testConfig(t, "https://example.com")
	before, err := os.ReadFile(path) //nolint:gosec // 仅检查 t.TempDir 中测试配置未被失败更新覆盖。
	if err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, "new-secret", "--config", path, "config", "set", "test", "--url", "https://different.example", "--username", "u", "--password-stdin", "--timeout-seconds", "121")
	if code == 0 || strings.Contains(errOut, "new-secret") {
		t.Fatal("bad configuration accepted")
	}
	after, err := os.ReadFile(path) //nolint:gosec // 仅检查 t.TempDir 中测试配置未被失败更新覆盖。
	if err != nil || string(before) != string(after) {
		t.Fatal("failed update changed old config")
	}
	code, out, errOut := runCLI(t, "", "--password=accidental-secret", "version")
	if code == 0 || strings.Contains(out+errOut, "accidental-secret") {
		t.Fatal("unknown flag leaked value")
	}
}
