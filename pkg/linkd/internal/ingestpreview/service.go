// Package ingestpreview 在请求内执行标准化和 KAC 调试字段转换，仅持有配置读取端口。
package ingestpreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/runtimeconfig"
)

// Error 的信息只包含输入位置和稳定错误分类，不暴露原始 payload 或依赖响应。
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Calculation 只允许标准化字段，禁止消费配置、丰富和输出资源进入请求。
type Calculation struct {
	Cleaner struct {
		Type string `json:"type"`
	} `json:"cleaner"`
	FingerprintMode   string            `json:"fingerprint_mode"`
	FingerprintField  string            `json:"fingerprint_field,omitempty"`
	FingerprintFields []string          `json:"fingerprint_fields,omitempty"`
	SeverityMapping   map[string]string `json:"severity_mapping,omitempty"`
	DefaultSeverity   string            `json:"default_severity,omitempty"`
}

// Source 在 draft 计算配置与明确的 published 版本之间二选一。
type Source struct {
	Kind        string       `json:"kind"`
	Version     int64        `json:"version,omitempty"`
	Calculation *Calculation `json:"calculation,omitempty"`
}

// KACSource 由受信 KAC 后端从同租户来源记录生成，不由插件覆盖。
type KACSource struct {
	ID   string `json:"source_id"`
	Name string `json:"source_name"`
}

// Sample 的时间和身份固定于首次接收，不以重试时间替代发生时间的缺省值。
type Sample struct {
	ID         string          `json:"sample_id"`
	ReceivedAt time.Time       `json:"received_at"`
	Payload    json.RawMessage `json:"payload"`
}

// Request 是同步调试输入，不受理保存、发布配置或正式处置请求。
type Request struct {
	BKTenantID    string    `json:"bk_tenant_id"`
	EventSourceID string    `json:"event_source_id"`
	SessionID     string    `json:"session_id"`
	Source        Source    `json:"source"`
	KACSource     KACSource `json:"kac_source"`
	FieldCatalog  []string  `json:"field_catalog"`
	Samples       []Sample  `json:"samples"`
	Output        string    `json:"output"`
}

// Diagnostic 标记无法从来源单条输入确定的字段，不把缺省伪装成丰富失败。
type Diagnostic struct {
	Code   string   `json:"code"`
	Fields []string `json:"fields"`
}

// Item 按 sample 与标准等级返回调试结果，KAC 负责保存及页面封装。
type Item struct {
	SampleID    string             `json:"sample_id"`
	Severity    string             `json:"severity"`
	Action      domain.EventAction `json:"action"`
	Passed      bool               `json:"passed"`
	KACAlarm    map[string]any     `json:"kac_alarm"`
	Diagnostics []Diagnostic       `json:"diagnostics"`
}

// Response 不承诺正式告警已经创建；Version 只用于真实 published 配置。
type Response struct {
	ConfigOrigin   string `json:"config_origin"`
	ConfigDigest   string `json:"config_digest"`
	SeverityDigest string `json:"severity_digest"`
	Version        int64  `json:"event_source_version,omitempty"`
	Items          []Item `json:"items"`
}

