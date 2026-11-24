// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package allinone_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// saveScaleBatchSpans 保存独立批次的实际起止，避免用一秒状态轮询估算成员吞吐。
func (h *policyHarness) saveScaleBatchSpans() {
	root := os.Getenv("LINKD_E2E_MERGE_EVIDENCE_DIR")
	if root == "" {
		return
	}
	path := filepath.Join(root, h.names.Token+"-batch-spans.jsonl")
	if err := os.MkdirAll(root, 0700); err != nil { //nolint:gosec // G703: 显式 E2E 诊断目录，只写本轮测试证据。
		h.t.Fatal(err)
	}
	output, err := os.Create(path) //nolint:gosec // G304: path 来自显式 E2E 目录与本轮隔离资源名。
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	readScaleBatchSpans(h.t, h.process.logPath, func(row map[string]any) {
		raw, err := json.Marshal(row)
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := output.Write(append(raw, '\n')); err != nil {
			h.t.Fatal(err)
		}
	})
	h.t.Logf("SCALE_SPANS path=%s", path)
}

func readScaleBatchSpans(t *testing.T, path string, record func(map[string]any)) {
	t.Helper()
	input, err := os.Open(path) //nolint:gosec // G304: 只读本轮启动的 Linkd 测试进程日志。
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	scan := bufio.NewScanner(input)
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		var row map[string]any
		if json.Unmarshal(scan.Bytes(), &row) == nil && row["msg"] == "merge member batch completed" {
			record(row)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
}
