// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// StandardEventConverter writes a decision as the standard raw event.
type StandardEventConverter interface {
	Convert(*contract.TriggerEventV1) (linkdoutput.Event, error)
}

// TriggerEventSink is the critical Kafka output for Trigger events.
// A successful WriteBatch means every event received a synchronous broker ACK.
type TriggerEventSink struct {
	core              *DecisionSink
	legacyConverter   LegacyEventConverter
	standardConverter StandardEventConverter
	legacyTopic       string
	maxLegacyBytes    int
	// now is the clock the per-batch lease admission reads; time.Now in
	// production, injected by tests that place the lease deadline.
	now func() time.Time
	// protocol is what the producer was opened with and why; nil for a sink
	// built around a producer that was not opened by this package (tests),
	// which is read as headers supported.
	protocol *ProtocolNegotiation
}

// ProtocolNegotiation is the protocol this sink's producer speaks and the
// broker answers it was decided from, for readers; nil before the sink has
// been opened by the opener. Every open negotiates afresh.
func (sink *TriggerEventSink) ProtocolNegotiation() *ProtocolNegotiation {
	if sink == nil || sink.protocol == nil {
		return nil
	}
	copied := *sink.protocol
	copied.Brokers = append([]BrokerProtocol(nil), sink.protocol.Brokers...)
	return &copied
}

// headersSupported is whether the producer can send a record with headers.
func (sink *TriggerEventSink) headersSupported() bool {
	return sink.protocol == nil || sink.protocol.HeadersSupported
}

// ConfigureStandardOutput replaces the standard raw event converter, once, at
// assembly. The default one is complete; this exists so the assembly can give
// it the observation callback that reports an alert level this build has no
// name for, which the sink has no recorder to report itself.
func (sink *TriggerEventSink) ConfigureStandardOutput(converter StandardEventConverter) error {
	if converter == nil {
		return errors.New("invalid standard output configuration")
	}
	sink.standardConverter = converter
	return nil
}

// ConfigureLegacyOutput is called once during assembly, before any writes.
func (sink *TriggerEventSink) ConfigureLegacyOutput(converter LegacyEventConverter, topic string, maxBytes int) error {
	if converter == nil || !strings.HasPrefix(topic, "alarmd_") || maxBytes <= 0 {
		return errors.New("invalid legacy output configuration")
	}
	sink.legacyConverter, sink.legacyTopic, sink.maxLegacyBytes = converter, topic, maxBytes
	return nil
}

// triggerEventDependencyError marks a broker write whose ACK failed or is
// unknown: the message may or may not have landed, and only a replay of the
// same event identity settles it. It is the only error from WriteBatch that
// a caller should retry. What this process decides on its own -- a decision
// the converter will not write, a message the client refuses before any
// broker sees it -- is an OutputRejectedError, never this: it used to be
// wrapped here too, and a deployment whose output was refused by its own
// client for want of a protocol version read as a Kafka that was down,
// retried every round, and committed no progress. Encoding and local
// lifecycle errors remain ordinary.
type triggerEventDependencyError struct {
	err error
}

// OutputRejectedError is a decision this process will not write, decided
// here and not at a broker. Reason is the completion code the Slot names
// (contract.ReasonOutputConversionRejected or ReasonOutputClientRejected),
// Detail the sentence that says why, from the converter or the client, and
// the identities say which decision. It does not mark
// RetryableOutputDependency on purpose: the same decision meets the same
// refusal on every retry, so a caller finishes the Plan by this name
// instead of waiting for a broker that was never asked.
type OutputRejectedError struct {
	Reason     string
	Detail     string
	EventID    string
	StrategyID string
	BusinessID string
	Format     string
}

func (err *OutputRejectedError) Error() string {
	if err == nil {
		return "kafka trigger event sink: output rejected"
	}
	return fmt.Sprintf("kafka trigger event sink: %s: %s (event %s, strategy %s, business %s, format %s)",
		err.Reason, err.Detail, err.EventID, err.StrategyID, err.BusinessID, err.Format)
}