// SourceReader 只允许读取来源记录和明确版本，不提供发布或任务运行端口。
type SourceReader interface {
	Get(context.Context, string) (eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// Service 的预算与正式处理池隔离；不存在丰富、业务写入和消息发布依赖。
type Service struct {
	sources  SourceReader
	severity func() runtimeconfig.Snapshot
	slots    chan struct{}
}

// New 注入必要的配置读取能力，所有计算均在请求内完成。
func New(sources SourceReader, severity func() runtimeconfig.Snapshot) *Service {
	return &Service{sources: sources, severity: severity, slots: make(chan struct{}, 4)}
}

// Preview 固定一次配置快照；任何样本硬错误都拒绝整批，不返回可误标通过的部分结果。
func (s *Service) Preview(ctx context.Context, request Request) (Response, error) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Response{}, &Error{429, "preview capacity exceeded"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Response{}, &Error{504, "preview cancelled"}
	}
	for key, value := range map[string]string{"bk_tenant_id": request.BKTenantID, "event_source_id": request.EventSourceID, "session_id": request.SessionID, "source_id": request.KACSource.ID} {
		limit := 128
		if key == "event_source_id" {
			limit = 32
		}
		if key == "bk_tenant_id" {
			limit = 64
		}
		if err := domain.ValidateIdentityPart(key, value, limit); err != nil {
			return Response{}, &Error{400, key + " is required or invalid"}
		}
	}
	if request.Output != "kac_alarm" || request.KACSource.Name == "" || len(request.KACSource.Name) > 256 || len(request.Samples) < 1 || len(request.Samples) > 10 || len(request.FieldCatalog) > 128 {
		return Response{}, &Error{400, "invalid preview output, source metadata or sample limits"}
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > 1<<20 {
		return Response{}, &Error{400, "preview exceeds input limits"}
	}
	snapshot := s.severity()
	source, err := s.source(ctx, request, snapshot.Severity)
	if err != nil {
		return Response{}, err
	}
	factory, err := cleaner.NewNormalizationFactory(source, snapshot.Severity)
	if err != nil {
		return Response{}, &Error{422, "invalid normalization configuration"}
	}
	configJSON, _ := json.Marshal(struct {
		Source   config.EventSource
		Metadata KACSource
		Catalog  []string
	}{source, request.KACSource, request.FieldCatalog})
	configHash := sha256.Sum256(configJSON)
	response := Response{ConfigOrigin: request.Source.Kind, ConfigDigest: hex.EncodeToString(configHash[:]), SeverityDigest: snapshot.Digest, Version: source.Version, Items: []Item{}}
	if request.Source.Kind == "draft" {
		// 私有 Event 使用工厂要求的版本占位；响应不能将其描述成实际发布版本。
		response.Version = 0
	}
	seen := map[string]bool{}
	for index, sample := range request.Samples {
		if ctx.Err() != nil {
			return Response{}, &Error{504, "preview timed out or cancelled"}
		}
		if domain.ValidateIdentityPart("sample_id", sample.ID, 128) != nil || sample.ReceivedAt.IsZero() || seen[sample.ID] {
			return Response{}, &Error{400, fmt.Sprintf("invalid samples[%d] identity or timestamp", index)}
		}
		seen[sample.ID] = true
		digest := sha256.Sum256(sample.Payload)
		raw := cleaner.RawEventMessage{RecordID: "preview:" + request.SessionID + ":" + sample.ID + ":" + hex.EncodeToString(digest[:]), BKTenantID: request.BKTenantID, EventSourceID: request.EventSourceID, ReceivedAt: sample.ReceivedAt, Payload: sample.Payload}
		draft, err := (cleaner.StandardCleaner{}).Clean(ctx, raw)
		if err != nil {
			return Response{}, &Error{422, fmt.Sprintf("samples[%d] invalid standard payload", index)}
		}
		// related_tenant_id 在正式工厂中可覆盖 payload；调试必须先拒绝错租户，不能利用覆盖掩盖输入错误。
		if draft.BKTenantID != request.BKTenantID || draft.SourceEventID == "" || draft.SourceAlertID == "" {
			return Response{}, &Error{422, fmt.Sprintf("samples[%d] tenant or source identity mismatch", index)}
		}
		event, err := factory.Build(raw, draft)
		if err != nil {
			return Response{}, &Error{422, fmt.Sprintf("samples[%d] normalization failed", index)}
		}
		if len(response.Items)+len(event.Evaluations) > 64 {
			return Response{}, &Error{400, "evaluation result limit exceeded"}
		}
		for _, evaluation := range event.Evaluations {
			fields, err := kachook.DebugFields(event, evaluation, request.KACSource.ID, request.KACSource.Name, request.FieldCatalog, snapshot.KACLevel)
			if err != nil {
				return Response{}, &Error{422, fmt.Sprintf("samples[%d] KAC field conversion failed", index)}
			}
			diagnostics := []Diagnostic{}
			if evaluation.Action != domain.EventActionTriggered {
				diagnostics = append(diagnostics, Diagnostic{"history_not_available", []string{"begin_at", "status", "close_time"}})
			}
			response.Items = append(response.Items, Item{sample.ID, evaluation.Severity, evaluation.Action, true, fields, diagnostics})
		}
	}
	resultJSON, err := json.Marshal(response)
	if err != nil || len(resultJSON) > 1<<20 {
		return Response{}, &Error{422, "preview result exceeds output limits"}
	}
	return response, nil
}

func (s *Service) source(ctx context.Context, r Request, severity config.SeverityConfig) (config.EventSource, error) {
	var source config.EventSource
	switch r.Source.Kind {
	case "draft":
		if r.Source.Calculation == nil || r.Source.Version != 0 {
			return source, &Error{400, "draft requires calculation without version"}
		}
		c := r.Source.Calculation
		source = config.EventSource{Cleaner: config.CleanerConfig{Type: c.Cleaner.Type}, FingerprintMode: c.FingerprintMode, FingerprintField: c.FingerprintField, FingerprintFields: c.FingerprintFields, SeverityMapping: c.SeverityMapping, DefaultSeverity: c.DefaultSeverity}
	case "published":
		if r.Source.Calculation != nil || r.Source.Version <= 0 || s.sources == nil {
			return source, &Error{400, "published requires an explicit version"}
		}
		record, err := s.sources.Get(ctx, r.EventSourceID)
		if err != nil {
			return source, readError(ctx, err)
		}
		if record.ID != r.EventSourceID || record.Deleted || record.Published < r.Source.Version {
			return source, &Error{404, "published source not found"}
		}
		release, err := s.sources.GetRelease(ctx, r.EventSourceID, r.Source.Version)
		if err != nil {
			return source, readError(ctx, err)
		}
		if release.Version != r.Source.Version || release.Deleted || release.Spec.EventSourceID != r.EventSourceID || release.Spec.RelatedTenantID != r.BKTenantID {
			return source, &Error{400, "published source tenant mismatch"}
		}
		spec := release.Spec
		// 仅复制计算字段，不让真实 Release 的丰富、消费或输出资源进入调试运行时。
		source = config.EventSource{Version: release.Version, Cleaner: config.CleanerConfig{Type: spec.Cleaner.Type}, FingerprintMode: spec.FingerprintMode, FingerprintField: spec.FingerprintField, FingerprintFields: spec.FingerprintFields, SeverityMapping: spec.SeverityMapping, DefaultSeverity: spec.DefaultSeverity}
	default:
		return source, &Error{400, "source kind must be draft or published"}
	}
	source.EventSourceID = r.EventSourceID
	source.RelatedTenantID = r.BKTenantID
	source = source.WithDefaults()
	if err := source.ValidateNormalization(severity); err != nil {
		return source, &Error{422, "invalid normalization configuration"}
	}
	return source, nil
}

func readError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &Error{504, "preview timed out or cancelled"}
	}
	if errors.Is(err, eventsource.ErrNotFound) {
		return &Error{404, "published source not found"}
	}
	return &Error{http.StatusBadGateway, "preview configuration unavailable"}
}
