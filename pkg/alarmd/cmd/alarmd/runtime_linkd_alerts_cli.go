package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

// linkdAlertsDefaultWindow is how far back an alert is looked for when the
// read names no time range: the link's own bound on a range is the link's to
// apply, and its refusal is passed back as it says it.
const linkdAlertsDefaultWindow = 24 * time.Hour

// linkdAlertsTimeout bounds one linkd.alerts read: an alert's page, then its
// logs and events, each one Console call.
const linkdAlertsTimeout = 110 * time.Second

// linkdAlertsAnswer is linkd.alerts' answer. By strategy it is the link's
// reconciliation of the strategy's active alerts; by alert, the link's alert
// records with each one's logs and events. EventSourceID is the source the
// read was made for, SourceByTopic how it was chosen.
type linkdAlertsAnswer struct {
	Mode          string                         `json:"mode"`
	TenantID      string                         `json:"tenant_id"`
	EventSourceID string                         `json:"event_source_id"`
	SourceByTopic *fleet.LinkdSourceByTopicFacts `json:"source_by_topic,omitempty"`
	Strategy      *linkdStrategyAlerts           `json:"strategy,omitempty"`
	From          string                         `json:"from,omitempty"`
	To            string                         `json:"to,omitempty"`
	Alerts        []linkdAlertRecord             `json:"alerts,omitempty"`
	NextCursor    string                         `json:"next_cursor,omitempty"`
	Warnings      []string                       `json:"warnings,omitempty"`
}

// linkdStrategyAlerts is one strategy's active alerts as the link reconciles
// them: the fingerprints in its open set with an active alert (members),
// those with an active alert the set lacks (missing_redis), and those the set
// holds with no active alert (redis_only).
type linkdStrategyAlerts struct {
	StrategyID   string             `json:"strategy_id"`
	Members      []string           `json:"members"`
	MissingRedis []string           `json:"missing_redis"`
	RedisOnly    []string           `json:"redis_only"`
	Alerts       []openalerts.Alert `json:"alerts"`
}

// linkdAlertRecord is one of the link's alerts: what a reader compares,
// lifted from the record, then the record itself, its logs and its events,
// each as the link wrote it after the channel's redaction.
type linkdAlertRecord struct {
	AlertID          string            `json:"alert_id"`
	Status           string            `json:"status,omitempty"`
	EventSourceID    string            `json:"event_source_id,omitempty"`
	Severity         string            `json:"severity,omitempty"`
	StrategyID       string            `json:"strategy_id,omitempty"`
	StrategyVersion  string            `json:"strategy_version,omitempty"`
	Enrich           json.RawMessage   `json:"enrich,omitempty"`
	Record           json.RawMessage   `json:"record"`
	Logs             []json.RawMessage `json:"logs"`
	LogsNextCursor   string            `json:"logs_next_cursor,omitempty"`
	Events           []json.RawMessage `json:"events"`
	EventsNextCursor string            `json:"events_next_cursor,omitempty"`
}

// linkdConsoleReader is what linkd.alerts reads through: the process's own
// Console client, with the binding it resolved.
type linkdConsoleReader interface {
	Binding(context.Context) (openalerts.TargetBinding, error)
	Reconcile(context.Context, openalerts.StrategyKey) (openalerts.Reconciliation, error)
	Entities(context.Context, string, url.Values) (openalerts.EntityPage, error)
	Alert(context.Context, string, string) (json.RawMessage, error)
	Record() openalerts.ConsoleRecord
}

// linkdConsoleOf is the Console as linkd.alerts reads it: none when the
// process holds none, never an interface around a nil client.
func linkdConsoleOf(console *openalerts.HTTPReconciler) linkdConsoleReader {
	if console == nil {
		return nil
	}
	return console
}

