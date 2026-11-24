// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package allinone_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
)

// restartDuringMerge 只用于显式故障验收：成员批已开始完成而组未汇总时强制终止真实进程。
// 不删除租约、不缩短生产租期，等待自己的控制面租约自然过期再重启。
func (h *policyHarness) restartDuringMerge(size int, cfg config.Config, client *redis.Client) {
	h.t.Helper()
	call, cancel := context.WithTimeout(h.ctx, 3*time.Minute)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	completed := 0
	for completed == 0 {
		// 仅读取本轮 TempDir 下的合成测试进程日志。
		raw, err := os.ReadFile(h.process.logPath)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `"msg":"merge member batch completed"`) && strings.Contains(line, `"kind":"link"`) {
				completed++
			}
		}
		if completed != 0 {
			break
		}
		select {
		case <-call.Done():
			h.t.Fatal("merge did not start before restart deadline")
		case <-tick.C:
		}
	}
	batchSize := cfg.MergeControlSettings().MemberBatchSize
	if completed >= (size+batchSize-1)/batchSize {
		h.t.Fatal("restart missed partial execution window")
	}
	old := h.process
	if err := old.command.Process.Kill(); err != nil {
		h.t.Fatal(err)
	}
	<-old.wait
	old.exited, old.exitErr = true, nil
	_ = old.logFile.Close()
	h.t.Logf("SCALE_RESTART interrupted_after_completed_batches=%d members=%d", completed, size)
	leader := "linkd:dispatch:{" + cfg.Dispatch.WithDefaults().Deployment + "}:leader"
	for {
		present, err := client.Exists(call, leader).Result()
		if err != nil {
			h.t.Fatal(err)
		}
		if present == 0 {
			break
		}
		select {
		case <-call.Done():
			h.t.Fatal("owned controller lease did not expire")
		case <-tick.C:
		}
	}
	h.process = startAllInOne(h.t, repositoryRoot(h.t), old.command.Path, h.configPath, h.t.TempDir())
	// 等待新控制面恢复就绪，后续复用完整容量验收核对成员、关系、事件和唯一父动作。
	for {
		if exited, err := h.process.checkExited(); exited {
			h.t.Fatalf("restart exited: %v", err)
		}
		var state map[string]any
		if err := h.client.Call(call, http.MethodGet, "/api/v1/runtime", nil, &state); err == nil {
			break
		}
		select {
		case <-call.Done():
			h.t.Fatal("restarted controller did not become ready")
		case <-tick.C:
		}
	}
	h.t.Log("SCALE_RESTART controller_restarted=true")
}