// OutputRejectionReason is how a caller tells this apart from every other
// error without importing the type: the reason it names. Callers that
// cannot import this package (the worker's observations, the fleet page)
// read the reason and the detail through these two methods on a local
// interface, so nobody slices the Error() text for them.
func (err *OutputRejectedError) OutputRejectionReason() string {
	if err == nil {
		return ""
	}
	return err.Reason
}

// OutputRejectionDetail is the sentence that says why, from the converter
// or the client, without the identities Error() appends; those travel on
// the observation's trace fields.
func (err *OutputRejectedError) OutputRejectionDetail() string {
	if err == nil {
		return ""
	}
	return err.Detail
}

// OutputDeferredError is a batch the sink did not start because the lease
// the Slot runs under has less life left than the batch needs to land.
// Nothing was sent, so nothing is unknown: the caller keeps the Plan waiting
// and retries after the next renewal, and the Slot ends with the lease if
// none comes. It marks neither RetryableOutputDependency (no broker was
// asked) nor a rejection (nothing about the content is wrong).
type OutputDeferredError struct {
	Remaining time.Duration
	Needed    time.Duration
}

func (err *OutputDeferredError) Error() string {
	if err == nil {
		return "kafka trigger event sink: output deferred"
	}
	return fmt.Sprintf("kafka trigger event sink: %s: the lease has %s left and one batch needs %s to land",
		contract.ReasonOutputLeaseExpiring, err.Remaining.Round(time.Millisecond), err.Needed)
}

// OutputDeferralReason is how a caller tells a deferral from every other
// error without importing the type.
func (err *OutputDeferredError) OutputDeferralReason() string {
	if err == nil {
		return ""
	}
	return contract.ReasonOutputLeaseExpiring
}

// admitAgainstLease is the per-batch admission of decision-016: before the
// first byte, the batch's bound plus the margin must fit inside what is
// left of the lease the Slot runs under. A context that carries no lease
// authority admits as it always did.
func admitAgainstLease(ctx context.Context, now func() time.Time) error {
	authority, ok := execution.LeaseAuthorityFromContext(ctx)
	if !ok {
		return nil
	}
	needed := OutputAdmissionMargin + OutputBatchBound
	remaining := authority.Deadline().Sub(now())
	if remaining < needed {
		return &OutputDeferredError{Remaining: remaining, Needed: needed}
	}
	return nil
}

func outputRejected(reason, detail, format string, event *contract.TriggerEventV1) *OutputRejectedError {
	rejected := &OutputRejectedError{Reason: reason, Detail: detail, Format: format}
	if event != nil {
		rejected.EventID = event.EventID
		rejected.BusinessID = event.BusinessID
		rejected.StrategyID = event.PlanRef.StrategyID
	}
	return rejected
}

// clientRejection reports whether a producer error is the client refusing
// the message itself, before any network: Sarama returns a ConfigurationError
// for a record it cannot encode on the configured protocol (headers before
// 0.11) and ErrMessageSizeTooLarge for one over its own cap. A batch counts
// as client-rejected only when every failed message in it was; one broker
// failure among them keeps the whole batch a dependency failure, since the
// others may have landed or not.
func clientRejection(err error) (string, bool) {
	var batch sarama.ProducerErrors
	if errors.As(err, &batch) {
		if len(batch) == 0 {
			return "", false
		}
		details := make([]string, 0, len(batch))
		for _, failure := range batch {
			if failure == nil {
				return "", false
			}
			detail, rejected := oneClientRejection(failure.Err)
			if !rejected {
				return "", false
			}
			details = append(details, detail)
		}
		return strings.Join(uniqueStrings(details), "; "), true
	}
	return oneClientRejection(err)
}

