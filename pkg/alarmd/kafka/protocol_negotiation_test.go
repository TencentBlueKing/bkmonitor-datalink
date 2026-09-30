// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// scriptedAPIVersions plays a cluster: the bootstrap address answers
// Metadata with every broker that has an answer or an error scripted, and
// each broker answers ApiVersions from the script.
type scriptedAPIVersions struct {
	answers     map[string]*sarama.ApiVersionsResponse
	errs        map[string]error
	metadataErr map[string]error
	asked       []string
	bootstraps  []string
}

func (client *scriptedAPIVersions) Brokers(bootstrap string, _ *sarama.Config) ([]clusterBroker, error) {
	client.bootstraps = append(client.bootstraps, bootstrap)
	if err := client.metadataErr[bootstrap]; err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(client.answers)+len(client.errs))
	for addr := range client.answers {
		addrs = append(addrs, addr)
	}
	for addr := range client.errs {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	listed := make([]clusterBroker, 0, len(addrs))
	for index, addr := range addrs {
		listed = append(listed, clusterBroker{ID: int32(index + 1), Address: addr})
	}
	return listed, nil
}

func (client *scriptedAPIVersions) ApiVersions(addr string, _ *sarama.Config) (*sarama.ApiVersionsResponse, error) {
	client.asked = append(client.asked, addr)
	if err := client.errs[addr]; err != nil {
		return nil, err
	}
	return client.answers[addr], nil
}

func produceUpTo(max int16) *sarama.ApiVersionsResponse {
	return &sarama.ApiVersionsResponse{ApiVersions: []*sarama.ApiVersionsResponseBlock{
		{ApiKey: 3, MinVersion: 0, MaxVersion: 4},
		{ApiKey: produceAPIKey, MinVersion: 0, MaxVersion: max},
	}}
}

// The protocol the producer speaks is decided from every broker's own
// answer: the newest version they all accept, between the floor and the
// version record headers need. One broker short of headers keeps the whole
// cluster on the floor, by name; one broker that does not answer decides
// nothing, and says which.
func TestTheProtocolIsTheNewestVersionEveryBrokerAccepts(t *testing.T) {
	t.Parallel()

	config := sarama.NewConfig()
	config.Version = minimumBrokerVersion
	cases := []struct {
		name       string
		answers    map[string]*sarama.ApiVersionsResponse
		errs       map[string]error
		wantErr    string
		want       string
		wantHeader bool
		wantReason string
		wantProd   int16
	}{
		{name: "all brokers take record batches", answers: map[string]*sarama.ApiVersionsResponse{"a:9092": produceUpTo(7), "b:9092": produceUpTo(3)},
			want: RecordHeaderBrokerVersion, wantHeader: true, wantProd: 3},
		{name: "one broker stops at produce v2", answers: map[string]*sarama.ApiVersionsResponse{"a:9092": produceUpTo(7), "b:9092": produceUpTo(2)},
			want: MinimumBrokerVersion, wantReason: ProtocolReasonUnsupportedByBroker, wantProd: 2},
		{name: "a broker that names no produce api", answers: map[string]*sarama.ApiVersionsResponse{"a:9092": {ApiVersions: []*sarama.ApiVersionsResponseBlock{{ApiKey: 3, MaxVersion: 4}}}},
			want: MinimumBrokerVersion, wantReason: ProtocolReasonUnsupportedByBroker, wantProd: 2},
		{name: "a broker that does not answer", answers: map[string]*sarama.ApiVersionsResponse{"a:9092": produceUpTo(7)},
			errs: map[string]error{"b:9092": errors.New("dial tcp: connection refused")}, wantErr: "b:9092 did not answer"},
		{name: "a broker that refuses", answers: map[string]*sarama.ApiVersionsResponse{"a:9092": {Err: sarama.ErrUnsupportedVersion}},
			wantErr: "a:9092 did not answer"},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			brokers := make([]string, 0, len(test.answers)+len(test.errs))
			for addr := range test.answers {
				brokers = append(brokers, addr)
			}
			for addr := range test.errs {
				brokers = append(brokers, addr)
			}
			client := &scriptedAPIVersions{answers: test.answers, errs: test.errs}
			// One bootstrap name, as a deployment configures it; the brokers
			// come from the cluster's own metadata.
			negotiation, err := negotiateProtocol([]string{"boot:9092"}, config, client)
			if len(client.asked) != len(brokers) {
				t.Fatalf("asked %v, want every broker the metadata listed %v", client.asked, brokers)
			}
			if negotiation.MetadataFrom != "boot:9092" && test.wantErr == "" {
				t.Fatalf("metadata from %q, want the bootstrap that answered", negotiation.MetadataFrom)
			}
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("negotiateProtocol() error = %v, want it to name %q", err, test.wantErr)
				}
				if len(negotiation.Brokers) != len(brokers) {
					t.Fatalf("partial answers = %+v, want one row per broker even on failure", negotiation.Brokers)
				}
				return
			}
			if err != nil {
				t.Fatalf("negotiateProtocol() error = %v", err)
			}
			if negotiation.Negotiated != test.want || negotiation.Version().String() != test.want ||
				negotiation.HeadersSupported != test.wantHeader || negotiation.Reason != test.wantReason ||
				negotiation.ProduceVersion != test.wantProd {
				t.Fatalf("negotiation = %s (reason %q, produce v%d), want %s headers=%t reason=%q produce v%d",
					negotiation.String(), negotiation.Reason, negotiation.ProduceVersion, test.want, test.wantHeader, test.wantReason, test.wantProd)
			}
			if negotiation.Configured != MinimumBrokerVersion || negotiation.Wanted != RecordHeaderBrokerVersion || negotiation.WantedProduceVersion != 3 {
				t.Fatalf("negotiation names configured %s wanted %s produce v%d, want %s / %s / v3",
					negotiation.Configured, negotiation.Wanted, negotiation.WantedProduceVersion, MinimumBrokerVersion, RecordHeaderBrokerVersion)
			}
			for _, broker := range negotiation.Brokers {
				if !broker.Answered || broker.Error != "" || broker.ID <= 0 {
					t.Fatalf("broker row %+v, want answered with no error and the node id the metadata gave", broker)
				}
			}
		})
	}
}

