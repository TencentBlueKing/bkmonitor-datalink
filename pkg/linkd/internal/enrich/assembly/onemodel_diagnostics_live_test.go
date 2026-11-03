// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package assembly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/logging"
)

// TestOneModelDiagnosticsLiveReplay 用显式提供的真实 Event 和已发布来源重放完整丰富链。
// 仅装配既有只读 Runtime，不启动 Lifecycle、Repository、Hook、发布器或队列消费者。
// 普通单测跳过；最多 32 个样本，4 条连接，3 分钟总预算。报告只保留身份、状态和诊断，
// 业务补丁以摘要比较，运行日志仅统计条数，避免把异常正文保存进交付报告。
func TestOneModelDiagnosticsLiveReplay(t *testing.T) {
	fixturePath, resourcesPath, reportPath := os.Getenv("LINKD_DIAGNOSTICS_FIXTURE"), os.Getenv("LINKD_DIAGNOSTICS_RESOURCES"), os.Getenv("LINKD_DIAGNOSTICS_REPORT")
	if fixturePath == "" || resourcesPath == "" || reportPath == "" {
		t.Skip("requires explicit read-only resources, captured samples and report path")
	}
	read := func(path string, target any) {
		t.Helper()
		info, err := os.Stat(path) //nolint:gosec // G703: 操作者显式指定的集成输入，读取前限制大小。
		if err != nil || info.Size() > 8<<20 {
			t.Fatal("live input unavailable or oversized")
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: 同上。
		if err != nil || json.Unmarshal(raw, target) != nil {
			t.Fatal("invalid live input")
		}
	}
	var fixture struct {
		SourceVersion int64              `json:"source_version"`
		Tenant        string             `json:"bk_tenant_id"`
		Source        config.EventSource `json:"source"`
		Events        []domain.Event     `json:"events"`
	}
	var resources config.ResourcesConfig
	read(fixturePath, &fixture)
	read(resourcesPath, &resources)
	fixture.Source.Version = fixture.SourceVersion
	if fixture.Tenant == "" || len(fixture.Events) == 0 || len(fixture.Events) > 32 || fixture.Source.Version <= 0 {
		t.Fatal("invalid fixture bounds")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runtime, err := Open(ctx, fixture.Source, resources, 4, 5*time.Second, nil)
	if err != nil {
		t.Fatal("open selected read-only resources failed")
	}
	t.Cleanup(func() {
		if runtime.Close() != nil {
			t.Error("close resources failed")
		}
	})
	reads := &diagnosticsK8sReader{next: runtime.sources.K8s}
	runtime.sources.K8s = reads
	var logs bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &logs)
	if err != nil {
		t.Fatal(err)
	}
	router, err := runtime.Router(fixture.Source, nil, WithLogger(logger))
	if err != nil {
		t.Fatal("assemble source failed")
	}
	type step struct {
		Name              string              `json:"name"`
		Severity          string              `json:"severity"`
		Status            domain.EnrichStatus `json:"status"`
		Diagnostics       []enrich.Diagnostic `json:"diagnostics"`
		PatchDigest       string              `json:"patch_digest"`
		ReusedFromAlertID string              `json:"reused_from_alert_id,omitempty"`
	}
	type row struct {
		EventID       string              `json:"event_id"`
		Status        domain.EnrichStatus `json:"status"`
		Steps         []step              `json:"steps"`
		Warnings      int                 `json:"warnings"`
		K8sReads      map[string]int      `json:"k8s_reads"`
		Reuse         []step              `json:"reuse,omitempty"`
		ReuseK8sReads map[string]int      `json:"reuse_k8s_reads,omitempty"`
	}
	report := make([]row, 0, len(fixture.Events))
	for _, event := range fixture.Events {
		if event.BKTenantID != fixture.Tenant || event.EventSourceID != fixture.Source.EventSourceID {
			t.Fatal("sample escaped selected scope")
		}
		original := event.Clone()
		logs.Reset()
		reads.calls = map[string]int{}
		result, err := router.Enrich(ctx, enrich.Input{Event: event, Preview: true})
		if err != nil {
			t.Fatalf("replay failed for %s", event.EventID)
		}
		if !reflect.DeepEqual(original, event) {
			t.Fatal("source Event mutated")
		}
		record := row{EventID: event.EventID, Status: result.Status, Warnings: bytes.Count(logs.Bytes(), []byte("\n")), K8sReads: reads.calls}
		for _, evaluation := range result.Data.Evaluations {
			payload, err := enrich.DecodePayload(evaluation.Data)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range payload.Processors {
				for name, output := range entry {
					raw, err := json.Marshal(output.Patches)
					if err != nil {
						t.Fatal(err)
					}
					digest := sha256.Sum256(raw)
					record.Steps = append(record.Steps, step{Name: name, Severity: evaluation.Severity, Status: output.Status, Diagnostics: output.Diagnostics, PatchDigest: hex.EncodeToString(digest[:]), ReusedFromAlertID: output.ReusedFromAlertID})
				}
			}
		}
		// 仅在内存中开启目标步骤复用；不发布配置、不保存 Alert。
		// 同一真实 Event 的第一轮结果作为活跃快照，验证后续 Event 的复用资格。
		if len(result.Data.Evaluations) > 0 {
			selected := result.Data.Evaluations[0]
			hasK8s := false
			for _, entry := range mustLivePayload(t, selected.Data).Processors {
				if output, ok := entry["k8s"]; ok && output.Status != domain.EnrichStatusSkipped {
					hasK8s = true
				}
			}
			if hasK8s {
				source := fixture.Source
				source.Enrich.Processors = append([]config.EnrichProcessorConfig(nil), source.Enrich.Processors...)
				for i := range source.Enrich.Processors {
					if source.Enrich.Processors[i].Type == "monitor-resource" || source.Enrich.Processors[i].Type == "k8s" {
						source.Enrich.Processors[i].ReuseOnActive = true
					}
				}
				reuseRouter, err := runtime.Router(source, nil)
				if err != nil {
					t.Fatal(err)
				}
				active := domain.Alert{AlertID: "readonly-fallback-snapshot", BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint, Status: domain.AlertStatusActive, EnrichSeverity: selected.Severity, Enrich: selected.Data.Clone()}
				incoming := event.Clone()
				incoming.EventID = event.EventID + "-readonly-reuse"
				incoming.Evaluations = nil
				for _, evaluation := range event.Evaluations {
					if evaluation.Severity == selected.Severity {
						incoming.Evaluations = append(incoming.Evaluations, evaluation)
					}
				}
				reads.calls = map[string]int{}
				next, err := reuseRouter.Enrich(ctx, enrich.Input{Event: incoming, ActiveAlert: &active})
				if err != nil {
					t.Fatal("read-only snapshot reuse failed")
				}
				record.ReuseK8sReads = reads.calls
				for _, evaluation := range next.Data.Evaluations {
					for _, entry := range mustLivePayload(t, evaluation.Data).Processors {
						for name, output := range entry {
							if name != "monitor-resource" && name != "k8s" {
								continue
							}
							raw, err := json.Marshal(output.Patches)
							if err != nil {
								t.Fatal(err)
							}
							digest := sha256.Sum256(raw)
							record.Reuse = append(record.Reuse, step{Name: name, Severity: evaluation.Severity, Status: output.Status, Diagnostics: output.Diagnostics, PatchDigest: hex.EncodeToString(digest[:]), ReusedFromAlertID: output.ReusedFromAlertID})
						}
					}
				}
			}
		}
		report = append(report, record)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if os.WriteFile(reportPath, raw, 0o600) != nil { //nolint:gosec // G703: 操作者显式指定的集成报告路径。
		t.Fatal("write private report failed")
	}
	t.Logf("read-only replay completed: tenant=%s source=%s version=%d events=%d", fixture.Tenant, fixture.Source.EventSourceID, fixture.Source.Version, len(report))
}

// 包装真实 Reader 仅计数；查询条件、返回结果和错误均原样透传。
type diagnosticsK8sReader struct {
	next  enrich.K8sReader
	calls map[string]int
}

func (r *diagnosticsK8sReader) FindK8sInstance(ctx context.Context, tenant, model string, dimensions domain.DimensionMap) (enrich.Instance, bool, error) {
	r.calls[model]++
	return r.next.FindK8sInstance(ctx, tenant, model, dimensions)
}
func mustLivePayload(t *testing.T, data domain.JSONObject) enrich.Payload {
	t.Helper()
	payload, err := enrich.DecodePayload(data)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
