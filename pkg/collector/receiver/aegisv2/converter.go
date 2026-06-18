package aegisv2

import (
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var errMappingNotImplemented = errors.New("aegisv2 mapping rule is not implemented")

const (
	defaultServiceName = "unknown_service"
	// maxUpsertDepth 限制 upsertAny 对象展开的最大递归深度，超出后整体 JSON 序列化，防止无界栈增长。
	maxUpsertDepth = 10
)

func decodeTraces(buf []byte) (ptrace.Traces, bool, error) {
	return decodeTracesWithTraceID(buf, pcommon.TraceID{})
}

func decodeTracesWithTraceID(buf []byte, requestTraceID pcommon.TraceID) (ptrace.Traces, bool, error) {
	payload, handled, err := parseCollectPayload(buf)
	if !handled || err != nil {
		return ptrace.Traces{}, handled, err
	}
	records, err := parseD2Records(payload.D2)
	if err != nil {
		return ptrace.Traces{}, true, err
	}
	traces, err := splitTraces(payload, records, requestTraceID)
	return traces, true, err
}

func decodeMetrics(buf []byte) (pmetric.Metrics, bool, error) {
	_, handled, err := parseCollectPayload(buf)
	if !handled || err != nil {
		return pmetric.Metrics{}, handled, err
	}
	metrics, err := splitMetrics()
	return metrics, true, err
}

func decodeLogs(buf []byte) (plog.Logs, bool, error) {
	_, handled, err := parseCollectPayload(buf)
	if !handled || err != nil {
		return plog.Logs{}, handled, err
	}
	logs, err := splitLogs()
	return logs, true, err
}

func parseCollectPayload(buf []byte) (collectPayload, bool, error) {
	var payload collectPayload
	if err := json.Unmarshal(buf, &payload); err != nil {
		return collectPayload{}, false, nil
	}
	if payload.Topic == "" && payload.Scheme == "" && len(payload.D2) == 0 {
		return collectPayload{}, false, nil
	}
	return payload, true, nil
}

func parseD2Records(raw json.RawMessage) ([]d2Record, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var records []d2Record
	return records, json.Unmarshal(raw, &records)
}

// splitTraces 将 aegisv2 Payload 转换为 OTel Traces。
func splitTraces(payload collectPayload, records []d2Record, traceID pcommon.TraceID) (ptrace.Traces, error) {
	traces := ptrace.NewTraces()
	if len(records) == 0 {
		return traces, nil
	}

	rs := traces.ResourceSpans().AppendEmpty()
	rAttrs := rs.Resource().Attributes()
	ensureDefaultServiceName(rAttrs)
	upsertString(rAttrs, "aegisv2.topic", payload.Topic)
	upsertString(rAttrs, "aegisv2.scheme", payload.Scheme)
	upsertString(rAttrs, "version", payload.Bean.Version)
	upsertString(rAttrs, "aid", payload.Bean.Aid)
	upsertString(rAttrs, "env", payload.Bean.Env)
	upsertString(rAttrs, "platform", payload.Bean.Platform)
	upsertString(rAttrs, "netType", payload.Bean.NetType)
	upsertString(rAttrs, "vp", payload.Bean.VP)
	upsertString(rAttrs, "sr", payload.Bean.SR)
	upsertString(rAttrs, "referer", payload.Bean.Referer)
	upsertString(rAttrs, "session.id", records[0].Fields.Session.ID)

	scopeSpans := rs.ScopeSpans().AppendEmpty()
	scopeSpans.Scope().SetName("aegisv2.collect")
	if payload.Bean.Version != "" {
		scopeSpans.Scope().SetVersion(payload.Bean.Version)
	}

	now := pcommon.NewTimestampFromTime(time.Now())
	if traceID == (pcommon.TraceID{}) {
		traceID = random.TraceID()
	}

	for _, record := range records {
		if record.Fields.Action.IsValid() {
			appendActionSpan(scopeSpans, traceID, now, payload, record)
		}
		msgs := record.Message
		if len(msgs) == 0 {
			recordEmptyMessageDrop()
			continue
		}
		for _, msg := range msgs {
			event := aegisEvent{record, msg}
			ctx := BuildContext{
				ScopeSpans: scopeSpans,
				TraceID:    traceID,
				Now:        now,
				Payload:    payload,
				Record:     record,
				Msg:        msg,
				EventType:  event.EventType(),
			}
			if err := buildSpanWithRegistry(defaultBuilderRegistry, &ctx); err != nil {
				return ptrace.Traces{}, err
			}
		}
	}

	if logger.LoggerLevel() == logger.DebugLevelDesc {
		if b, err := json.Marshal(records); err == nil {
			logger.Debugf("aegisv2/splitTraces: %s", b)
		}
	}
	return traces, nil
}

func splitMetrics() (pmetric.Metrics, error) {
	return pmetric.NewMetrics(), errors.Wrap(errMappingNotImplemented, "metrics mapping")
}

func splitLogs() (plog.Logs, error) {
	return plog.NewLogs(), errors.Wrap(errMappingNotImplemented, "logs mapping")
}
