// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type skillRun struct {
	code int
	out  map[string]any
}

func runSkills(t *testing.T, home, workdir string, args ...string) skillRun {
	t.Helper()
	var stdout bytes.Buffer
	a := New(strings.NewReader(""), &stdout, io.Discard, "test")
	a.home, a.workdir = home, workdir
	code := a.Run(append([]string{"skills"}, args...))
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not JSON: %q", stdout.String())
	}
	return skillRun{code, out}
}

func (r skillRun) targets(t *testing.T) []map[string]any {
	t.Helper()
	var targets []map[string]any
	for _, target := range r.out["result"].(map[string]any)["targets"].([]any) {
		targets = append(targets, target.(map[string]any))
	}
	return targets
}

func shownSkill(t *testing.T) string {
	t.Helper()
	return runSkills(t, t.TempDir(), t.TempDir(), "show").out["result"].(map[string]any)["text"].(string)
}

// The default installs the built-in text, byte for byte the one show
// prints, into the universal user directory; a second install changes
// nothing, not even the file's time.
func TestSkillsInstallWritesTheShownTextAndThenLeavesItAlone(t *testing.T) {
	home := t.TempDir()
	run := runSkills(t, home, t.TempDir(), "install")
	path := filepath.Join(home, ".agents", "skills", "alarmd-cli", "SKILL.md")
	written, err := os.ReadFile(path)
	if err != nil || run.code != 0 || string(written) != shownSkill(t) {
		t.Fatalf("exit %d, file %v, want the shown text at %s", run.code, err, path)
	}
	if target := run.targets(t)[0]; target["status"] != "installed" || target["path"] != path {
		t.Fatalf("target %v", target)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	again := runSkills(t, home, t.TempDir(), "install")
	info, _ := os.Stat(path)
	if again.code != 0 || again.targets(t)[0]["status"] != "unchanged" || !info.ModTime().Equal(old) {
		t.Fatalf("second install exit %d %v, mtime %v", again.code, again.targets(t), info.ModTime())
	}
}

// A different SKILL.md is a conflict: left alone, exit 3 with the target
// named in the limitations and the same install with --force as next_call;
// with --force it is replaced and the directory's other files stay.
func TestADifferentSkillIsAConflictUntilForced(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "skills", "alarmd-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	for name, text := range map[string]string{"SKILL.md": "mine", "notes.md": "keep"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := runSkills(t, home, t.TempDir(), "install", "--tool", "claude")
	kept, _ := os.ReadFile(path)
	limitations := run.out["evidence"].(map[string]any)["limitations"].([]any)
	next := run.out["next_call"].([]any)
	if run.code != 3 || run.out["status"] != "partial" || string(kept) != "mine" || run.targets(t)[0]["status"] != "conflict" {
		t.Fatalf("exit %d %v, file %q", run.code, run.out, kept)
	}
	if len(limitations) != 1 || !strings.HasPrefix(limitations[0].(string), "target_conflict: "+path+": ") {
		t.Fatalf("limitations %v, want the conflict named with its path", limitations)
	}
	if len(next) != 1 || next[0].(map[string]any)["command"] != "alarmd-cli skills install --tool claude --force" {
		t.Fatalf("next_call %v, want the same install with --force", next)
	}
	forced := runSkills(t, home, t.TempDir(), "install", "--tool", "claude", "--force")
	replaced, _ := os.ReadFile(path)
	notes, _ := os.ReadFile(filepath.Join(dir, "notes.md"))
	if forced.code != 0 || forced.targets(t)[0]["status"] != "updated" || string(replaced) != shownSkill(t) || string(notes) != "keep" {
		t.Fatalf("forced exit %d %v, notes %q", forced.code, forced.targets(t), notes)
	}
}

// all names every tool once per path, with the tools sharing a path
// together; project scope and --directory resolve as the table and the
// argument say.
func TestSkillsTargetsFollowTheToolsScopeAndDirectory(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	run := runSkills(t, home, work, "install", "--tool", "all", "--dry-run")
	paths := map[string]string{}
	for _, target := range run.targets(t) {
		var tools []string
		for _, tool := range target["tools"].([]any) {
			tools = append(tools, tool.(string))
		}
		paths[strings.TrimPrefix(target["path"].(string), home+"/")] = strings.Join(tools, ",")
	}
	want := map[string]string{
		".agents/skills/alarmd-cli/SKILL.md": "universal,codex", ".claude/skills/alarmd-cli/SKILL.md": "claude-code",
		".cursor/skills/alarmd-cli/SKILL.md": "cursor", ".copilot/skills/alarmd-cli/SKILL.md": "copilot",
		".gemini/skills/alarmd-cli/SKILL.md": "gemini-cli", ".config/opencode/skills/alarmd-cli/SKILL.md": "opencode",
	}
	if len(paths) != len(want) {
		t.Fatalf("targets %v, want %v", paths, want)
	}
	for path, tools := range want {
		if paths[path] != tools {
			t.Errorf("%s: tools %q, want %q", path, paths[path], tools)
		}
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--tool", "copilot", "--scope", "project"}, filepath.Join(work, ".github/skills/alarmd-cli/SKILL.md")},
		{[]string{"--tool", "opencode", "--scope=project"}, filepath.Join(work, ".opencode/skills/alarmd-cli/SKILL.md")},
		{[]string{"--directory", "custom/skills"}, filepath.Join(work, "custom/skills/alarmd-cli/SKILL.md")},
		{[]string{"--directory", filepath.Join(home, "abs")}, filepath.Join(home, "abs/alarmd-cli/SKILL.md")},
	} {
		run := runSkills(t, home, work, append([]string{"install", "--dry-run"}, c.args...)...)
		if got := run.targets(t)[0]["path"]; got != c.want {
			t.Errorf("%v: path %v, want %s", c.args, got, c.want)
		}
	}
}

// A dry run writes nothing and previews what the run would do, a conflict
// included.
func TestADryRunWritesNothingAndPreviewsConflicts(t *testing.T) {
	home := t.TempDir()
	run := runSkills(t, home, t.TempDir(), "install", "--dry-run")
	if run.code != 0 || run.targets(t)[0]["status"] != "would_install" {
		t.Fatalf("exit %d %v", run.code, run.targets(t))
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("a dry run created %v", err)
	}
	dir := filepath.Join(home, ".agents", "skills", "alarmd-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if run := runSkills(t, home, t.TempDir(), "install", "--dry-run"); run.code != 3 || run.targets(t)[0]["status"] != "conflict" {
		t.Fatalf("dry run over a conflict: exit %d %v", run.code, run.targets(t))
	}
	if run := runSkills(t, home, t.TempDir(), "install", "--dry-run", "--force"); run.code != 0 || run.targets(t)[0]["status"] != "would_update" {
		t.Fatalf("forced dry run: exit %d %v", run.code, run.targets(t))
	}
}

// A symbolic link for the skill directory or the file is refused, with
// --force too, and what it points at is left alone.
func TestASymbolicLinkIsRefusedEvenWithForce(t *testing.T) {
	for _, linked := range []string{"directory", "file"} {
		t.Run(linked, func(t *testing.T) {
			home, elsewhere := t.TempDir(), t.TempDir()
			outside := filepath.Join(elsewhere, "SKILL.md")
			if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			skills := filepath.Join(home, ".agents", "skills")
			if err := os.MkdirAll(skills, 0o755); err != nil {
				t.Fatal(err)
			}
			if linked == "directory" {
				if err := os.Symlink(elsewhere, filepath.Join(skills, "alarmd-cli")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(skills, "alarmd-cli"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(skills, "alarmd-cli", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			}
			run := runSkills(t, home, t.TempDir(), "install", "--force")
			kept, _ := os.ReadFile(outside)
			limitations := run.out["evidence"].(map[string]any)["limitations"].([]any)
			if run.code != 3 || string(kept) != "outside" || len(limitations) != 1 || !strings.Contains(limitations[0].(string), "symbolic link") {
				t.Fatalf("exit %d %v, outside %q", run.code, run.out, kept)
			}
		})
	}
}

// An unknown tool or scope, or a stray argument, is invalid input (exit 2)
// and writes nothing.
func TestInvalidSkillsInputIsExitTwoAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"install", "--tool", "notepad"}, {"install", "--scope", "global"}, {"install", "--tool"},
		{"install", "extra"}, {"install", "--force=yes"}, {"install", "--scope", "user", "--scope", "project"},
		{"show", "x"}, {"list", "x"}, {"uninstall"}, {},
	} {
		if run := runSkills(t, home, t.TempDir(), args...); run.code != 2 || run.out["status"] != "error" {
			t.Errorf("%v: exit %d %v, want 2", args, run.code, run.out)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("invalid input wrote %v", entries)
	}
}

// A directory that cannot be written is a write failure (exit 1), named.
func TestAnUnwritableDirectoryIsExitOne(t *testing.T) {
	home := t.TempDir()
	blocked := filepath.Join(home, "blocked")
	if err := os.WriteFile(blocked, []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := runSkills(t, home, t.TempDir(), "install", "--directory", filepath.Join(blocked, "skills"))
	if run.code != 1 || run.targets(t)[0]["status"] != "failed" {
		t.Fatalf("exit %d %v", run.code, run.out)
	}
	if limitations := run.out["evidence"].(map[string]any)["limitations"].([]any); len(limitations) != 1 || !strings.HasPrefix(limitations[0].(string), "write_failed: ") {
		t.Fatalf("limitations %v, want the failed write named", limitations)
	}
}

// Every command and flag the skill tells an agent to use is one the CLI
// has, so renaming one without the skill fails here.
func TestTheSkillNamesOnlyCommandsAndFlagsTheCLIHas(t *testing.T) {
	var help bytes.Buffer
	New(strings.NewReader(""), &help, io.Discard, "test").Run([]string{"--help"})
	guide := help.String()
	text := string(skillText)
	commands := regexp.MustCompile(`alarmd-cli ((?:auth |profile |skills )?[a-z]+)`).FindAllStringSubmatch(text, -1)
	if len(commands) < 8 {
		t.Fatalf("found %d commands in the skill", len(commands))
	}
	for _, command := range commands {
		if !regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(command[1]) + `\b`).MatchString(guide) {
			t.Errorf("the skill names %q, which the CLI's command table does not have", command[1])
		}
	}
	for _, flag := range regexp.MustCompile(`--[a-z][a-z-]+`).FindAllString(text, -1) {
		if !strings.Contains(guide, flag) {
			t.Errorf("the skill names the flag %s, which the CLI's guide does not have", flag)
		}
	}
	for _, field := range []string{"meta.result_file", "next_call", "evidence.limitations", "effect"} {
		if !strings.Contains(text, field) {
			t.Errorf("the skill no longer tells the agent about %s", field)
		}
	}
}

// skills reads no profile and calls no server.
func TestSkillsReadNoProfileAndCallNoServer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	t.Setenv("ALARMD_CLI_CONFIG_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("HTTPS_PROXY", server.URL)
	t.Setenv("HTTP_PROXY", server.URL)
	home := t.TempDir()
	for _, args := range [][]string{{"list"}, {"show"}, {"install", "--tool", "all"}} {
		if run := runSkills(t, home, t.TempDir(), args...); run.code != 0 {
			t.Fatalf("%v: exit %d %v", args, run.code, run.out)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("skills made %d network calls", calls.Load())
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("ALARMD_CLI_CONFIG_DIR"))); !os.IsNotExist(err) {
		t.Fatal("skills created the configuration directory")
	}
}

// A conflict --force cannot resolve gets no next_call: --force over a
// symbolic link is still a conflict, with the limitation saying why. With a
// differing file beside it, next_call offers --force, and never repeats it
// or the dry run.
func TestNextCallOffersForceOnlyWhereItHelps(t *testing.T) {
	home, elsewhere := t.TempDir(), t.TempDir()
	outside := filepath.Join(elsewhere, "SKILL.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(home, ".agents", "skills", "alarmd-cli")
	differing := filepath.Join(home, ".claude", "skills", "alarmd-cli")
	for _, dir := range []string{linked, differing} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(linked, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(differing, "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := runSkills(t, home, t.TempDir(), "install", "--force")
	if run.code != 3 || len(run.out["next_call"].([]any)) != 0 || len(run.out["evidence"].(map[string]any)["limitations"].([]any)) != 1 {
		t.Fatalf("--force over a symbolic link: exit %d %v, want 3, its limitation and no next_call", run.code, run.out)
	}
	if alone := runSkills(t, home, t.TempDir(), "install"); alone.code != 3 || len(alone.out["next_call"].([]any)) != 0 {
		t.Fatalf("a symbolic link alone: exit %d %v, want 3 and no next_call", alone.code, alone.out)
	}
	mixed := runSkills(t, home, t.TempDir(), "install", "--tool", "universal", "--tool", "claude", "--dry-run")
	next := mixed.out["next_call"].([]any)
	if mixed.code != 3 || len(next) != 1 || next[0].(map[string]any)["command"] != "alarmd-cli skills install --tool universal --tool claude --force" {
		t.Fatalf("mixed conflicts: exit %d next %v, want --force offered without --dry-run", mixed.code, next)
	}
}
