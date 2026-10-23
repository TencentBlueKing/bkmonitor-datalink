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
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// skillText is the agent guide the CLI installs into AI tools' skill
// directories: the same use and boundaries the authorization page's agent
// block gives, without a deployment's address, version or environment.
//
//go:embed skills/alarmd-cli/SKILL.md
var skillText []byte

const skillName = "alarmd-cli"

// reasonDifferentSkill is the one conflict --force resolves.
const reasonDifferentSkill = "a different SKILL.md is already there"

// skillTools and their directories are the same table the admin CLI that
// ships beside alarmd installs its own skill by; the two change together.
var skillTools = []string{"universal", "codex", "claude-code", "cursor", "copilot", "gemini-cli", "opencode"}

var skillAliases = map[string]string{"claude": "claude-code", "gemini": "gemini-cli", "github-copilot": "copilot"}

func skillRoot(tool, scope string) string {
	switch tool {
	case "claude-code":
		return ".claude/skills"
	case "cursor":
		return ".cursor/skills"
	case "copilot":
		if scope == "user" {
			return ".copilot/skills"
		}
		return ".github/skills"
	case "gemini-cli":
		return ".gemini/skills"
	case "opencode":
		if scope == "user" {
			return ".config/opencode/skills"
		}
		return ".opencode/skills"
	}
	return ".agents/skills"
}

type skillTarget struct {
	Tools  []string `json:"tools"`
	Path   string   `json:"path"`
	Status string   `json:"status"`
	Reason string   `json:"reason,omitempty"`
}

// skills runs skills list, show and install. It reads no profile and makes
// no network call.
func (a *App) skills(args []string) int {
	if len(args) == 0 {
		return a.fail("invalid_input", "skills needs list, show or install; run --help", 2)
	}
	command, rest := args[0], args[1:]
	switch command {
	case "list":
		if len(rest) > 0 {
			return a.fail("invalid_input", "skills list takes no arguments", 2)
		}
		tools := make([]map[string]string, 0, len(skillTools))
		for _, tool := range skillTools {
			tools = append(tools, map[string]string{"tool": tool, "user_dir": "~/" + skillRoot(tool, "user"), "project_dir": skillRoot(tool, "project")})
		}
		return a.print(map[string]any{"status": "ok", "summary": "AI tools alarmd-cli can install its skill for; each gets <dir>/alarmd-cli/SKILL.md.",
			"result": map[string]any{"skill": skillName, "tools": tools, "aliases": skillAliases}})
	case "show":
		if len(rest) > 0 {
			return a.fail("invalid_input", "skills show takes no arguments", 2)
		}
		return a.print(map[string]any{"status": "ok", "summary": "The SKILL.md skills install writes.", "result": map[string]any{"skill": skillName, "text": string(skillText)}})
	case "install":
		return a.installSkill(rest)
	}
	return a.fail("invalid_input", "skills needs list, show or install; run --help", 2)
}