// A bootstrap address that answers no Metadata decides nothing; the next
// bootstrap address is tried, and when none answers the negotiation fails
// with no brokers asked -- the producer is not opened on a guess.
func TestTheBrokerListComesFromTheFirstBootstrapThatAnswers(t *testing.T) {
	t.Parallel()

	config := sinkConfigAtFloor()
	client := &scriptedAPIVersions{
		answers:     map[string]*sarama.ApiVersionsResponse{"a:9092": produceUpTo(3)},
		metadataErr: map[string]error{"dead:9092": errors.New("dial tcp: connection refused")},
	}
	negotiation, err := negotiateProtocol([]string{"dead:9092", "boot:9092"}, config, client)
	if err != nil || negotiation.MetadataFrom != "boot:9092" || len(client.bootstraps) != 2 || len(client.asked) != 1 {
		t.Fatalf("negotiateProtocol() = (%s, %v), bootstraps asked %v, brokers asked %v; want the second bootstrap's metadata and one broker asked",
			negotiation.String(), err, client.bootstraps, client.asked)
	}
	client = &scriptedAPIVersions{
		answers:     map[string]*sarama.ApiVersionsResponse{"a:9092": produceUpTo(3)},
		metadataErr: map[string]error{"dead:9092": errors.New("dial tcp: connection refused")},
	}
	_, err = negotiateProtocol([]string{"dead:9092"}, config, client)
	if err == nil || !strings.Contains(err.Error(), "dead:9092 did not answer Metadata") || len(client.asked) != 0 {
		t.Fatalf("negotiateProtocol() with no bootstrap answering = %v, brokers asked %v; want a failure naming the bootstrap and no broker asked", err, client.asked)
	}
}

