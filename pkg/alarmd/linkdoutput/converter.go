// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package linkdoutput writes a decision as the standard raw event the alert
// pipeline consumes.
//
// It carries facts and nothing else: whether this object is now triggered or
// resolved, at which level, from which data point, and since when its anomalies
// began. Everything an alert needs on top of that - when it was first seen,
// whether this supersedes an earlier level, when it closes, what the strategy
// and the resource behind it are - belongs to the consumer, which is why none of
// it is computed here and no state is kept for it.
package linkdoutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The actions the consumer distinguishes. A closed action is deliberately never
// produced: closing is a lifetime decision and this process does not own one.
const (
	ActionTriggered = "triggered"
	ActionResolved  = "resolved"
)

// The platform's alert levels, which are a closed set of three
// (constants/alert.py: FATAL / WARNING / REMIND). They are spelled out here
// because the consumer reads the severity as a name rather than a number.
var builtInSeverities = map[uint32]string{1: "critical", 2: "warning", 3: "info"}

// subjectSystems says which system owns each kind of object. The consumer uses
// it to look the object up, so a wrong system is worse than no subject.
var subjectSystems = map[string]struct{ System, Type string }{
	contract.MonitorSubjectHost:        {"cmdb", "host"},
	contract.MonitorSubjectService:     {"cmdb", "service_instance"},
	contract.MonitorSubjectTopo:        {"cmdb", "topo_node"},
	contract.MonitorSubjectK8sPod:      {"bcs", "pod"},
	contract.MonitorSubjectK8sNode:     {"bcs", "node"},
	contract.MonitorSubjectK8sService:  {"bcs", "service"},
	contract.MonitorSubjectK8sWorkload: {"bcs", "workload"},
	contract.MonitorSubjectAPMService:  {"apm", "service"},
}

// Event is one message on the wire.
type Event struct {
	EventID   string
	AlertID   string
	Payload   []byte
	Severity  string
	Action    string
	SubjectKd string
}

type wireSubject struct {
	System string `json:"system"`
	Type   string `json:"type"`
	ID     string `json:"id"`
}

type wireLabels struct {
	StrategyID      int64 `json:"strategy_id"`
	StrategyVersion int64 `json:"strategy_version"`
	BusinessID      int64 `json:"bk_biz_id"`
}

type wireExtraData struct {
	AnomalyBeginTime     string                     `json:"anomaly_begin_time,omitempty"`
	AdditionalDimensions map[string]json.RawMessage `json:"additional_dimensions,omitempty"`
}

type wireEvent struct {
	TenantID     string                     `json:"bk_tenant_id"`
	EventID      string                     `json:"event_id"`
	AlertID      string                     `json:"alert_id"`
	Title        string                     `json:"title"`
	Content      string                     `json:"content"`
	Severity     string                     `json:"severity"`
	Action       string                     `json:"action"`
	ActionReason string                     `json:"action_reason"`
	Dimensions   map[string]json.RawMessage `json:"dimensions"`
	Subject      *wireSubject               `json:"subject,omitempty"`
	OccurredAt   string                     `json:"occurred_at"`
	ProducedAt   string                     `json:"produced_at"`
	Labels       wireLabels                 `json:"labels"`
	ExtraData    wireExtraData              `json:"extra_data"`
}

// Converter turns decisions into wire messages. Now is injected because
// produced_at is the only field whose value is the moment of writing.
type Converter struct {
	now func() time.Time
}

func NewConverter(now func() time.Time) (*Converter, error) {
	if now == nil {
		return nil, errors.New("alarmd linkdoutput: a clock is required")
	}
	return &Converter{now: now}, nil
}

// Convert writes one decision.
//
// A decision without a frozen strategy revision cannot be written: the alert it
// would open is identified by a fingerprint this process derives from that
// revision, and the labels name a strategy version that only exists there. The
// caller decides what to do about such a decision - this returns an error
// rather than a message with holes in it.
func (converter *Converter) Convert(event *contract.TriggerEventV1) (Event, error) {
	if converter == nil || event == nil {
		return Event{}, errors.New("alarmd linkdoutput: a decision is required")
	}
	if event.StrategyRef == nil {
		return Event{}, errors.New("alarmd linkdoutput: a decision without a frozen strategy revision has no alert identity")
	}
	if event.DedupeMD5 == "" {
		return Event{}, errors.New("alarmd linkdoutput: a decision without a series identity has no alert identity")
	}
	action, err := actionFor(event.EventKind)
	if err != nil {
		return Event{}, err
	}
	primary, err := primaryLevel(event)
	if err != nil {
		return Event{}, err
	}
	businessID, err := strconv.ParseInt(event.BusinessID, 10, 64)
	if err != nil {
		return Event{}, fmt.Errorf("alarmd linkdoutput: business identity %q: %w", event.BusinessID, err)
	}
	dimensions := map[string]json.RawMessage{}
	var subject *wireSubject
	var additional map[string]json.RawMessage
	if event.Subject != nil {
		dimensions = event.Subject.Dimensions
		subject = wireSubjectFor(event.Subject.Subject)
		additional = event.Subject.Subject.Additional
	}
	if dimensions == nil {
		dimensions = map[string]json.RawMessage{}
	}
	severity := severityFor(primary)
	message := wireEvent{
		TenantID: event.TenantID, EventID: event.EventID, AlertID: event.DedupeMD5,
		Title: title(event, primary, action), Content: content(event, primary),
		Severity: severity, Action: action,
		// Left empty on purpose: the reason an alert resolved is not a fact
		// this process establishes, and a guess here would be read as one.
		ActionReason: "",
		Dimensions:   dimensions, Subject: subject,
		OccurredAt: wireTime(event.RecordRef.SourceTime), ProducedAt: wireTime(converter.now().Unix()),
		Labels: wireLabels{
			StrategyID: event.StrategyRef.StrategyID, StrategyVersion: event.StrategyRef.Revision,
			BusinessID: businessID,
		},
		ExtraData: wireExtraData{AdditionalDimensions: additional},
	}
	if begin := primary.DecisionWindow.Trigger.AnomalyBeginTime; begin > 0 {
		message.ExtraData.AnomalyBeginTime = wireTime(begin)
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return Event{}, fmt.Errorf("alarmd linkdoutput: encode decision: %w", err)
	}
	kind := ""
	if subject != nil {
		kind = subject.Type
	}
	return Event{
		EventID: event.EventID, AlertID: event.DedupeMD5, Payload: payload,
		Severity: severity, Action: action, SubjectKd: kind,
	}, nil
}