// cliLinkdAlertsOperation reads the link's alerts through the Console this
// process already holds, with the token it already has: GETs only. Nil
// leaves the operation listed and unavailable with its reason.
func cliLinkdAlertsOperation(console linkdConsoleReader) obchannel.Operation {
	text := func(description string, max int) obchannel.Field {
		return obchannel.Field{Type: "string", Description: description, MinLength: 1, MaxLength: max, Pattern: "^[^\\x00-\\x1f\\x7f]+$"}
	}
	minLimit := int64(1)
	return obchannel.Operation{ID: "linkd.alerts",
		Summary: "经本部署已有的 linkd Console 只读 linkd 的告警，二选一问法：按策略（strategy_id）读 linkd 对这条策略的对账——当前活动告警按指纹列出，" +
			"每个指纹在不在活动集合里（members / missing_redis / redis_only）；按告警（alert_id 或 source_alert_id）读告警记录（状态 active/recovered/closed、" +
			"事件源、策略标签与丰富结果）及它的生命周期流水与每个事件的处理状态和结局（alert_created / alert_recovered 等）。恢复和关闭只能按告警问：" +
			"按策略读的是活动告警，空不等于已关闭。事件源用本部署绑定选定的那个（source_by_topic）。时间范围默认最近 24 小时，from/to/limit 原样交给 linkd，" +
			"它拒绝时原话带回。",
		EvidenceScope: "linkd_alerts", ExecutionTimeout: linkdAlertsTimeout,
		Fields: map[string]obchannel.Field{
			"strategy_id":     {Type: "string", Description: "按策略问：策略 ID。", MinLength: 1, MaxLength: 20, Pattern: "^[1-9][0-9]*$", Source: "strategy.get"},
			"alert_id":        text("按告警问：linkd 的告警 ID。", 1024),
			"source_alert_id": text("按告警问：本部署发出的告警身份（source_alert_id）。", 1024),
			"tenant":          text("租户，默认 system。", 256),
			"from":            text("按告警问的起点，RFC 3339；默认 to 前 24 小时。", 64),
			"to":              text("按告警问的终点，RFC 3339；默认现在。", 64),
			"limit":           {Type: "integer", Description: "按 source_alert_id 问的每页条数，原样交给 linkd；不给用 linkd 的默认。按 alert_id 问只读那一条。", Minimum: &minLimit},
			"cursor":          text("按 source_alert_id 问时：告警段上一页的 next_cursor（按 alert_id 问只有一条，没有下一页）。", 16384),
			"logs_cursor":     text("只给 alert_id 时：流水段上一页的 logs_next_cursor。", 16384),
			"events_cursor":   text("只给 alert_id 时：事件段上一页的 events_next_cursor。", 16384),
		},
		OutputSchema: obchannel.SchemaOf(linkdAlertsAnswer{}),
		Limits: map[string]any{"redis_commands": 0, "console_calls": "1 by strategy; 1 + 2 per alert by alert",
			"time_range": "the link's own bound; refused by the link, not here", "redaction_contract": redact.Contract},
		Availability: func() obchannel.Availability {
			if console == nil {
				return obchannel.Availability{Reason: "linkd_console_not_configured: this process reads no linkd Console"}
			}
			return obchannel.Availability{Available: true}
		},
		Validate: func(p obchannel.Params) error {
			asked := 0
			for _, key := range []string{"strategy_id", "alert_id", "source_alert_id"} {
				if p.String(key) != "" {
					asked++
				}
			}
			if asked != 1 {
				return errors.New("name exactly one of strategy_id, alert_id or source_alert_id")
			}
			if (p.String("logs_cursor") != "" || p.String("events_cursor") != "") && p.String("alert_id") == "" {
				return errors.New("logs_cursor and events_cursor continue one alert's segments: give its alert_id")
			}
			if p.String("cursor") != "" && p.String("alert_id") != "" {
				return errors.New("an alert_id names one alert, which has no next page: cursor continues a source_alert_id read")
			}
			return nil
		},
		Run: func(ctx context.Context, p obchannel.Params) obchannel.Outcome {
			return readLinkdAlerts(ctx, console, p, time.Now)
		}}
}

