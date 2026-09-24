// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package preview 执行不保存的丰富模拟，只依赖来源、告警和外部资源的读取端口。
package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/custom"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/eventsource"
	"linkd/internal/jsonpath"
	"linkd/internal/store"
)

// Error 给 HTTP 边界提供稳定状态码；Message 不包含依赖响应或连接凭据。
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Request 同时支持已有告警身份和临时 JSON，两种输入必须互斥。
type Request struct {
	BKTenantID    string               `json:"bk_tenant_id"`
	EventSourceID string               `json:"event_source_id"`
	Input         Input                `json:"input"`
	Enrich        *config.EnrichConfig `json:"enrich,omitempty"`
}

// Input 的 Alert 为原始 JSON，避免把省略和零值混淆。
type Input struct {
	AlertID string          `json:"alert_id,omitempty"`
	Alert   json.RawMessage `json:"alert,omitempty"`
}

// Change 明确标识缺失和 null 的差异。
type Change struct {
	Path         string `json:"path"`
	Before       any    `json:"before"`
	After        any    `json:"after"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
}

// Response 包含一次模拟的配置身份、步骤输出及按需合成视图。
type Response struct {
	Version         int64               `json:"event_source_version"`
	ConfigDigest    string              `json:"config_digest"`
	Original        map[string]any      `json:"original"`
	EnrichStatus    domain.EnrichStatus `json:"enrich_status"`
	Enrich          domain.JSONObject   `json:"enrich"`
	EffectiveAlert  map[string]any      `json:"effective_alert"`
	Changes         []Change            `json:"changes"`
	PreviousChanges []Change            `json:"previous_changes"`
	Trace           []ProcessorTrace    `json:"trace"`
}

// ProcessorTrace 保留处理器与规则执行记录，不包含完整外部响应。
type ProcessorTrace struct {
	Processor   string              `json:"processor"`
	Status      domain.EnrichStatus `json:"status"`
	Rules       []enrich.Trace      `json:"rules"`
	Diagnostics []enrich.Diagnostic `json:"diagnostics,omitempty"`
}

// SourceReader 只能读取已发布配置，不能发布或修改配置。
type SourceReader interface {
	Get(context.Context, string) (eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// AlertReader 只能按明确租户与告警身份读取一条告警。
type AlertReader func(context.Context, string, string) (domain.Alert, error)

// OpenEnricher 装配与正式执行相同的引擎，并返回调用结束时的资源释放函数。
// Enricher 是预览消费的只读执行端口，由控制面装配实现。
type Enricher interface {
	Enrich(context.Context, enrich.Input) (enrich.Result, error)
}

type OpenEnricher func(context.Context, config.EventSource) (Enricher, func() error, error)

// Service 的并发预算与正式 Lifecycle 隔离，防止调试占满正式工作池。
type Service struct {
	sources SourceReader
	read    AlertReader
	open    OpenEnricher
	slots   chan struct{}
}

// New 注入窄读取端口；服务可并发调用。
func New(sources SourceReader, read AlertReader, open OpenEnricher) *Service {
	return &Service{sources: sources, read: read, open: open, slots: make(chan struct{}, 4)}
}

// Preview 固定一次来源发布快照；输入历史丰富仅参与对比，不作为本轮规则输入。
func (s *Service) Preview(ctx context.Context, request Request) (Response, error) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Response{}, &Error{429, "preview capacity exceeded"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if domain.ValidateIdentityPart("bk_tenant_id", request.BKTenantID, 64) != nil || domain.ValidateIdentityPart("event_source_id", request.EventSourceID, 32) != nil {
		return Response{}, &Error{400, "bk_tenant_id and event_source_id are required"}
	}
	hasJSON := len(request.Input.Alert) > 0
	if (request.Input.AlertID != "") == hasJSON {
		return Response{}, &Error{400, "choose exactly one of input.alert_id and input.alert"}
	}
	if len(request.Input.AlertID) > 256 {
		return Response{}, &Error{400, "alert_id too long"}
	}
	record, err := s.sources.Get(ctx, request.EventSourceID)
	if err != nil {
		return Response{}, readError(ctx, err)
	}
	if record.Deleted || record.Published <= 0 {
		return Response{}, &Error{404, "published event source not found"}
	}
	release, err := s.sources.GetRelease(ctx, record.ID, record.Published)
	if err != nil {
		return Response{}, readError(ctx, err)
	}
	if release.Deleted || release.Spec.EventSourceID != request.EventSourceID {
		return Response{}, &Error{404, "published event source not found"}
	}
	source := release.Spec
	if source.RelatedTenantID != "" && source.RelatedTenantID != request.BKTenantID {
		return Response{}, &Error{400, "tenant does not match event source"}
	}
	if request.Enrich != nil {
		source.Enrich = *request.Enrich
	}
	if err := source.Enrich.Validate(); err != nil {
		return Response{}, &Error{422, "invalid enrich configuration: " + safeConfigError(source.Enrich, err)}
	}
	var raw []byte
	if hasJSON {
		raw = request.Input.Alert
	} else {
		if s.read == nil {
			return Response{}, &Error{503, "alert reader unavailable"}
		}
		alert, err := s.read(ctx, request.BKTenantID, request.Input.AlertID)
		if err != nil {
			return Response{}, readError(ctx, err)
		}
		if alert.AlertID != request.Input.AlertID {
			return Response{}, &Error{502, "alert identity mismatch"}
		}
		raw, err = json.Marshal(alert)
		if err != nil {
			return Response{}, &Error{400, "invalid stored alert"}
		}
	}
	var original map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&original); err != nil || original == nil {
		return Response{}, &Error{400, "alert must be an object"}
	}
	if err := jsonpath.ValidateTree(original); err != nil {
		return Response{}, &Error{400, "alert exceeds input limits"}
	}
	for key, want := range map[string]string{"bk_tenant_id": request.BKTenantID, "event_source_id": request.EventSourceID} {
		if got, ok := original[key]; ok && got != want {
			return Response{}, &Error{400, "alert tenant or event source does not match request"}
		}
		original[key] = want
	}
	var alert domain.Alert
	if err := json.Unmarshal(raw, &alert); err != nil {
		return Response{}, &Error{400, "invalid alert field type"}
	}
	alert.BKTenantID = request.BKTenantID
	alert.EventSourceID = request.EventSourceID
	if err := alert.Labels.Validate(); err != nil {
		return Response{}, &Error{400, "invalid alert labels"}
	}
	previous := alert.Clone()
	delete(original, "enrich")
	delete(original, "enrich_status")
	alert.Enrich = domain.JSONObject{}
	alert.EnrichStatus = domain.EnrichStatusPending
	engine, closeRuntime, err := s.open(ctx, source)
	if err != nil {
		return Response{}, readError(ctx, err)
	}
	defer func() { _ = closeRuntime() }()
	result, err := engine.Enrich(ctx, enrich.Input{Alert: alert, Preview: true})
	if err != nil {
		return Response{}, readError(ctx, err)
	}
	effective, trace, err := compose(original, result.Data)
	if err != nil {
		return Response{}, &Error{500, "invalid enrich result"}
	}
	previousEffective, _, err := compose(original, previous.Enrich)
	if err != nil {
		return Response{}, &Error{400, "invalid previous enrichment"}
	}
	encoded, _ := json.Marshal(source.Redacted().Enrich)
	digest := sha256.Sum256(encoded)
	return Response{Version: release.Version, ConfigDigest: hex.EncodeToString(digest[:]), Original: original, EnrichStatus: result.Status, Enrich: result.Data, EffectiveAlert: effective, Changes: Diff(original, effective), PreviousChanges: Diff(previousEffective, effective), Trace: trace}, nil
}

func readError(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return &Error{504, "preview timed out or cancelled"}
	}
	if errors.Is(err, eventsource.ErrNotFound) || errors.Is(err, store.ErrNotFound) {
		return &Error{404, "alert or event source not found"}
	}
	return &Error{502, "preview read dependency unavailable"}
}

func safeConfigError(c config.EnrichConfig, _ error) string {
	for i, processor := range c.Processors {
		if processor.Type == "cmdb" || processor.Type == "fields" {
			if _, err := custom.Compile(processor.Type, processor.Config); err != nil {
				return fmt.Sprintf("processors[%d]: %s", i, err)
			}
		}
	}
	return "check processor configuration"
}

func compose(original map[string]any, payload domain.JSONObject) (map[string]any, []ProcessorTrace, error) {
	current := jsonpath.Clone(original).(map[string]any)
	trace := []ProcessorTrace{}
	if len(payload) == 0 {
		return current, trace, nil
	}
	parsed, err := enrich.DecodePayload(payload)
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range parsed.Processors {
		for name, result := range entry {
			trace = append(trace, ProcessorTrace{Processor: name, Status: result.Status, Rules: result.Trace, Diagnostics: result.Diagnostics})
			if result.Status != domain.EnrichStatusSucceeded && result.Status != domain.EnrichStatusPartial {
				continue
			}
			patches := result.Patches
			if patches == nil {
				patches, err = kingeye.ProjectValue(name, result.Value)
				if err != nil {
					return nil, nil, err
				}
			}
			current, err = domain.ApplyEnrichPatches(current, patches)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return current, trace, nil
}

// Diff 按路径排序并保留存在性；数组作为完整叶值，避免索引移动造成含糊差异。
func Diff(before, after map[string]any) []Change {
	result := []Change{}
	var walk func(string, any, bool, any, bool)
	walk = func(path string, a any, ae bool, b any, be bool) {
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			names := make([]string, 0, len(keys))
			for k := range keys {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				av, ax := am[k]
				bv, bx := bm[k]
				encoded, _ := json.Marshal(k)
				walk(path+"["+string(encoded)+"]", av, ax, bv, bx)
			}
			return
		}
		if ae != be || !reflect.DeepEqual(a, b) {
			result = append(result, Change{Path: path, Before: a, After: b, BeforeExists: ae, AfterExists: be})
		}
	}
	walk("$", before, true, after, true)
	return result
}