func oneClientRejection(err error) (string, bool) {
	var configuration sarama.ConfigurationError
	if errors.As(err, &configuration) {
		return string(configuration), true
	}
	if errors.Is(err, sarama.ErrMessageSizeTooLarge) {
		return sarama.ErrMessageSizeTooLarge.Error(), true
	}
	return "", false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := values[:0]
	for _, value := range values {
		if _, done := seen[value]; done {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func (err *triggerEventDependencyError) Error() string {
	if err == nil || err.err == nil {
		return "kafka trigger event sink: output dependency failure"
	}
	return err.err.Error()
}

func (err *triggerEventDependencyError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *triggerEventDependencyError) RetryableOutputDependency() {}

// OpenTriggerEventSink prepares and opens the sink in one call.
func OpenTriggerEventSink(coordinates DecisionSinkConfig) (*TriggerEventSink, error) {
	opener, err := PrepareTriggerEventSink(coordinates)
	if err != nil {
		return nil, err
	}
	return opener.Open()
}

// TriggerEventSinkOpener is the half of opening the sink that needs the
// network. The other half -- reading and validating the coordinates -- has
// already run when one of these exists, so an error from Open is the broker
// not answering, never the configuration being wrong. The two are separated
// because the process treats them differently: a wrong configuration is
// refused at startup, a broker that does not answer is retried while the
// replica stays up and says it is not ready.
type TriggerEventSinkOpener struct {
	coordinates DecisionSinkConfig
	config      *sarama.Config
}

// PrepareTriggerEventSink validates the coordinates and builds the client
// configuration, and touches no network.
func PrepareTriggerEventSink(coordinates DecisionSinkConfig) (*TriggerEventSinkOpener, error) {
	config, err := NewDecisionProducerOnlyConfig(coordinates)
	if err != nil {
		return nil, err
	}
	return &TriggerEventSinkOpener{coordinates: coordinates, config: config}, nil
}

// Open negotiates the protocol with the brokers, then connects and opens
// the producer on it. It may be called again after a failure; each call is
// a fresh attempt and a fresh negotiation, so a cluster upgraded while the
// sink was down is spoken to at its new version when the sink reopens.
func (opener *TriggerEventSinkOpener) Open() (*TriggerEventSink, error) {
	if opener == nil || opener.config == nil {
		return nil, errors.New("kafka trigger event sink: opener is not prepared")
	}
	negotiation, err := NegotiateProtocol(opener.coordinates.Brokers, opener.config)
	if err != nil {
		return nil, &ProtocolNegotiationError{Negotiation: negotiation, Err: err}
	}
	config := *opener.config
	config.Version = negotiation.Version()
	client, err := sarama.NewClient(opener.coordinates.Brokers, &config)
	if err != nil {
		return nil, fmt.Errorf("kafka trigger event sink: open client: %w", err)
	}
	producer, err := newSyncProducerForOutput(client, opener.coordinates.OutputTopic)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka trigger event sink: open producer: %w", err), client.Close())
	}
	sink, err := newTriggerEventSink(opener.coordinates.OutputTopic, producer, client)
	if err != nil {
		return nil, errors.Join(err, producer.Close(), client.Close())
	}
	sink.protocol = &negotiation
	return sink, nil
}

// ProtocolNegotiationError is an open that could not decide the protocol
// because a broker did not answer. It carries the partial answers so the
// state a reader sees says which broker, and is retried like any open
// failure: the producer is not opened on a guess.
type ProtocolNegotiationError struct {
	Negotiation ProtocolNegotiation
	Err         error
}

func (err *ProtocolNegotiationError) Error() string {
	if err == nil || err.Err == nil {
		return "kafka trigger event sink: protocol negotiation failed"
	}
	return "kafka trigger event sink: " + err.Err.Error()
}

func (err *ProtocolNegotiationError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func newTriggerEventSink(
	outputTopic string,
	producer syncMessageProducer,
	client closeableClient,
) (*TriggerEventSink, error) {
	core, err := newDecisionSink(outputTopic, producer, client)
	if err != nil {
		return nil, err
	}
	standard, err := linkdoutput.NewConverter(nil)
	if err != nil {
		return nil, err
	}
	return &TriggerEventSink{
		core: core, legacyConverter: &legacyoutput.Converter{}, standardConverter: standard,
		legacyTopic: "alarmd_0bkmonitor_backend_event", maxLegacyBytes: 524288, now: time.Now,
	}, nil
}

// tenantHeader is the record header the consumer's Kafka adapter reads the
// tenant from.
const tenantHeader = "bk_tenant_id"

func (sink *TriggerEventSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	if sink == nil || sink.core == nil {
		return ErrDecisionSinkClosed
	}
	if ctx == nil {
		return errors.New("kafka trigger event sink: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := admitAgainstLease(ctx, sink.now); err != nil {
		return err
	}
	messages := make([]*sarama.ProducerMessage, len(events))
	formats := make([]string, len(events))
	// refused holds, per event, the rule it broke and why; a zero entry is an
	// event the sink will write or that the protocol has no message for.
	// Each refusal is about that event alone: the others of the batch go on.
	refused := make([]refusal, len(events))
	groups := make(map[string][]int)
	for index := range events {
		// Standard events have already been validated when built; retain the
		// converter's wire checks without rehashing the internal envelope.
		// Historical and Python paths retain the validation formerly done by
		// EncodeTriggerEventV1, but no longer encode that internal structure.
		if events[index].WireFormat != contract.WireFormatStandardRawEvent {
			if err := contract.ValidateTriggerEventV1(&events[index]); err != nil {
				refused[index] = refusal{rule: observability.OutputRejectEventInvalid, detail: fmt.Sprintf("validate event %d: %v", index, err)}
				continue
			}
		}
		var revision int64
		if events[index].StrategyRef != nil {
			revision = events[index].StrategyRef.Revision
		}
		format := contract.ResolveOutputWireFormat(events[index].WireFormat, revision)
		formats[index] = format
		if format != contract.WireFormatStandardRawEvent && format != contract.WireFormatPythonCompatible {
			refused[index] = refusal{rule: observability.OutputRejectFormatUnsupported, detail: fmt.Sprintf("unsupported output format %q", format)}
			continue
		}
		if format == contract.WireFormatStandardRawEvent {
			if !sink.headersSupported() {
				// Decided before the converter runs and before the client
				// is asked: the record would carry a header and the
				// negotiated protocol has none. Named with the brokers'
				// own answers, so the reader sees which cluster and why;
				// the Python-compatible output on the same sink is not
				// affected, it carries no header. It is the deployment's,
				// the same for every event, so it refuses the batch whole.
				return outputRejected(contract.ReasonOutputClientRejected,
					"the standard RawEvent carries the tenant in a record header and the brokers accept no Produce version that carries headers: "+sink.protocol.String(),
					format, &events[index])
			}
			converted, convertErr := sink.standardConverter.Convert(&events[index])
			if convertErr != nil {
				rule, named := linkdoutput.RuleOf(convertErr)
				if !named {
					rule = observability.OutputRejectOther
				}
				refused[index] = refusal{rule: rule, detail: convertErr.Error()}
				continue
			}
			// Keyed by the alert identity, so one alert's history stays on one
			// partition and its trigger and its resolution arrive in order.
			// The tenant rides in a header as well as in the payload: the
			// consumer's adapter reads the header and its cleaner reads the
			// payload, and the two have to agree.
			messages[index] = &sarama.ProducerMessage{
				Topic:   sink.core.outputTopic,
				Key:     sarama.StringEncoder(converted.AlertID),
				Value:   sarama.ByteEncoder(converted.Payload),
				Headers: []sarama.RecordHeader{{Key: []byte(tenantHeader), Value: []byte(converted.TenantID)}},
			}
			continue
		}
		if format == contract.WireFormatPythonCompatible {
			if events[index].LegacyOutput == nil || events[index].LegacyOutput.Configuration == nil {
				refused[index] = refusal{rule: observability.OutputRejectLegacyContextMissing, detail: "legacy event has no frozen compatibility context"}
				continue
			}
			// The Python protocol carries anomaly points and nothing else: its
			// producer builds every message from the anomaly list and stamps
			// ABNORMAL, and recovery is decided downstream from the absence of
			// anomalies. alarmd's own Recovery is a steady-state result, so
			// every healthy series produces one every cycle - converting those
			// put a hundred times Python's volume on the topic. A Recovery
			// event has no representation in this protocol, so it produces no
			// message here and no snapshot; the native protocol still carries
			// it, because the choice of protocol is the revision's, not the
			// event kind's.
			if !contract.EventHasMessage(format, events[index].EventKind) {
				messages[index] = nil
				continue
			}
			key := events[index].TenantID + "\x00" + events[index].BusinessID
			groups[key] = append(groups[key], index)
		}
	}
	for _, indices := range groups {
		batch := make([]contract.TriggerEventV1, len(indices))
		for i, index := range indices {
			batch[i] = events[index]
		}
		converted, failures, err := sink.legacyConverter.ConvertEach(ctx, batch)
		if err != nil {
			// The legacy converter writes its snapshot store once, after it
			// has judged every event, and says so when that is what failed
			// (legacyoutput.SnapshotStoreError marks RetryableOutputDependency);
			// a cancelled context is the caller's. Anything else it returns
			// for the whole group is its own answer about these events, which
			// it gives again on every retry.
			if ctx.Err() != nil || isRetryableDependency(err) {
				return &triggerEventDependencyError{err: fmt.Errorf("legacy conversion failed: %w", err)}
			}
			for _, index := range indices {
				refused[index] = refusal{rule: observability.OutputRejectLegacyConversion, detail: "legacy conversion failed: " + err.Error()}
			}
			continue
		}
		if len(converted) != len(batch) || len(failures) != len(batch) {
			for _, index := range indices {
				refused[index] = refusal{rule: observability.OutputRejectLegacyOutputInvalid, detail: "legacy conversion result count mismatch"}
			}
			continue
		}
		for i, item := range converted {
			index := indices[i]
			switch {
			case failures[i] != nil:
				rule := observability.OutputRejectLegacyConversion
				var config *legacyoutput.StrategyConfigError
				if errors.As(failures[i], &config) {
					rule = observability.OutputRejectLegacyStrategyInvalid
				}
				refused[index] = refusal{rule: rule, detail: "legacy conversion failed: " + failures[i].Error()}
			case len(item.Payload) > sink.maxLegacyBytes:
				refused[index] = refusal{rule: observability.OutputRejectLegacyPayloadTooLarge,
					detail: fmt.Sprintf("legacy payload of %d bytes exceeds %d", len(item.Payload), sink.maxLegacyBytes)}
			case item.EventID != batch[i].EventID || len(item.Payload) == 0 || !json.Valid(item.Payload) || len(item.DedupeMD5) != 32 || strings.ToLower(item.DedupeMD5) != item.DedupeMD5:
				refused[index] = refusal{rule: observability.OutputRejectLegacyOutputInvalid, detail: "legacy conversion returned invalid event identity/payload"}
			default:
				if _, err := hex.DecodeString(item.DedupeMD5); err != nil {
					refused[index] = refusal{rule: observability.OutputRejectLegacyOutputInvalid, detail: "legacy conversion returned a non-hex dedupe identity: " + err.Error()}
					continue
				}
				messages[index] = &sarama.ProducerMessage{Topic: sink.legacyTopic, Key: sarama.StringEncoder(item.DedupeMD5), Value: sarama.ByteEncoder(item.Payload)}
			}
		}
	}
	// A series goes out whole or not at all: its State moves as one, so an
	// event of it that was written while its sibling was refused would sit
	// at the consumer with no State behind it -- an alert whose recovery
	// this process could never decide. Every event of a series with a
	// refused event is withheld beside it.
	withheld := withholdSeriesOf(events, refused)
	for index := range events {
		if refused[index].rule != "" || withheld[index] {
			messages[index] = nil
		}
	}
	partial := partialRejection(events, formats, refused, withheld)
	if partial != nil {
		observability.ReportOutputRejected(ctx, partial.facts(), len(partial.Withheld))
		if partial.whole {
			first := partial.Rejected[0]
			rejected := outputRejected(contract.ReasonOutputConversionRejected, first.Detail, first.Format, &events[first.index])
			return rejected
		}
	}
	// Which events the protocol had no message for, by format and kind, before
	// the nil slots are compacted away: this is the only place that knows
	// which slot stayed empty and why, and the caller must not re-derive the
	// protocol's rule to find out.
	withoutMessageBy := map[withoutMessageKey]int64{}
	for index, message := range messages {
		if message == nil && refused[index].rule == "" && !withheld[index] {
			withoutMessageBy[withoutMessageKey{format: formats[index], eventKind: events[index].EventKind}]++
		}
	}
	buckets := make([]observability.OutputWithoutMessage, 0, len(withoutMessageBy))
	for key, count := range withoutMessageBy {
		buckets = append(buckets, observability.OutputWithoutMessage{Format: key.format, EventKind: key.eventKind, Events: count})
	}
	published := messages[:0]
	for _, message := range messages {
		if message != nil {
			published = append(published, message)
		}
	}
	messages = published
	// The count is the sink's to give: how many messages the batch became
	// and how many events the protocol had no message for. A batch of
	// recoveries under the Python-compatible protocol is zero messages and
	// a success, and the caller's line has to be able to say so.
	withoutMessage := 0
	for _, bucket := range buckets {
		withoutMessage += int(bucket.Events)
	}
	observability.ReportOutputWrite(ctx, len(messages), withoutMessage, buckets)
	if len(messages) == 0 {
		return partial.err()
	}
	if err := sink.core.writeMessages(ctx, messages); err != nil {
		publishErr := fmt.Errorf("kafka trigger event sink: publish batch: %w", err)
		if errors.Is(err, ErrDecisionSinkClosed) || ctx.Err() != nil {
			return publishErr
		}
		if detail, rejected := clientRejection(err); rejected {
			// The client, not a broker: nothing was sent and nothing will
			// be by retrying. Named for the first event of the batch; the
			// detail is the client's own sentence.
			return outputRejected(contract.ReasonOutputClientRejected, detail, string(contract.ResolveOutputWireFormat(events[0].WireFormat, eventRevision(events[0]))), &events[0])
		}
		return &triggerEventDependencyError{err: publishErr}
	}
	return partial.err()
}

func isRetryableDependency(err error) bool {
	var dependency interface{ RetryableOutputDependency() }
	return errors.As(err, &dependency) && dependency != nil
}

func eventRevision(event contract.TriggerEventV1) int64 {
	if event.StrategyRef == nil {
		return 0
	}
	return event.StrategyRef.Revision
}

func (sink *TriggerEventSink) Shutdown(ctx context.Context) error {
	if sink == nil || sink.core == nil {
		return nil
	}
	return sink.core.Shutdown(ctx)
}

func (sink *TriggerEventSink) Close() error {
	if sink == nil || sink.core == nil {
		return nil
	}
	return sink.core.Close()
}

// withoutMessageKey is what an event the protocol had no message for is
// bucketed by: its resolved format and its kind, and nothing else. A type of
// its own rather than the reported bucket with the count left zero, so that
// which fields take part in the bucketing is said by the type -- a field
// added to the reported bucket later cannot split the buckets, and the sum
// over them would go on equalling the total while the buckets quietly
// multiplied, which no assertion on the total would catch.
type withoutMessageKey struct {
	format    string
	eventKind string
}
