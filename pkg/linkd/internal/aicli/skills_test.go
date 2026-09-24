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

func TestSkillsInstallAllAgentsAndScopes(t *testing.T) {
	for _, scope := range []string{"user", "project"} {
		for _, agent := range []string{"codex", "claude-code", "cursor", "gemini-cli", "all"} {
			t.Run(scope+"/"+agent, func(t *testing.T) {
				home := t.TempDir()
				project := t.TempDir()
				t.Setenv("HOME", home)
				args := []string{"skills", "install", "--agent", agent, "--scope", scope}
				base := home
				if scope == "project" {
					args = append(args, "--project-dir", project)
					base = project
				}
				code, out, errOut := runCLI(t, "", append(args, "--dry-run")...)
				if code != 0 || !strings.Contains(out, `"state":"missing"`) {
					t.Fatalf("dry run %s %s", out, errOut)
				}
				entries, err := os.ReadDir(base)
				if err != nil || len(entries) != 0 {
					t.Fatal("dry run wrote files")
				}
				code, _, errOut = runCLI(t, "", args...)
				if code != 0 {
					t.Fatal(errOut)
				}
				targets, err := skillTargets(agent, scope, map[bool]string{true: project}[scope == "project"])
				if err != nil {
					t.Fatal(err)
				}
				wantCount := 1
				if agent == "all" {
					wantCount = 2
				}
				if len(targets) != wantCount {
					t.Fatalf("shared directory not deduplicated: %v", targets)
				}
				files, err := bundle()
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range targets {
					for name, expected := range files {
						actual, err := os.ReadFile(filepath.Join(target.Path, filepath.FromSlash(name)))
						if err != nil || string(actual) != string(expected) {
							t.Fatalf("incomplete bundle %s %v", name, err)
						}
					}
				}
				code, out, errOut = runCLI(t, "", args...)
				if code != 0 || strings.Contains(out, `"state":"installed"`) || !strings.Contains(out, `"state":"current"`) {
					t.Fatalf("not idempotent %s %s", out, errOut)
				}
			})
		}
	}
}

func TestSkillsConflictForceAndSymlinks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	args := []string{"skills", "install", "--agent", "all"}
	if code, _, errOut := runCLI(t, "", args...); code != 0 {
		t.Fatal(errOut)
	}
	path := filepath.Join(root, ".claude", "skills", "linkd-ops", "SKILL.md")
	if err := os.WriteFile(path, []byte("local customized content"), 0600); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(filepath.Dir(path), "my-notes.md")
	if err := os.WriteFile(extra, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, "", args...)
	if code == 0 || errorCode(t, errOut) != "install_conflict" {
		t.Fatal("overwrote custom skill", errOut)
	}
	code, _, errOut = runCLI(t, "", append(args, "--force")...)
	if code != 0 {
		t.Fatal(errOut)
	}
	if raw, err := os.ReadFile(extra); err != nil || string(raw) != "preserve" { //nolint:gosec // extra 是 t.TempDir 内刻意保留的用户文件。
		t.Fatal("unmanaged file deleted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "", append(args, "--force")...)
	if code == 0 || errorCode(t, errOut) != "install_error" {
		t.Fatal("followed symlink", errOut)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "untouched" { //nolint:gosec // outside 是 t.TempDir 内用于验证链接保护的哨兵文件。
		t.Fatal("symlink target changed")
	}
}

func TestSkillsRequireExplicitAgentAndValidateScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{{"skills", "install"}, {"skills", "install", "--agent", "unknown"}, {"skills", "install", "--agent", "codex", "--scope", "unknown"}, {"skills", "install", "--agent", "codex", "--project-dir", "."}} {
		code, _, errOut := runCLI(t, "", args...)
		if code == 0 || errorCode(t, errOut) != "invalid_argument" {
			t.Fatal("invalid install accepted", errOut)
		}
	}
	code, out, errOut := runCLI(t, "", "skills", "list")
	if code != 0 || !strings.Contains(out, "linkd-ops") {
		t.Fatalf("offline list %s %s", out, errOut)
	}
}

func TestSkillsResumeIncompleteInstallWithoutOverwritingChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	args := []string{"skills", "install", "--agent", "codex"}
	if code, _, errOut := runCLI(t, "", args...); code != 0 {
		t.Fatal(errOut)
	}
	targets, err := skillTargets("codex", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(targets[0].Path, "references", "diagnostics.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "", args...); code != 0 {
		t.Fatal("incomplete install did not resume", errOut)
	}
	if _, err := os.Stat(missing); err != nil {
		t.Fatal(err)
	}
}
