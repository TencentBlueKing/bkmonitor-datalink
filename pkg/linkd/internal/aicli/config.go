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
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type profile struct {
	URL            string `yaml:"url" json:"url"`
	Username       string `yaml:"username" json:"username"`
	Password       string `yaml:"password" json:"password"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type settings struct {
	CurrentProfile string             `yaml:"current_profile" json:"current_profile"`
	Profiles       map[string]profile `yaml:"profiles" json:"profiles"`
}

func configLocation(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir != "" && !filepath.IsAbs(dir) {
		return "", problem("config_error", "XDG_CONFIG_HOME 必须为绝对路径")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", problem("config_error", "无法定位用户主目录")
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "linkd-cli", "config.yaml"), nil
}

func validProfileName(name string) bool {
	return regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(name)
}

func validateProfile(p profile) error {
	u, err := url.Parse(p.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return problem("config_error", "url 必须是无凭据、查询参数及片段的 HTTP(S) Console 地址")
	}
	// 与 Console 部署子路径约束一致，避免转义、点路径和重定向改变接口边界。
	if path := strings.TrimSuffix(u.EscapedPath(), "/"); path != "" && !regexp.MustCompile(`^(/[a-zA-Z0-9_-]+)+$`).MatchString(path) {
		return problem("config_error", "Console 子路径仅支持字母、数字、下划线和连字符")
	}
	if p.Username == "" || p.Password == "" || strings.Contains(p.Username, ":") || strings.ContainsAny(p.Username+p.Password, "\r\n\x00") || len(p.Username)+len(p.Password)+1 > 4096 {
		return problem("config_error", "必须提供合法账号密码（合计最多 4096 字节），用户名不能含冒号")
	}
	if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 120 {
		return problem("config_error", "timeout-seconds 必须在 1–120 之间")
	}
	return nil
}

func readSettings(path string) (settings, error) {
	result := settings{Profiles: map[string]profile{}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return result, problem("config_error", "配置必须是仅当前用户可读写的普通文件（0600），不能是符号链接")
	}
	f, err := os.Open(path) //nolint:gosec // 只读取用户显式选择的本地配置，前面已检查普通文件和私有权限。
	if err != nil {
		return result, problem("config_error", "无法读取配置文件")
	}
	defer func() { _ = f.Close() }()
	raw, err := readBounded(f, 1<<20)
	if err != nil {
		return result, problem("config_error", "配置读取失败或超过 1 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if decoder.Decode(&result) != nil {
		return result, problem("config_error", "配置 YAML 无效或包含未知字段")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return result, problem("config_error", "配置只能包含一个 YAML 文档")
	}
	if result.Profiles == nil {
		result.Profiles = map[string]profile{}
	}
	for name, p := range result.Profiles {
		if !validProfileName(name) {
			return result, problem("config_error", "profile 名称无效")
		}
		if err := validateProfile(p); err != nil {
			return result, err
		}
	}
	if result.CurrentProfile != "" {
		if _, ok := result.Profiles[result.CurrentProfile]; !ok {
			return result, problem("config_error", "当前 profile 不存在")
		}
	}
	return result, nil
}

func writeSettings(path string, cfg settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return problem("config_error", "无法创建配置目录")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return problem("config_error", "配置目录必须是普通目录")
	}
	if err := os.Chmod(dir, 0700); err != nil { //nolint:gosec // 这里是目录，当前用户需要 execute 权限以访问 0600 配置文件。
		return problem("config_error", "无法设置配置目录权限")
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return problem("config_error", "无法编码配置")
	}
	// 临时文件和目标位于同一目录；中途失败保留旧文件，不暴露半份凭据。
	f, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return problem("config_error", "无法创建配置临时文件")
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(raw); err != nil {
		return problem("config_error", "无法写入配置")
	}
	if err := f.Sync(); err != nil {
		return problem("config_error", "无法同步配置")
	}
	if err := f.Close(); err != nil {
		return problem("config_error", "无法关闭配置文件")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return problem("config_error", "无法替换配置文件")
	}
	return nil
}

func (o *options) load() (settings, string, error) {
	path, err := configLocation(o.configPath)
	if err != nil {
		return settings{}, "", err
	}
	cfg, err := readSettings(path)
	return cfg, path, err
}

func (o *options) selected() (string, profile, error) {
	cfg, _, err := o.load()
	if err != nil {
		return "", profile{}, err
	}
	name := o.profile
	if name == "" {
		name = cfg.CurrentProfile
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return "", profile{}, problem("config_error", "目标 profile 不存在，请先 config set/use 或传入 --profile")
	}
	return name, p, nil
}

func publicSettings(cfg settings) settings {
	copy := settings{CurrentProfile: cfg.CurrentProfile, Profiles: map[string]profile{}}
	for name, p := range cfg.Profiles {
		p.Password = "******"
		copy.Profiles[name] = p
	}
	return copy
}

func configCommand(opts *options) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "管理本地连接配置（不调用远程接口）"}
	var address, username string
	var passwordStdin bool
	var timeout int
	set := &cobra.Command{Use: "set <profile>", Short: "保存连接；密码仅从标准输入接收，首个 profile 自动选中", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !validProfileName(args[0]) {
			return problem("invalid_argument", "profile 名称仅允许 1–64 位字母、数字、下划线和连字符")
		}
		if !passwordStdin {
			return problem("invalid_argument", "必须使用 --password-stdin 提供密码")
		}
		raw, err := readBounded(cmd.InOrStdin(), 4096)
		if err != nil {
			return problem("invalid_argument", "无法读取密码或密码超过上限")
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		p := profile{URL: strings.TrimRight(address, "/"), Username: username, Password: password, TimeoutSeconds: timeout}
		if err := validateProfile(p); err != nil {
			return err
		}
		cfg, path, err := opts.load()
		if err != nil {
			return err
		}
		cfg.Profiles[args[0]] = p
		if cfg.CurrentProfile == "" {
			cfg.CurrentProfile = args[0]
		}
		if err := writeSettings(path, cfg); err != nil {
			return err
		}
		return emit(cmd.OutOrStdout(), publicSettings(cfg))
	}}
	set.Flags().StringVar(&address, "url", "", "Console 基础 URL，可包含部署子路径")
	set.Flags().StringVar(&username, "username", "", "Basic Auth 用户名")
	set.Flags().BoolVar(&passwordStdin, "password-stdin", false, "从标准输入读取密码（移除一个结尾换行）")
	set.Flags().IntVar(&timeout, "timeout-seconds", 30, "请求超时秒数，1–120")
	root.AddCommand(set)
	for _, action := range []string{"list", "show", "use", "delete"} {
		command := &cobra.Command{Use: action, Short: "本地 profile " + action, Args: cobra.NoArgs}
		if action == "show" {
			command.Use += " [profile]"
			command.Args = cobra.MaximumNArgs(1)
		}
		if action == "use" || action == "delete" {
			command.Use += " <profile>"
			command.Args = cobra.ExactArgs(1)
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			cfg, path, err := opts.load()
			if err != nil {
				return err
			}
			if action == "list" {
				return emit(cmd.OutOrStdout(), publicSettings(cfg))
			}
			name := opts.profile
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				name = cfg.CurrentProfile
			}
			p, ok := cfg.Profiles[name]
			if !ok {
				return problem("config_error", "profile 不存在")
			}
			switch action {
			case "show":
				p.Password = "******"
				return emit(cmd.OutOrStdout(), map[string]any{"profile": name, "connection": p})
			case "use":
				cfg.CurrentProfile = name
			case "delete":
				delete(cfg.Profiles, name)
				if cfg.CurrentProfile == name {
					cfg.CurrentProfile = ""
				}
			}
			if err := writeSettings(path, cfg); err != nil {
				return err
			}
			return emit(cmd.OutOrStdout(), publicSettings(cfg))
		}
		root.AddCommand(command)
	}
	return root
}
