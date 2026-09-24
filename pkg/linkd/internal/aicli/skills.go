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
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"
)

//go:embed skills/linkd-ops
var skillFiles embed.FS

type skillTarget struct {
	Agents []string `json:"agents"`
	Path   string   `json:"path"`
	State  string   `json:"state"`
	base   string
	subdir string
}

func skillTargets(agent, scope, projectDir string) ([]skillTarget, error) {
	agents := []string{"codex", "claude-code", "cursor", "gemini-cli"}
	if agent != "all" {
		if !slices.Contains(agents, agent) {
			return nil, problem("invalid_argument", "--agent 必须为 codex、claude-code、cursor、gemini-cli 或 all")
		}
		agents = []string{agent}
	}
	var base string
	var err error
	switch scope {
	case "user":
		if projectDir != "" {
			return nil, problem("invalid_argument", "--project-dir 只能与 --scope project 一起使用")
		}
		base, err = os.UserHomeDir()
	case "project":
		if projectDir == "" {
			base, err = os.Getwd()
		} else {
			base, err = filepath.Abs(projectDir)
		}
	default:
		return nil, problem("invalid_argument", "--scope 必须为 user 或 project")
	}
	if err != nil {
		return nil, problem("install_error", "无法定位安装根目录")
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return nil, problem("install_error", "安装根目录必须已经存在")
	}
	var result []skillTarget
	for _, agent := range agents {
		subdir := ".agents"
		if agent == "claude-code" {
			subdir = ".claude"
		}
		path := filepath.Join(base, subdir, "skills", "linkd-ops")
		index := slices.IndexFunc(result, func(t skillTarget) bool { return t.Path == path })
		if index >= 0 {
			result[index].Agents = append(result[index].Agents, agent)
			continue
		}
		result = append(result, skillTarget{Agents: []string{agent}, Path: path, base: base, subdir: subdir})
	}
	return result, nil
}

func bundle() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(skillFiles, "skills/linkd-ops", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		value, err := skillFiles.ReadFile(path)
		if err != nil {
			return err
		}
		files[path[len("skills/linkd-ops/"):]] = value
		return nil
	})
	return files, err
}

// inspectSkill 在写入任何目标前检查全部已存在节点。--force 只替换随包文件，
// 不删除用户额外文件，也不沿着 skill 内的符号链接覆盖其他位置。
func inspectSkill(target skillTarget, files map[string][]byte) (string, error) {
	for _, path := range []string{filepath.Join(target.base, target.subdir), filepath.Join(target.base, target.subdir, "skills"), target.Path} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return "missing", nil
		}
		if err != nil || !info.IsDir() {
			return "", problem("install_error", "安装路径包含符号链接或非目录节点，拒绝覆盖")
		}
	}
	state := "current"
	for name, expected := range files {
		parent := filepath.Dir(filepath.Join(target.Path, filepath.FromSlash(name)))
		for parent != target.Path {
			info, err := os.Lstat(parent)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", problem("install_error", "无法检查 skill 目录")
			}
			if err == nil && !info.IsDir() {
				return "", problem("install_error", "skill 内包含符号链接或非目录节点")
			}
			parent = filepath.Dir(parent)
		}
		path := filepath.Join(target.Path, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if state == "current" {
				state = "incomplete"
			}
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return "", problem("install_error", "skill 文件不是普通文件，拒绝覆盖")
		}
		f, err := os.Open(path) //nolint:gosec // 路径来自内嵌 bundle 的固定文件名，已拒绝符号链接节点。
		if err != nil {
			return "", problem("install_error", "无法读取已安装的 skill")
		}
		actual, readErr := readBounded(f, 1<<20)
		_ = f.Close()
		if readErr != nil || !bytes.Equal(actual, expected) {
			state = "different"
		}
	}
	return state, nil
}

func installSkill(target skillTarget, files map[string][]byte) error {
	// 每个文件先写入同目录临时文件再替换；单文件不会出现半份内容。
	// 跨目录安装不保证事务性；出错保留已完成文件，重复安装可继续完成。
	for _, name := range sortedFileNames(files) {
		path := filepath.Join(target.Path, filepath.FromSlash(name))
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return problem("install_error", "无法创建 skill 目录；已完成文件会保留")
		}
		if err := writeSkillFile(path, files[name]); err != nil {
			return err
		}
	}
	return nil
}

func sortedFileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func writeSkillFile(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".linkd-skill-*")
	if err != nil {
		return problem("install_error", "无法创建 skill 临时文件")
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(raw); err != nil {
		return problem("install_error", "无法写入 skill")
	}
	if err := f.Close(); err != nil {
		return problem("install_error", "无法关闭 skill 临时文件")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return problem("install_error", "无法替换 skill 文件；已完成文件会保留")
	}
	return nil
}

func skillsCommand() *cobra.Command {
	root := &cobra.Command{Use: "skills", Short: "离线查看或安装面向 Linkd 开发维护的 skill"}
	for _, action := range []string{"list", "install"} {
		var agent, scope, projectDir string
		var force, dryRun bool
		cmd := &cobra.Command{Use: action, Short: "离线 " + action + " linkd-ops", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if action == "install" && !cmd.Flags().Changed("agent") {
				return problem("invalid_argument", "安装必须显式选择 --agent")
			}
			targets, err := skillTargets(agent, scope, projectDir)
			if err != nil {
				return err
			}
			files, err := bundle()
			if err != nil {
				return problem("install_error", "无法读取内嵌 skill")
			}
			for i := range targets {
				state, err := inspectSkill(targets[i], files)
				if err != nil {
					return err
				}
				targets[i].State = state
				if action == "install" && !dryRun && state == "different" && !force {
					return problem("install_conflict", "已安装 skill 与内嵌版本不同；先预览，显式 --force 才覆盖随包文件")
				}
			}
			if action == "install" && !dryRun {
				for i := range targets {
					if targets[i].State == "current" {
						continue
					}
					if err := installSkill(targets[i], files); err != nil {
						return err
					}
					targets[i].State = "installed"
				}
			}
			return emit(cmd.OutOrStdout(), map[string]any{"skill": "linkd-ops", "scope": scope, "dry_run": dryRun, "targets": targets, "files": sortedFileNames(files)})
		}}
		cmd.Flags().StringVar(&agent, "agent", "all", "codex、claude-code、cursor、gemini-cli 或 all；共享目录自动去重")
		cmd.Flags().StringVar(&scope, "scope", "user", "user 或 project")
		cmd.Flags().StringVar(&projectDir, "project-dir", "", "项目目录，默认当前目录，仅用于 project scope")
		if action == "install" {
			cmd.Flags().BoolVar(&force, "force", false, "覆盖内容不同的随包文件，保留其他文件")
			cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只显示安装路径和差异状态，不写文件")
		}
		root.AddCommand(cmd)
	}
	return root
}