func readLinkdAlerts(ctx context.Context, console linkdConsoleReader, p obchannel.Params, now func() time.Time) obchannel.Outcome {
	tenant := p.String("tenant")
	if tenant == "" {
		tenant = "system"
	}
	binding, err := console.Binding(ctx)
	if err != nil {
		return linkdAlertsFailure(err)
	}
	answer := linkdAlertsAnswer{TenantID: tenant, EventSourceID: binding.EventSourceID,
		SourceByTopic: linkdSourceByTopicFacts(console.Record().SourceByTopic)}
	scrub := redact.New()
	if id := p.String("strategy_id"); id != "" {
		answer.Mode = "strategy"
		result, err := console.Reconcile(ctx, openalerts.StrategyKey{TenantID: tenant, StrategyID: id})
		if err != nil {
			return linkdAlertsFailure(err)
		}
		answer.Strategy = &linkdStrategyAlerts{StrategyID: id, Members: nonNil(result.Members), MissingRedis: nonNil(result.Missing),
			RedisOnly: nonNil(result.Suppressed), Alerts: result.Alerts}
		if answer.Strategy.Alerts == nil {
			answer.Strategy.Alerts = []openalerts.Alert{}
		}
		limitations := []string{
			"Active alerts only: a recovered or closed alert is no longer listed. Read it by alert_id or source_alert_id; an empty strategy does not mean closed."}
		if result.Truncated {
			limitations = append(limitations, "Linkd's answer was incomplete or over 10000 rows / 5000 active alerts: only the first part is listed, and an alert not listed may still be active.")
		}
		return obchannel.Outcome{Value: answer, Complete: !result.Truncated, Limitations: limitations}
	}
	answer.Mode = "alert"
	query := url.Values{"bk_tenant_id": {tenant}, "event_source_id": {binding.EventSourceID}}
	to, from := p.String("to"), p.String("from")
	if to == "" {
		to = now().UTC().Format(time.RFC3339)
	}
	if from == "" {
		if end, err := time.Parse(time.RFC3339, to); err == nil {
			from = end.Add(-linkdAlertsDefaultWindow).UTC().Format(time.RFC3339)
		}
	}
	answer.From, answer.To = from, to
	query.Set("to", to)
	if from != "" {
		query.Set("from", from)
	}
	var page openalerts.EntityPage
	if id := p.String("alert_id"); id != "" {
		// The link's list takes no alert id and would answer every alert in
		// range; the alert is read on the route that addresses it.
		item, err := console.Alert(ctx, tenant, id)
		switch {
		case errors.Is(err, openalerts.ErrAlertNotFound):
			answer.Alerts = []linkdAlertRecord{}
			return obchannel.Outcome{Value: answer, Complete: true, Limitations: []string{
				"The link has no alert with this alert_id."}}
		case err != nil:
			return linkdAlertsFailure(err)
		}
		page.Items = []json.RawMessage{item}
	} else {
		query.Set("source_alert_id", p.String("source_alert_id"))
		if limit := p.Int("limit", 0); limit > 0 {
			query.Set("limit", strconv.Itoa(limit))
		}
		if cursor := p.String("cursor"); cursor != "" {
			query.Set("cursor", cursor)
		}
		var err error
		if page, err = console.Entities(ctx, openalerts.EntityAlerts, query); err != nil {
			return linkdAlertsFailure(err)
		}
	}
	answer.NextCursor, answer.Warnings = page.NextCursor, page.Warnings
	answer.Alerts = []linkdAlertRecord{}
	for _, item := range page.Items {
		record := liftLinkdAlert(scrub.JSON(item))
		if record.AlertID == "" {
			continue
		}
		related := url.Values{"bk_tenant_id": {tenant}, "from": {from}, "to": {to}}
		if from == "" {
			related.Del("from")
		}
		logs := cloneValues(related)
		logs.Set("alert_id", record.AlertID)
		if cursor := p.String("logs_cursor"); cursor != "" {
			logs.Set("cursor", cursor)
		}
		logPage, err := console.Entities(ctx, openalerts.EntityAlertLogs, logs)
		if err != nil {
			return linkdAlertsFailure(err)
		}
		events := cloneValues(related)
		events.Set("related_alert_id", record.AlertID)
		if cursor := p.String("events_cursor"); cursor != "" {
			events.Set("cursor", cursor)
		}
		eventPage, err := console.Entities(ctx, openalerts.EntityEvents, events)
		if err != nil {
			return linkdAlertsFailure(err)
		}
		record.Logs, record.LogsNextCursor = scrubAll(scrub, logPage.Items), logPage.NextCursor
		record.Events, record.EventsNextCursor = scrubAll(scrub, eventPage.Items), eventPage.NextCursor
		answer.Alerts = append(answer.Alerts, record)
	}
	return obchannel.Outcome{Value: answer, Complete: answer.NextCursor == "", Limitations: []string{
		"Records are the link's as it wrote them, after the channel's redaction. The time range and page size are the link's to accept: a refusal is its own words."}}
}

// liftLinkdAlert takes what a reader compares out of one alert item
// ({tenantId, id, timestamp, summary, payload}) and keeps the item whole.
func liftLinkdAlert(item json.RawMessage) linkdAlertRecord {
	var parsed struct {
		ID      string `json:"id"`
		Payload struct {
			AlertID       string                     `json:"alert_id"`
			Status        string                     `json:"status"`
			EventSourceID string                     `json:"event_source_id"`
			Severity      string                     `json:"severity"`
			Labels        map[string]json.RawMessage `json:"labels"`
			Enrich        json.RawMessage            `json:"enrich"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(item, &parsed)
	record := linkdAlertRecord{AlertID: parsed.Payload.AlertID, Status: parsed.Payload.Status, EventSourceID: parsed.Payload.EventSourceID,
		Severity: parsed.Payload.Severity, Enrich: parsed.Payload.Enrich, Record: item, Logs: []json.RawMessage{}, Events: []json.RawMessage{}}
	if record.AlertID == "" {
		record.AlertID = parsed.ID
	}
	record.StrategyID = labelWord(parsed.Payload.Labels["strategy_id"])
	record.StrategyVersion = labelWord(parsed.Payload.Labels["strategy_version"])
	return record
}

// labelWord is a label as a reader compares it: a string as it is, a number
// in its decimal text.
func labelWord(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func scrubAll(scrub *redact.Scrubber, items []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		out = append(out, scrub.JSON(item))
	}
	return out
}

func cloneValues(values url.Values) url.Values {
	out := url.Values{}
	for key, list := range values {
		out[key] = append([]string(nil), list...)
	}
	return out
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// linkdAlertsFailure names a failed read by the Console's failure class; a
// refusal by the link carries the link's own words.
func linkdAlertsFailure(err error) obchannel.Outcome {
	message := "The linkd Console read did not complete: " + openalerts.ConsoleFailureClass(err) + "."
	var refusal *openalerts.LinkRefusal
	if errors.As(err, &refusal) {
		message = "linkd refused the read (HTTP " + strconv.Itoa(refusal.Status) + "): " + redact.New().Text(refusal.Code+" "+refusal.Message)
	}
	return obchannel.Outcome{Error: &obchannel.Failure{Code: "linkd_" + openalerts.ConsoleFailureClass(err), Message: message}}
}