func actionFor(kind string) (string, error) {
	switch kind {
	case contract.TriggerEventAbnormal:
		return ActionTriggered, nil
	case contract.TriggerEventRecovery:
		return ActionResolved, nil
	default:
		return "", fmt.Errorf("alarmd linkdoutput: decision kind %q has no action", kind)
	}
}

// primaryLevel returns the level the event was aggregated to. The aggregation
// already happened - lowest priority among the levels that agree with the
// event's kind, then lowest level id - so this only has to find it again.
func primaryLevel(event *contract.TriggerEventV1) (contract.LevelResultV1, error) {
	for _, level := range event.LevelResults {
		if level.LevelID == event.PrimaryLevelID {
			return level, nil
		}
	}
	return contract.LevelResultV1{}, fmt.Errorf(
		"alarmd linkdoutput: primary level %d is absent from the decision", event.PrimaryLevelID,
	)
}

// severityFor names the level for the consumer.
//
// The three built-in levels are the platform's whole set today and their names
// do not change. A level outside them is not rejected: levels are stated in the
// strategy snapshot and the platform may extend them, so the snapshot's own
// identifier is used when it has one, and otherwise an identifier derived from
// the level. Neither case invents a business meaning - a consumer that does not
// recognise the name maps it or falls back on its own terms.
func severityFor(level contract.LevelResultV1) string {
	if name, builtIn := builtInSeverities[level.LevelID]; builtIn {
		return name
	}
	if level.LevelCode != "" {
		return level.LevelCode
	}
	return "level_" + strconv.FormatUint(uint64(level.LevelID), 10)
}

// SeverityIsBuiltIn reports whether a level had a name of its own rather than
// one derived from its number. A derived name is the signal that the platform
// grew a level this build has no mapping for, which is worth counting: the
// consumer will fall back to its default severity and the alert arrives at the
// wrong level, quietly.
func SeverityIsBuiltIn(level contract.LevelResultV1) bool {
	_, builtIn := builtInSeverities[level.LevelID]
	return builtIn || level.LevelCode != ""
}

func wireSubjectFor(subject contract.MonitorSubject) *wireSubject {
	naming, known := subjectSystems[subject.Type]
	if !known || subject.ID == "" {
		// A record with no object is a real answer - custom reporting with no
		// target dimensions has none - and an empty subject says so. Inventing
		// one would attach the alert to something.
		return nil
	}
	return &wireSubject{System: naming.System, Type: naming.Type, ID: subject.ID}
}

func wireTime(epochSeconds int64) string {
	return time.Unix(epochSeconds, 0).UTC().Format(time.RFC3339)
}

// title and content are a plain statement of the decision. They are a starting
// point the consumer enriches with the strategy and the resource, so they name
// only what this process established, and never guess a metric's meaning.
func title(event *contract.TriggerEventV1, primary contract.LevelResultV1, action string) string {
	verb := "triggered"
	if action == ActionResolved {
		verb = "resolved"
	}
	return fmt.Sprintf("Strategy %d level %d %s", event.StrategyRef.StrategyID, primary.LevelID, verb)
}

func content(event *contract.TriggerEventV1, primary contract.LevelResultV1) string {
	window := primary.DecisionWindow.Trigger
	values := observedValues(event.Observed)
	if values == "" {
		values = "no value"
	}
	return fmt.Sprintf(
		"%s at %s; %d of %d points in the window were anomalous",
		values, wireTime(event.RecordRef.SourceTime), window.ObservedAnomalies, window.WindowSize,
	)
}

func observedValues(observed contract.TriggerObservedV1) string {
	names := make([]string, 0, len(observed.Values))
	for name := range observed.Values {
		names = append(names, name)
	}
	sort.Strings(names)
	rendered := ""
	for _, name := range names {
		if rendered != "" {
			rendered += ", "
		}
		rendered += name + "=" + string(observed.Values[name])
		if observed.Unit != "" {
			rendered += observed.Unit
		}
	}
	return rendered
}
