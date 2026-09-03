// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Command rawgen 生成 standard JSONL 和对应的可计算预期结果。
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"linkd/internal/testkit/rawgen"
)

const defaultStartTime = "2026-08-31T00:00:00Z"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "rawgen:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("rawgen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profile := flags.String("profile", "", "读取完整 JSON profile；不能与规模/场景 flags 同时使用")
	seed := flags.Uint64("seed", 42, "确定性随机种子")
	count := flags.Int("count", 100, "按 -types 平均分配的生命周期场景数量")
	typesText := flags.String(
		"types",
		"active,recovered,closed,severity_rotation",
		"参与平均分配的逗号分隔场景类型",
	)
	mixText := flags.String("mix", "", "精确配额，例如 active=10,recovered=20；设置后忽略 -count/-types")
	tenantCount := flags.Int("tenant-count", 10, "租户数量；cross_tenant 至少需要 2")
	tenantPrefix := flags.String("tenant-prefix", "tenant-load", "租户 ID 前缀")
	eventSourceID := flags.String("event-source-id", "e2e-source", "EventSource event_source_id")
	duplicates := flags.Int("duplicates", 0, "额外重复 delivery 数量")
	invalid := flags.Int("invalid", 0, "额外确定性坏消息数量")
	minUpdates := flags.Int("min-updates", 0, "每个适用生命周期的最少 updated 数")
	maxUpdates := flags.Int("max-updates", 2, "每个适用生命周期的最多 updated 数")
	startTimeText := flags.String("start-time", defaultStartTime, "数据时间基线，RFC3339")
	outputPath := flags.String("out", "-", "standard JSONL 输出路径；- 表示 stdout")
	expectedPath := flags.String("expected-out", "", "可选的配置和预期结果 JSON 输出路径")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}

	var config rawgen.Config
	if *profile != "" {
		forbidden := make(map[string]bool)
		flags.Visit(func(item *flag.Flag) {
			if item.Name != "profile" && item.Name != "out" && item.Name != "expected-out" {
				forbidden[item.Name] = true
			}
		})
		if len(forbidden) != 0 {
			return fmt.Errorf("-profile cannot be combined with generation flags: %v", forbidden)
		}
		loaded, err := rawgen.LoadConfig(*profile)
		if err != nil {
			return err
		}
		config = loaded
	} else {
		startTime, err := time.Parse(time.RFC3339, *startTimeText)
		if err != nil {
			return fmt.Errorf("parse -start-time: %w", err)
		}
		var counts map[rawgen.ScenarioType]int
		if *mixText != "" {
			counts, err = rawgen.ParseMix(*mixText)
		} else {
			var types []rawgen.ScenarioType
			types, err = rawgen.ParseScenarioTypes(*typesText)
			if err == nil {
				counts, err = rawgen.BalancedCounts(*count, types)
			}
		}
		if err != nil {
			return err
		}
		config = rawgen.Config{
			Seed:             *seed,
			EventSourceID:    *eventSourceID,
			TenantPrefix:     *tenantPrefix,
			TenantCount:      *tenantCount,
			Counts:           counts,
			DuplicateRecords: *duplicates,
			InvalidRecords:   *invalid,
			MinUpdates:       *minUpdates,
			MaxUpdates:       *maxUpdates,
			StartTime:        startTime,
		}
	}
	dataset, err := rawgen.Generate(config)
	if err != nil {
		return err
	}
	if err := writeJSONL(*outputPath, stdout, dataset.Records); err != nil {
		return err
	}
	if *expectedPath != "" {
		if err := writeExpected(*expectedPath, dataset); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(
		stderr,
		"seed=%d scenarios=%d records=%d valid_events=%d outputs=%d alerts=%d invalid=%d duplicates=%d\n",
		dataset.Config.Seed,
		scenarioCount(dataset.Config.Counts),
		dataset.Expected.InputRecords,
		len(dataset.Expected.SourceEventIDs),
		dataset.Expected.OutputMessages,
		len(dataset.Expected.Alerts),
		dataset.Config.InvalidRecords,
		dataset.Config.DuplicateRecords,
	)
	return nil
}

func writeJSONL(path string, stdout io.Writer, records []rawgen.Record) error {
	if path == "-" {
		return encodeJSONL(stdout, records)
	}
	// path 由本地开发者显式指定，用于产生测试制品。
	//nolint:gosec // G703: 写入调用方要求的明确路径是 CLI 的目标能力。
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open standard output %q: %w", path, err)
	}
	encodeErr := encodeJSONL(file, records)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}

func encodeJSONL(destination io.Writer, records []rawgen.Record) error {
	buffer := bufio.NewWriter(destination)
	for index, record := range records {
		if _, err := buffer.Write(record.Body); err != nil {
			return fmt.Errorf("write standard event %d: %w", index, err)
		}
		if err := buffer.WriteByte('\n'); err != nil {
			return fmt.Errorf("write standard separator: %w", err)
		}
	}
	if err := buffer.Flush(); err != nil {
		return fmt.Errorf("flush standard output: %w", err)
	}
	return nil
}

func writeExpected(path string, dataset rawgen.Dataset) error {
	rawgen.SortExpected(&dataset.Expected)
	body, err := json.MarshalIndent(dataset, "", "  ")
	if err != nil {
		return fmt.Errorf("encode expected result: %w", err)
	}
	body = append(body, '\n')
	// path 由本地开发者显式指定，用于产生测试制品。
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write expected output %q: %w", path, err)
	}
	return nil
}

func scenarioCount(counts map[rawgen.ScenarioType]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