// A standard RawEvent on a sink whose brokers take no headers is refused
// before the converter and before the client, by name, with the brokers'
// own answers in the sentence; the Python-compatible output on the same
// sink is sent.
func TestAStandardRawEventIsRefusedByNameWhereTheBrokersTakeNoHeaders(t *testing.T) {
	t.Parallel()

	sent := 0
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sent++
		return 0, 0, nil
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	negotiation, err := negotiateProtocol([]string{"a:9092"}, sinkConfigAtFloor(), &scriptedAPIVersions{answers: map[string]*sarama.ApiVersionsResponse{"a:9092": produceUpTo(2)}})
	if err != nil {
		t.Fatal(err)
	}
	sink.protocol = &negotiation
	if got := sink.ProtocolNegotiation(); got == nil || got.Reason != ProtocolReasonUnsupportedByBroker || got.HeadersSupported {
		t.Fatalf("ProtocolNegotiation() = %+v, want the floor named unsupported", got)
	}

	standard := triggerEventGolden(t)
	standard.WireFormat = contract.WireFormatStandardRawEvent
	rejected := outputRejectionOf(t, sink.WriteBatch(context.Background(), []contract.TriggerEventV1{standard}))
	if rejected.Reason != contract.ReasonOutputClientRejected || !strings.Contains(rejected.Detail, "record header") ||
		!strings.Contains(rejected.Detail, "a:9092 produce v0..v2") {
		t.Fatalf("rejection = %+v, want %s naming record headers and the broker's answer", rejected, contract.ReasonOutputClientRejected)
	}
	if sent != 0 {
		t.Fatalf("producer sent %d messages for a standard event the protocol cannot carry", sent)
	}

	legacy := legacyEventForTest(t)
	legacy.WireFormat = contract.WireFormatPythonCompatible
	if err := sink.ConfigureLegacyOutput(legacyConverterFunc(func(_ context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
		return []LegacyConvertedEvent{{EventID: events[0].EventID, Payload: json.RawMessage(`{"legacy":true}`), DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479"}}, nil
	}), "alarmd_legacy", 64<<10); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{legacy}); err != nil || sent != 1 {
		t.Fatalf("WriteBatch(legacy) = %v, sent=%d, want the Python-compatible event sent on the floor", err, sent)
	}
}

func sinkConfigAtFloor() *sarama.Config {
	config := sarama.NewConfig()
	config.Version = minimumBrokerVersion
	return config
}

// Against a broker, end to end: the opener negotiates, the producer speaks
// what the broker accepts, and the produce request the broker receives is
// of that version -- v2 on a cluster that stops there, where a fixed 0.11
// producer sent v3 and every real 0.10.2 broker closed the connection; v3
// where the cluster takes record batches, so the header goes through.
func TestTheOpenerSpeaksTheVersionTheBrokerAccepts(t *testing.T) {
	cases := []struct {
		name         string
		produceMax   int16
		wantVersion  int16
		standardSent bool
	}{
		{name: "cluster stops at produce v2", produceMax: 2, wantVersion: 2},
		{name: "cluster takes record batches", produceMax: 3, wantVersion: 3, standardSent: true},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			broker := sarama.NewMockBroker(t, 1)
			defer broker.Close()
			coordinates := validDecisionSinkConfig()
			coordinates.Brokers = []string{broker.Addr()}
			coordinates.BrokerVersion = MinimumBrokerVersion
			broker.SetHandlerByMap(map[string]sarama.MockResponse{
				"MetadataRequest": sarama.NewMockMetadataResponse(t).
					SetBroker(broker.Addr(), broker.BrokerID()).
					SetLeader(coordinates.OutputTopic, 0, broker.BrokerID()).
					SetLeader("alarmd_legacy", 0, broker.BrokerID()),
				"ApiVersionsRequest": sarama.NewMockWrapper(produceUpTo(test.produceMax)),
				"ProduceRequest":     sarama.NewMockProduceResponse(t).SetVersion(test.wantVersion),
			})
			opener, err := PrepareTriggerEventSink(coordinates)
			if err != nil {
				t.Fatal(err)
			}
			sink, err := opener.Open()
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			defer func() { _ = sink.Close() }()
			negotiation := sink.ProtocolNegotiation()
			if negotiation == nil || negotiation.ProduceVersion != test.wantVersion || negotiation.HeadersSupported != test.standardSent {
				t.Fatalf("negotiation = %+v, want produce v%d headers=%t", negotiation, test.wantVersion, test.standardSent)
			}

			legacy := legacyEventForTest(t)
			legacy.WireFormat = contract.WireFormatPythonCompatible
			if err := sink.ConfigureLegacyOutput(legacyConverterFunc(func(_ context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
				return []LegacyConvertedEvent{{EventID: events[0].EventID, Payload: json.RawMessage(`{"legacy":true}`), DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479"}}, nil
			}), "alarmd_legacy", 64<<10); err != nil {
				t.Fatal(err)
			}
			if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{legacy}); err != nil {
				t.Fatalf("WriteBatch(legacy) = %v, want sent on whatever the broker accepts", err)
			}
			standard := triggerEventGolden(t)
			standard.WireFormat = contract.WireFormatStandardRawEvent
			err = sink.WriteBatch(context.Background(), []contract.TriggerEventV1{standard})
			if test.standardSent && err != nil {
				t.Fatalf("WriteBatch(standard) = %v, want sent where the cluster takes headers", err)
			}
			if !test.standardSent {
				if rejected := outputRejectionOf(t, err); rejected.Reason != contract.ReasonOutputClientRejected {
					t.Fatalf("WriteBatch(standard) = %+v, want %s where the cluster takes no headers", rejected, contract.ReasonOutputClientRejected)
				}
			}

			produces := 0
			for _, request := range broker.History() {
				produce, ok := request.Request.(*sarama.ProduceRequest)
				if !ok {
					continue
				}
				produces++
				if produce.Version != test.wantVersion {
					t.Fatalf("broker received produce v%d, want v%d: the version every real broker of this cluster accepts", produce.Version, test.wantVersion)
				}
			}
			wantProduces := 1
			if test.standardSent {
				wantProduces = 2
			}
			if produces != wantProduces {
				t.Fatalf("broker received %d produce requests, want %d", produces, wantProduces)
			}
		})
	}
}

