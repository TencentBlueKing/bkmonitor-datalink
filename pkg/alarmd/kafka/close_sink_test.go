package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
)

func closeSinkRequest() linkdoutput.CloseRequest {
	return linkdoutput.CloseRequest{TenantID: "tenant", Fingerprint: strings.Repeat("a", 32), AlertInstanceID: "active-instance",
		StrategyID: 123, StrategyRevision: 4, BusinessID: 2, OccurredAt: time.Unix(1700000000, 0)}
}

func TestCloseSinkUsesNativeProducerKeyAndTenantHeader(t *testing.T) {
	var sent []*sarama.ProducerMessage
	sink, err := newTriggerEventSink("native-events", &fakeSyncProducer{send: func(m *sarama.ProducerMessage) (int32, int64, error) {
		sent = append(sent, m)
		return 0, 0, nil
	}}, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	r := closeSinkRequest()
	if err := sink.WriteCloseBatch(context.Background(), []linkdoutput.CloseRequest{r}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	m := sent[0]
	key, err := m.Key.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if m.Topic != "native-events" || string(key) != r.Fingerprint {
		t.Fatalf("topic=%s key=%s", m.Topic, key)
	}
	if len(m.Headers) != 1 || string(m.Headers[0].Key) != "bk_tenant_id" || string(m.Headers[0].Value) != r.TenantID {
		t.Fatalf("headers=%v", m.Headers)
	}
	data, err := m.Value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		AlertID     string `json:"alert_id"`
		Evaluations []struct {
			Severity string `json:"severity"`
			Action   string `json:"action"`
			Reason   string `json:"action_reason"`
		} `json:"evaluations"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.AlertID != r.Fingerprint || len(wire.Evaluations) != 1 || wire.Evaluations[0].Severity != linkdoutput.SeverityAllLevels || wire.Evaluations[0].Action != "closed" || wire.Evaluations[0].Reason != "strategy_inactive" {
		t.Fatalf("payload=%s", data)
	}
}

func TestCloseSinkRejectsWholeInvalidBatchBeforeProducer(t *testing.T) {
	for name, mutate := range map[string]func(*linkdoutput.CloseRequest){
		"tenant":      func(r *linkdoutput.CloseRequest) { r.TenantID = "" },
		"instance":    func(r *linkdoutput.CloseRequest) { r.AlertInstanceID = "" },
		"fingerprint": func(r *linkdoutput.CloseRequest) { r.Fingerprint = "bad" },
		"strategy":    func(r *linkdoutput.CloseRequest) { r.StrategyID = 0 },
		"revision":    func(r *linkdoutput.CloseRequest) { r.StrategyRevision = 0 },
		"business":    func(r *linkdoutput.CloseRequest) { r.BusinessID = 0 },
		"time":        func(r *linkdoutput.CloseRequest) { r.OccurredAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			sent := 0
			sink, err := newTriggerEventSink("native", &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { sent++; return 0, 0, nil }}, &fakeCloser{})
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()
			bad := closeSinkRequest()
			mutate(&bad)
			if err := sink.WriteCloseBatch(context.Background(), []linkdoutput.CloseRequest{closeSinkRequest(), bad}); err == nil || sent != 0 {
				t.Fatalf("err=%v sent=%d", err, sent)
			}
		})
	}
}

func TestCloseSinkHonorsLeaseAdmissionAndCancelledOwnerContext(t *testing.T) {
	now := time.Unix(1700000000, 0)
	for _, name := range []string{"expired", "insufficient", "boundary", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			sent := 0
			sink, err := newTriggerEventSink("native", &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { sent++; return 0, 0, nil }}, &fakeCloser{})
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()
			sink.now = func() time.Time { return now }
			deadline := now.Add(OutputAdmissionMargin + OutputBatchBound)
			if name == "expired" {
				deadline = now.Add(-time.Second)
			}
			if name == "insufficient" {
				deadline = deadline.Add(-time.Millisecond)
			}
			ctx := execution.ContextWithLeaseAuthority(context.Background(), fixedLease{deadline: deadline})
			if name == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err = sink.WriteCloseBatch(ctx, []linkdoutput.CloseRequest{closeSinkRequest()})
			if name == "boundary" {
				if err != nil || sent != 1 {
					t.Fatalf("err=%v sent=%d", err, sent)
				}
				return
			}
			if sent != 0 {
				t.Fatalf("rejected owner sent %d", sent)
			}
			if name == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			var deferred *OutputDeferredError
			if !errors.As(err, &deferred) {
				t.Fatalf("expected lease deferral, got %v", err)
			}
		})
	}
}

func TestCloseSinkRefusesBrokerWithoutTenantHeaders(t *testing.T) {
	sent := 0
	sink, err := newTriggerEventSink("native", &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { sent++; return 0, 0, nil }}, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	negotiation, err := negotiateProtocol([]string{"a:9092"}, sinkConfigAtFloor(), &scriptedAPIVersions{answers: map[string]*sarama.ApiVersionsResponse{"a:9092": produceUpTo(2)}})
	if err != nil {
		t.Fatal(err)
	}
	sink.protocol = &negotiation
	if err := sink.WriteCloseBatch(context.Background(), []linkdoutput.CloseRequest{closeSinkRequest()}); err == nil || sent != 0 {
		t.Fatalf("err=%v sent=%d", err, sent)
	}
}

func TestCloseSinkWritesNegativeBusinessIdentity(t *testing.T) {
	var sent []*sarama.ProducerMessage
	sink, err := newTriggerEventSink("native-events", &fakeSyncProducer{send: func(m *sarama.ProducerMessage) (int32, int64, error) {
		sent = append(sent, m)
		return 0, 0, nil
	}}, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	request := closeSinkRequest()
	request.BusinessID = -42
	if err := sink.WriteCloseBatch(context.Background(), []linkdoutput.CloseRequest{request}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	data, err := sent[0].Value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Labels struct {
			BusinessID int64 `json:"bk_biz_id"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Labels.BusinessID != request.BusinessID {
		t.Fatalf("business identity changed: %s", data)
	}
}
