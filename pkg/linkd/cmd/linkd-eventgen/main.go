// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Command linkd-eventgen 通过配置生成事件，支持本地运行和 Admin 托管模式。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"linkd/internal/config"
	"linkd/internal/eventgen"
	"linkd/internal/logging"
)

var (
	version   = "dev"
	gitCommit = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root().ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func load(path string) (eventgen.Config, error) {
	//nolint:gosec // G304: 路径来自本地操作者明确指定的配置文件，不接受远端请求。
	b, e := os.ReadFile(path)
	if e != nil {
		return eventgen.Config{}, e
	}
	var c eventgen.Config
	e = yaml.Unmarshal(b, &c)
	return c, e
}

func root() *cobra.Command {
	root := &cobra.Command{Use: "linkd-eventgen", Short: "配置驱动的 Linkd 事件生成器", SilenceUsage: true}
	root.AddCommand(&cobra.Command{Use: "version", RunE: func(c *cobra.Command, _ []string) error {
		_, e := fmt.Fprintf(c.OutOrStdout(), "version: %s\ngit_commit: %s\n", version, gitCommit)
		return e
	}})
	for _, mode := range []string{"validate", "preview", "run"} {
		var path, sources string
		var count int
		cmd := &cobra.Command{Use: mode, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			c, e := load(path)
			if e != nil {
				return e
			}
			if _, e = eventgen.Compile(c); e != nil {
				return e
			}
			switch mode {
			case "validate":
				_, e = fmt.Fprintln(cmd.OutOrStdout(), "配置有效")
				return e
			case "preview":
				records, e := eventgen.Preview(c, c.StartAt, count)
				if e != nil {
					return e
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(records)
			default:
				return localRun(cmd.Context(), path, sources, c)
			}
		}}
		cmd.Flags().StringVar(&path, "config", "", "eventgen YAML/JSON 配置")
		_ = cmd.MarkFlagRequired("config")
		if mode == "preview" {
			cmd.Flags().IntVar(&count, "count", 10, "预览事件数量，最多 100")
		}
		if mode == "run" {
			cmd.Flags().StringVar(&sources, "sources", "", "包含 EventSource 的 Linkd 配置")
			_ = cmd.MarkFlagRequired("sources")
		}
		root.AddCommand(cmd)
	}
	var o eventgen.ManagedOptions
	managed := &cobra.Command{Use: "serve", Short: "连接控制面，等待显式启动任务", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if o.Token == "" {
			o.Token = os.Getenv("LINKD_EVENTGEN_WORKER_TOKEN")
		}
		logger, err := logging.New(logging.DefaultConfig(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		o.Logger = logger
		return eventgen.RunManaged(cmd.Context(), o)
	}}
	managed.Flags().StringVar(&o.URL, "control-plane", "", "Linkd 控制面地址")
	managed.Flags().DurationVar(&o.PollInterval, "poll-interval", 3*time.Second, "配置与指令拉取周期")
	_ = managed.MarkFlagRequired("control-plane")
	root.AddCommand(managed)
	return root
}

func localSources(path string, c eventgen.Config) (map[string]config.EventSource, error) {
	cfg, e := config.Load(path, config.Overrides{})
	if e != nil {
		return nil, e
	}
	result := map[string]config.EventSource{}
	for _, s := range c.Scenarios {
		for _, source := range cfg.EventSources {
			if source.EventSourceID == s.SourceID {
				source = source.WithDefaults()
				if !source.Enabled || source.RelatedTenantID != "" && source.RelatedTenantID != c.TenantID {
					return nil, fmt.Errorf("source unavailable for tenant")
				}
				result[s.SourceID] = source
			}
		}
		if _, ok := result[s.SourceID]; !ok {
			return nil, fmt.Errorf("source %s not found", s.SourceID)
		}
	}
	return result, nil
}

func localRun(ctx context.Context, path, sourcePath string, c eventgen.Config) error {
	sources, e := localSources(sourcePath, c)
	if e != nil {
		return e
	}
	publisher, e := eventgen.NewKafkaPublisher(sources, c.Limits)
	if e != nil {
		return e
	}
	start := time.Now().UTC()
	if c.StartAt.After(start) {
		start = c.StartAt
	}
	logger, e := logging.New(logging.DefaultConfig(), os.Stderr)
	if e != nil {
		publisher.Close()
		return e
	}
	engine, e := eventgen.New(c, eventgen.NewRunID(), 1, start, publisher, logger)
	if e != nil {
		publisher.Close()
		return e
	}
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	info, e := os.Stat(path)
	if e != nil {
		return e
	}
	changed := info.ModTime()
	revision := int64(1)
	for {
		select {
		case err := <-done:
			_ = json.NewEncoder(os.Stdout).Encode(engine.Snapshot())
			return err
		case <-ticker.C:
			next, e := os.Stat(path)
			if e != nil || !next.ModTime().After(changed) {
				continue
			}
			candidate, e := load(path)
			if e == nil {
				var sources map[string]config.EventSource
				sources, e = localSources(sourcePath, candidate)
				if e == nil {
					e = engine.ApplySources(candidate, revision+1, sources)
				}
			}
			if e != nil {
				_, _ = fmt.Fprintln(os.Stderr, "eventgen: configuration reload failed")
				continue
			}
			revision++
			changed = next.ModTime()
		}
	}
}