func (a *App) installSkill(args []string) int {
	var tools []string
	scope, directory, dryRun, force := "", "", false, false
	for i := 0; i < len(args); i++ {
		name, value, equal := strings.Cut(args[i], "=")
		switch name {
		case "--tool", "--scope", "--directory":
			if !equal {
				if i+1 == len(args) {
					return a.fail("invalid_input", name+" requires a value", 2)
				}
				i++
				value = args[i]
			}
			if value == "" {
				return a.fail("invalid_input", name+" requires a nonempty value", 2)
			}
			if name == "--tool" {
				tools = append(tools, value)
			} else if (name == "--scope" && scope != "") || (name == "--directory" && directory != "") {
				return a.fail("invalid_input", "flags may only be provided once", 2)
			} else if name == "--scope" {
				scope = value
			} else {
				directory = value
			}
		case "--dry-run", "--force":
			if equal || (name == "--dry-run" && dryRun) || (name == "--force" && force) {
				return a.fail("invalid_input", name+" takes no value and may only be provided once", 2)
			}
			if name == "--dry-run" {
				dryRun = true
			} else {
				force = true
			}
		default:
			return a.fail("invalid_input", "unknown skills install argument; run --help", 2)
		}
	}
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "project" {
		return a.fail("invalid_input", "--scope must be user or project", 2)
	}
	if len(tools) == 0 {
		tools = []string{"universal"}
	}
	var resolved []string
	for _, tool := range tools {
		if alias, ok := skillAliases[tool]; ok {
			tool = alias
		}
		switch {
		case tool == "all":
			resolved = append(resolved, skillTools...)
		case contains(skillTools, tool):
			resolved = append(resolved, tool)
		default:
			return a.fail("invalid_input", fmt.Sprintf("unknown tool %q; run skills list", tool), 2)
		}
	}
	base, err := a.skillBase(scope)
	if err != nil {
		return a.fail("configuration_error", err.Error(), 1)
	}
	var targets []*skillTarget
	byPath := map[string]*skillTarget{}
	for _, tool := range resolved {
		root := filepath.Join(base, skillRoot(tool, scope))
		if directory != "" {
			root = directory
			if !filepath.IsAbs(root) {
				root = filepath.Join(a.skillDir(), root)
			}
		}
		path := filepath.Join(filepath.Clean(root), skillName, "SKILL.md")
		if target, seen := byPath[path]; seen {
			if !contains(target.Tools, tool) {
				target.Tools = append(target.Tools, tool)
			}
			continue
		}
		target := &skillTarget{Tools: []string{tool}, Path: path}
		byPath[path] = target
		targets = append(targets, target)
	}
	var limitations []string
	failed, forceable := false, false
	for _, target := range targets {
		target.Status, target.Reason = planSkill(target.Path, force)
		switch target.Status {
		case "conflict":
			limitations = append(limitations, fmt.Sprintf("target_conflict: %s: %s", target.Path, target.Reason))
			forceable = forceable || target.Reason == reasonDifferentSkill
			continue
		case "unchanged":
			continue
		}
		if dryRun {
			target.Status = map[string]string{"installed": "would_install", "updated": "would_update"}[target.Status]
			continue
		}
		if err := writeSkill(target.Path); err != nil {
			target.Status, target.Reason, failed = "failed", err.Error(), true
			limitations = append(limitations, fmt.Sprintf("write_failed: %s: %s", target.Path, err))
		}
	}
	status, code, summary := "ok", 0, "The alarmd-cli skill is in place for every target."
	nextCall := []any{}
	switch {
	case failed:
		status, code, summary = "error", 1, "The skill could not be written to every target; each failure is named."
	case len(limitations) > 0:
		status, code, summary = "partial", 3, "Some targets already hold a different SKILL.md or a symbolic link and were left alone."
		// Only a different SKILL.md is what --force replaces: a symbolic
		// link, or a file that is not regular or not readable, stays a
		// conflict with it, so the limitations alone say why.
		if forceable && !force {
			again := []string{}
			for _, arg := range args {
				if arg != "--dry-run" {
					again = append(again, arg)
				}
			}
			nextCall = append(nextCall, map[string]any{"command": "alarmd-cli skills install " + shellJoin(append(again, "--force")),
				"reason": "Replace the differing SKILL.md files with this CLI's text; other files in those directories are kept. Symbolic links are refused even with --force."})
		}
	}
	if dryRun {
		summary = "Dry run, nothing written: every target would be installed or is already current."
		if code == 3 {
			summary = "Dry run, nothing written: some targets hold a different SKILL.md or a symbolic link and would be left alone."
		}
	}
	return a.printSkillResult(status, summary, targets, limitations, nextCall, code)
}

func (a *App) printSkillResult(status, summary string, targets []*skillTarget, limitations []string, nextCall []any, code int) int {
	if limitations == nil {
		limitations = []string{}
	}
	if exit := a.print(map[string]any{"status": status, "summary": summary, "result": map[string]any{"skill": skillName, "targets": targets},
		"evidence": map[string]any{"complete": len(limitations) == 0, "limitations": limitations}, "next_call": nextCall}); exit != 0 {
		return exit
	}
	return code
}

// planSkill says what installing at path would do: a symbolic link for the
// skill directory or the file is refused whatever --force says, a different
// file is replaced only with it.
func planSkill(path string, force bool) (string, string) {
	if info, err := os.Lstat(filepath.Dir(path)); err == nil {
		if info.Mode()&fs.ModeSymlink != 0 {
			return "conflict", "the skill directory is a symbolic link"
		}
		if !info.IsDir() {
			return "conflict", "the skill directory is not a directory"
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		// Nothing there yet; a path through a file fails when written.
		return "installed", ""
	}
	if err != nil {
		return "conflict", "the existing SKILL.md cannot be read"
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "conflict", "SKILL.md is a symbolic link"
	}
	if !info.Mode().IsRegular() {
		return "conflict", "SKILL.md is not a regular file"
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return "conflict", "the existing SKILL.md cannot be read"
	}
	if bytes.Equal(current, skillText) {
		return "unchanged", ""
	}
	if !force {
		return "conflict", reasonDifferentSkill
	}
	return "updated", ""
}

// writeSkill writes the skill through a temporary file in the same
// directory and a rename, so a failure leaves no half file.
func writeSkill(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.New("cannot create the skill directory")
	}
	temp, err := os.CreateTemp(dir, ".SKILL.md.*")
	if err != nil {
		return errors.New("cannot create a temporary file in the skill directory")
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(skillText); err != nil {
		_ = temp.Close()
		return errors.New("cannot write the temporary file")
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return errors.New("cannot set the file mode")
	}
	if err := temp.Close(); err != nil {
		return errors.New("cannot write the temporary file")
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return errors.New("cannot move the file into place")
	}
	return nil
}

func (a *App) skillBase(scope string) (string, error) {
	if scope == "project" {
		return a.skillDir(), nil
	}
	if a.home != "" {
		return a.home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate the user's home directory; use --directory")
	}
	return home, nil
}

func (a *App) skillDir() string {
	if a.workdir != "" {
		return a.workdir
	}
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && !strings.ContainsAny(arg, " \t\n'\"\\$`*?[]{}()<>|&;#~!") {
			quoted[i] = arg
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