// An open that could not decide the protocol is an open failure with the
// partial answers attached, not a producer on a guess.
func TestAnOpenWithoutAnAnswerFromEveryBrokerDoesNotGuess(t *testing.T) {
	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()
	coordinates := validDecisionSinkConfig()
	coordinates.Brokers = []string{broker.Addr()}
	coordinates.BrokerVersion = MinimumBrokerVersion
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest":    sarama.NewMockMetadataResponse(t).SetBroker(broker.Addr(), broker.BrokerID()),
		"ApiVersionsRequest": sarama.NewMockWrapper(&sarama.ApiVersionsResponse{Err: sarama.ErrUnsupportedVersion}),
	})
	opener, err := PrepareTriggerEventSink(coordinates)
	if err != nil {
		t.Fatal(err)
	}
	_, err = opener.Open()
	var failed *ProtocolNegotiationError
	if err == nil || !errors.As(err, &failed) || len(failed.Negotiation.Brokers) != 1 || failed.Negotiation.Brokers[0].Answered ||
		failed.Negotiation.Brokers[0].ID != broker.BrokerID() {
		t.Fatalf("Open() = %v, want a ProtocolNegotiationError naming the broker that did not answer", err)
	}
}

// The brokers asked are the ones the cluster's metadata names, not the
// bootstrap list: a deployment configures one address, and the broker that
// stops the cluster short of record headers may be one the bootstrap name
// never resolves to. Here the bootstrap broker takes record batches and the
// metadata names a second broker that does not; the negotiation lands on the
// floor and names that second broker by node id.
func TestTheNegotiationAsksEveryBrokerTheMetadataNamesNotJustTheBootstrap(t *testing.T) {
	bootstrap := sarama.NewMockBroker(t, 1)
	defer bootstrap.Close()
	older := sarama.NewMockBroker(t, 2)
	defer older.Close()
	coordinates := validDecisionSinkConfig()
	coordinates.Brokers = []string{bootstrap.Addr()}
	coordinates.BrokerVersion = MinimumBrokerVersion
	metadata := sarama.NewMockMetadataResponse(t).
		SetBroker(bootstrap.Addr(), bootstrap.BrokerID()).
		SetBroker(older.Addr(), older.BrokerID()).
		SetLeader(coordinates.OutputTopic, 0, older.BrokerID())
	bootstrap.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest":    metadata,
		"ApiVersionsRequest": sarama.NewMockWrapper(produceUpTo(7)),
	})
	older.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest":    metadata,
		"ApiVersionsRequest": sarama.NewMockWrapper(produceUpTo(2)),
		"ProduceRequest":     sarama.NewMockProduceResponse(t).SetVersion(2),
	})
	opener, err := PrepareTriggerEventSink(coordinates)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := opener.Open()
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = sink.Close() }()
	negotiation := sink.ProtocolNegotiation()
	if negotiation == nil || negotiation.Negotiated != MinimumBrokerVersion || negotiation.HeadersSupported ||
		negotiation.Reason != ProtocolReasonUnsupportedByBroker || len(negotiation.Brokers) != 2 {
		t.Fatalf("negotiation = %+v, want the floor because the second broker stops at produce v2", negotiation)
	}
	var namedOlder bool
	for _, broker := range negotiation.Brokers {
		if broker.ID == older.BrokerID() && broker.Address == older.Addr() && broker.ProduceMaxVersion == 2 {
			namedOlder = true
		}
	}
	if !namedOlder {
		t.Fatalf("brokers = %+v, want the older broker named by node id %d with produce max v2", negotiation.Brokers, older.BrokerID())
	}
	if negotiation.MetadataFrom != bootstrap.Addr() {
		t.Fatalf("metadata from %q, want the bootstrap address %s", negotiation.MetadataFrom, bootstrap.Addr())
	}
}
