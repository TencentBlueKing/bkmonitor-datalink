// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package linkdoutput writes a decision as the standard event the alert link
// daemon's `standard` cleaner reads.
//
// It carries facts and nothing else: which levels this object is now triggered
// or resolved at, from which data point, and what the round saw. Everything an
// alert needs on top of that -- when the alert started, whether a level
// supersedes another, when it closes, which model and instance the subject
// resolves to -- belongs to the consumer, which is why none of it is computed
// here and no state is kept for it.
//
// The field names, the action words and the value rules are the consumer's,
// copied from pkg/linkd/internal/cleaner/raw_event.go and its domain package
// rather than chosen here. Where the consumer is strict this side is strict
// before it: a null dimension, a duplicate key between the two dimension maps
// or a non-numeric value would have the whole message refused there, one
// message at a time and silently, so none of them is written.
package linkdoutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The two actions this process produces, out of the consumer's three.
//
// Detector conversion does not emit closed; ConvertClose handles explicit
// effective-time maintenance separately. Closing is a lifetime decision made on
// a timeout this process does not observe. A repeated triggered is how a
// continuing anomaly is stated.
const (
	ActionTriggered = "triggered"
	ActionResolved  = "resolved"
)

// The platform's alert levels, which are a closed set of three
// (constants/alert.py: FATAL / WARNING / REMIND). They are spelled out here
// because the consumer reads the severity as a name rather than a number.
var builtInSeverities = map[uint32]string{1: "critical", 2: "warning", 3: "info"}

// The consumer's bounds on what one message may carry, transcribed from its
// cleaner (linkd standard: evaluations 1..32 items, severity 1..32 bytes). A
// message over either is refused whole there, silently to this side, so both
// are held here where the values are made.
const (
	// MaxEvaluations is the most decided levels one message can carry. The
	// levels of a Plan are bounded by the compiler's level budget, and the
	// configuration refuses a budget above this, so the converter's own check
	// is reached only by a decision that was never compiled.
	MaxEvaluations = 32
	// MaxSeverityBytes is the longest severity name the consumer accepts. A
	// level code longer than this is treated as a level without a code: the
	// derived name is written and the level is counted as unmapped, because
	// a name the consumer refuses whole loses the alert, and a derived name
	// it maps to its default loses only the level, visibly.
	MaxSeverityBytes = 32
)

// subjectSystems says which system owns each kind of object, in the consumer's
// spelling. The consumer resolves the instance from its own strategy and the
// dimensions; the subject is what it shows and what its display falls back on,
// so a wrong system is worse than no subject.
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

// The evaluation families this process produces. A keyword strategy on a log
// or event source is still an algorithm over a counted series here, so it is
// metric_algorithm; only absence detection is its own family.
const (
	evaluationFamilyMetricAlgorithm = "metric_algorithm"
	evaluationFamilyNoData          = "no_data"
)

// observedValueName is the key the observed value is written under in
// values. It is what this process calls the value of the strategy's
// expression; the metric it was computed from is the consumer's to name from
// the strategy.
const observedValueName = "value"

// Event is one message on the wire.
type Event struct {
	EventID  string
	AlertID  string
	TenantID string
	Payload  []byte
	// Severity and Action are the primary level's, for the sink's own
	// bookkeeping; the message itself carries every decided level.
	Severity  string
	Action    string
	SubjectKd string
}

// wireEvaluation is one level's judgement. The consumer keys its lifecycle on
// the severity, so every decided level is written and an undecided one is
// left out: the consumer reads an absent level as "nothing said", never as
// resolved.
type wireEvaluation struct {
	Severity string `json:"severity"`
	Action   string `json:"action"`
	// Empty on purpose: the reason an alert resolved is not a fact this
	// process establishes, and a guess here would be read as one.
	ActionReason string `json:"action_reason"`
}

type wireSubject struct {
	System string `json:"system"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	// Name is what the consumer shows for the object. This process has no
	// display name in hand at the point of writing, so the field is absent
	// and the consumer falls back on the dimensions.
	Name string `json:"name,omitempty"`
}

// wireLabels are the three the consumer's enrichment requires as positive
// integers; a string or a zero fails its processors with invalid_field.
type wireLabels struct {
	StrategyID      int64 `json:"strategy_id"`
	StrategyVersion int64 `json:"strategy_version"`
	BusinessID      int64 `json:"bk_biz_id"`
}

type wireWindow struct {
	Size      uint32 `json:"size"`
	Anomalies uint32 `json:"anomalies"`
	Required  uint32 `json:"required"`
}

// wireExtraData is kept by the consumer as an opaque object on the alert. The
// first two fields are the ones its processors read; the rest are this
// process's facts about the decision, carried for whoever reads the alert.
type wireExtraData struct {
	AnomalyBeginTime     string                     `json:"anomaly_begin_time,omitempty"`
	AdditionalDimensions map[string]json.RawMessage `json:"additional_dimensions,omitempty"`
	Window               *wireWindow                `json:"window,omitempty"`
	NoDataPeriods        json.RawMessage            `json:"no_data_periods,omitempty"`
	EventSemanticDigest  string                     `json:"event_semantic_digest,omitempty"`
	SignalType           string                     `json:"signal_type,omitempty"`
	EvaluationFamily     string                     `json:"evaluation_family"`
	Unit                 string                     `json:"unit,omitempty"`
}

type wireEvent struct {
	TenantID    string                     `json:"bk_tenant_id"`
	EventID     string                     `json:"event_id"`
	AlertID     string                     `json:"alert_id"`
	Title       string                     `json:"title"`
	Content     string                     `json:"content"`
	Values      map[string]float64         `json:"values,omitempty"`
	Evaluations []wireEvaluation           `json:"evaluations"`
	Dimensions  map[string]json.RawMessage `json:"dimensions"`
	Subject     *wireSubject               `json:"subject,omitempty"`
	OccurredAt  string                     `json:"occurred_at"`
	ProducedAt  string                     `json:"produced_at"`
	Labels      wireLabels                 `json:"labels"`
	ExtraData   wireExtraData              `json:"extra_data"`
}

// Converter turns decisions into wire messages.
//
// It holds no clock. Every time in the message is a fact of the decision - the
// data point and the round that judged it - so converting the same decision
// twice, on a retry or a replay, writes the same bytes. The consumer asks for
// exactly that of produced_at.
type Converter struct {
	// onUnmappedSeverity is called for each level whose id had no name in
	// this build. Such an event still ships - refusing it would silence an
	// alert over a naming gap - but it arrives at the consumer under a default
	// severity, and that is the only place the gap is visible.
	onUnmappedSeverity func(level uint32)
}

// NewConverter builds a converter. It takes no clock: see Converter.
func NewConverter(onUnmappedSeverity func(level uint32)) (*Converter, error) {
	return &Converter{onUnmappedSeverity: onUnmappedSeverity}, nil
}

// Convert writes one decision.
//
// A decision without a frozen strategy revision cannot be written: the alert it
// would open is identified by a key this process derives from that revision,
// and the labels name a strategy version that only exists there. The caller
// decides what to do about such a decision - this returns an error rather
// than a message with holes in it.
func (converter *Converter) Convert(event *contract.TriggerEventV1) (Event, error) {
	if converter == nil || event == nil {
		return Event{}, reject(RuleIdentityMissing, errors.New("alarmd linkdoutput: a decision is required"))
	}
	if event.StrategyRef == nil {
		return Event{}, reject(RuleIdentityMissing, errors.New("alarmd linkdoutput: a decision without a frozen strategy revision has no alert identity"))
	}
	if event.DedupeMD5 == "" {
		return Event{}, reject(RuleIdentityMissing, errors.New("alarmd linkdoutput: a decision without a series identity has no alert identity"))
	}
	action, err := actionFor(event.EventKind)
	if err != nil {
		return Event{}, reject(RuleActionUnknown, err)
	}
	primary, err := primaryLevel(event)
	if err != nil {
		return Event{}, reject(RuleLevelsInvalid, err)
	}
	businessID, err := strconv.ParseInt(event.BusinessID, 10, 64)
	if err != nil {
		return Event{}, reject(RuleBusinessIdentity, fmt.Errorf("alarmd linkdoutput: business identity %q: %w", event.BusinessID, err))
	}
	evaluations, err := converter.evaluations(event)
	if err != nil {
		return Event{}, err
	}
	// The record's own dimensions, whole. The object's identity fields stay in
	// here rather than being taken out into the subject: the consumer's query
	// enrichment reads this map, and a message with the identity removed
	// would describe every object of one strategy the same way.
	dimensions := scalarDimensions(event.RecordRef.Dimensions)
	var subject *wireSubject
	var additional map[string]json.RawMessage
	if event.Subject != nil {
		subject = wireSubjectFor(event.Subject.Subject)
		additional = additionalDimensions(event.Subject.Subject.Additional, dimensions)
	}
	noData := isNoDataEvent(event.RecordRef.Dimensions)
	message := wireEvent{
		TenantID: event.TenantID, EventID: event.EventID, AlertID: event.DedupeMD5,
		Title: title(event, primary, action), Content: content(event, primary, noData),
		Evaluations: evaluations, Dimensions: dimensions, Subject: subject,
		// The data point, and the round that judged it. Neither is the clock:
		// a replayed Slot would otherwise claim its anomaly happened now, and
		// a retried write would move a time the consumer expects to hold.
		OccurredAt: wireTime(event.RecordRef.SourceTime), ProducedAt: wireTime(event.EvaluationTime),
		Labels: wireLabels{
			StrategyID: event.StrategyRef.StrategyID,
			// The frozen revision. The consumer resolves the strategy
			// snapshot it enriches from by this together with the id.
			StrategyVersion: event.StrategyRef.Revision,
			BusinessID:      businessID,
		},
		ExtraData: wireExtraData{
			AdditionalDimensions: additional,
			Window: &wireWindow{
				Size:      primary.DecisionWindow.Trigger.WindowSize,
				Anomalies: primary.DecisionWindow.Trigger.ObservedAnomalies,
				Required:  primary.DecisionWindow.Trigger.RequiredAnomalies,
			},
			EventSemanticDigest: event.EventSemanticDigest,
			SignalType:          event.SignalType,
			EvaluationFamily:    evaluationFamilyMetricAlgorithm,
		},
	}
	if begin := primary.DecisionWindow.Trigger.AnomalyBeginTime; begin > 0 {
		// In extra_data and not as the alert's start. It is the earliest
		// anomalous point still inside this window, so it moves as the window
		// slides; written as the alert's start it would make a long-running
		// alert look like it started again every round. The consumer takes
		// the start from the trigger it opened the alert on.
		message.ExtraData.AnomalyBeginTime = wireTime(begin)
	}
	if noData {
		// No value, on purpose. The synthetic point a no-data round produces
		// carries a marker rather than a measurement, and publishing it as
		// the observed value would put a number on a page whose whole subject
		// is that there was no number.
		message.ExtraData.EvaluationFamily = evaluationFamilyNoData
		message.ExtraData.NoDataPeriods = event.Observed.Values[contract.NoDataPeriodFactField]
	} else {
		message.Values = observedValues(event.Observed)
		message.ExtraData.Unit = event.Observed.Unit
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return Event{}, reject(RuleEncode, fmt.Errorf("alarmd linkdoutput: encode decision: %w", err))
	}
	kind := ""
	if subject != nil {
		kind = subject.Type
	}
	return Event{
		EventID: event.EventID, AlertID: event.DedupeMD5, TenantID: event.TenantID, Payload: payload,
		Severity: severityFor(primary), Action: action, SubjectKd: kind,
	}, nil
}

// evaluations writes one judgement per decided level, in level order.
//
// The consumer keeps one lifecycle per severity under an alert key, and it
// reads an absent level as nothing said. So a level whose result is ABNORMAL
// is triggered, one whose result is RECOVERY is resolved, and NORMAL or
// UNAVAILABLE levels are not written: writing "resolved" for a level that
// merely stayed normal would close nothing, and writing it for one this round
// could not evaluate would close an alert on no evidence.
//
// A decision has at least one decided level by construction - the aggregation
// that produced it refuses an all-NORMAL result - so an empty list here is a
// contract violation, not a message.
func (converter *Converter) evaluations(event *contract.TriggerEventV1) ([]wireEvaluation, error) {
	evaluations := make([]wireEvaluation, 0, len(event.LevelResults))
	seen := make(map[string]struct{}, len(event.LevelResults))
	for _, level := range event.LevelResults {
		action := ""
		switch level.Result {
		case contract.LevelResultAbnormal:
			action = ActionTriggered
		case contract.LevelResultRecovery:
			action = ActionResolved
		default:
			continue
		}
		severity := severityFor(level)
		if !SeverityIsBuiltIn(level) && converter.onUnmappedSeverity != nil {
			converter.onUnmappedSeverity(level.LevelID)
		}
		if _, duplicate := seen[severity]; duplicate {
			// Two levels with one name would be refused whole by the
			// consumer. It cannot happen with the platform's levels, whose
			// names are distinct by construction; refusing here keeps it
			// from happening silently with a level code that repeats one.
			return nil, reject(RuleLevelsInvalid, fmt.Errorf("alarmd linkdoutput: two levels of one decision share the severity %q", severity))
		}
		seen[severity] = struct{}{}
		evaluations = append(evaluations, wireEvaluation{Severity: severity, Action: action})
	}
	if len(evaluations) == 0 {
		return nil, reject(RuleLevelsInvalid, errors.New("alarmd linkdoutput: a decision with no decided level has nothing to say"))
	}
	if len(evaluations) > MaxEvaluations {
		// Unreachable for a compiled Plan: the level budget is held below
		// this at configuration time. Refused here so that it stays a
		// contract violation rather than a message the consumer drops.
		return nil, reject(RuleTooManyLevels, fmt.Errorf("alarmd linkdoutput: %d decided levels exceed the %d evaluations one message carries", len(evaluations), MaxEvaluations))
	}
	return evaluations, nil
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

// isNoDataEvent reads the tag the absence evaluation puts in the group's
// dimensions.
//
// From the dimensions rather than from anything about the Plan: a strategy that
// detects no-data also detects thresholds, so the Plan cannot say which of the
// two this event is. The tag is on the series, which is what the event is
// about, and it is the same thing the existing alert pipeline reads.
func isNoDataEvent(dimensions map[string]json.RawMessage) bool {
	_, tagged := dimensions[contract.NoDataDimensionTag]
	return tagged
}

// scalarDimensions is the record's dimensions with the null-valued ones left
// out.
//
// A null is how this process states that a declared identity dimension was
// absent from the series, and its own fingerprint reads it that way. The
// consumer's dimension type has no null - a string, a finite number or a
// boolean, and anything else refuses the whole message - and it keys the
// alert on alert_id rather than on these, so leaving the dimension out says
// the same thing to it that the null said here: the series did not carry it.
func scalarDimensions(dimensions map[string]json.RawMessage) map[string]json.RawMessage {
	kept := make(map[string]json.RawMessage, len(dimensions))
	for name, raw := range dimensions {
		if isJSONNull(raw) {
			continue
		}
		kept[name] = raw
	}
	return kept
}

// additionalDimensions is what the projection worked out that the record did
// not carry, minus anything the record did carry after all: the consumer
// refuses a key present in both maps, and the record's own value is the one
// that must win because the dimensions are what the record said.
func additionalDimensions(additional map[string]json.RawMessage, dimensions map[string]json.RawMessage) map[string]json.RawMessage {
	if len(additional) == 0 {
		return nil
	}
	kept := make(map[string]json.RawMessage, len(additional))
	for name, raw := range additional {
		if isJSONNull(raw) {
			continue
		}
		if _, carried := dimensions[name]; carried {
			continue
		}
		kept[name] = raw
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || strings.TrimSpace(string(raw)) == "null"
}

// observedValues is the measurement the decision was made on, as the flat
// numeric snapshot the consumer accepts.
//
// Only a finite number is a value there; a decision over several values has
// no single observed value, and picking one would state a measurement the
// strategy did not make. The values are all in the content either way, which
// is where a reader sees them. Nothing is written when there is nothing to
// say: the consumer reads an absent map as empty.
func observedValues(observed contract.TriggerObservedV1) map[string]float64 {
	raw := soleObservedValue(observed)
	if raw == nil {
		return nil
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return map[string]float64{observedValueName: number}
}

// soleObservedValue is the observation's value when there is exactly one, and
// nothing when there is not.
func soleObservedValue(observed contract.TriggerObservedV1) json.RawMessage {
	if len(observed.Values) != 1 {
		return nil
	}
	for _, value := range observed.Values {
		return value
	}
	return nil
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
// identifier is used when it has one the consumer can carry, and otherwise an
// identifier derived from the level. Neither case invents a business meaning -
// a consumer that does not recognise the name maps it or falls back on its own
// terms.
func severityFor(level contract.LevelResultV1) string {
	if name, builtIn := builtInSeverities[level.LevelID]; builtIn {
		return name
	}
	if levelCodeCarriable(level.LevelCode) {
		return level.LevelCode
	}
	return "level_" + strconv.FormatUint(uint64(level.LevelID), 10)
}

// levelCodeCarriable reports whether a snapshot's level code can be written
// as the severity: present, and within the consumer's length bound.
func levelCodeCarriable(code string) bool {
	return code != "" && len(code) <= MaxSeverityBytes
}

// SeverityIsBuiltIn reports whether a level had a name of its own rather than
// one derived from its number. A derived name is the signal that the platform
// grew a level this build has no mapping for, or named one in a way the
// consumer cannot carry, which is worth counting: the consumer will fall back
// to its default severity and the alert arrives at the wrong level, quietly.
func SeverityIsBuiltIn(level contract.LevelResultV1) bool {
	_, builtIn := builtInSeverities[level.LevelID]
	return builtIn || levelCodeCarriable(level.LevelCode)
}

// wireSubjectFor writes the object in the consumer's spelling.
//
// Every type the projection can produce has a spelling in subjectSystems -
// the test walks contract.MonitorSubjectTypes() through the table - so the
// unknown branch is the empty type: a record with no object, which is a real
// answer (custom reporting with no target dimensions has none), and no
// subject says so. Inventing one would attach the alert to something.
func wireSubjectFor(subject contract.MonitorSubject) *wireSubject {
	naming, known := subjectSystems[subject.Type]
	if !known || subject.ID == "" {
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
	return fmt.Sprintf("Strategy %d level %d %s", event.StrategyRef.StrategyID, primary.LevelID, action)
}

func content(event *contract.TriggerEventV1, primary contract.LevelResultV1, noData bool) string {
	window := primary.DecisionWindow.Trigger
	if noData {
		return fmt.Sprintf(
			"no data for %s periods, as of %s",
			noDataPeriods(event.Observed), wireTime(event.RecordRef.SourceTime),
		)
	}
	values := renderedValues(event.Observed)
	if values == "" {
		values = "no value"
	}
	return fmt.Sprintf(
		"%s at %s; %d of %d points in the window were anomalous",
		values, wireTime(event.RecordRef.SourceTime), window.ObservedAnomalies, window.WindowSize,
	)
}

// noDataPeriods is how many periods the group has been silent, as the absence
// evaluation counted it and carried it on the point. It is not recomputed here
// from anything: this process would have to know the period and the clock to do
// that, and the number would then disagree with the one that was detected on.
func noDataPeriods(observed contract.TriggerObservedV1) string {
	if raw, carried := observed.Values[contract.NoDataPeriodFactField]; carried {
		return string(raw)
	}
	return "an unknown number of"
}

func renderedValues(observed contract.TriggerObservedV1) string {
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
