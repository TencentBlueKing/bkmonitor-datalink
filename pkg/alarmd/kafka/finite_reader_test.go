package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Shopify/sarama"
)

type finiteTestPartition struct {
	messages chan *sarama.ConsumerMessage
	errs     chan *sarama.ConsumerError
	closed   bool
}

func (p *finiteTestPartition) Messages() <-chan *sarama.ConsumerMessage { return p.messages }
func (p *finiteTestPartition) Errors() <-chan *sarama.ConsumerError     { return p.errs }
func (p *finiteTestPartition) Close() error                             { p.closed = true; return nil }

type finiteTestSource struct {
	partition  *finiteTestPartition
	low, high  int64
	partitions []int32
}

func (s *finiteTestSource) Partitions(string) ([]int32, error) { return s.partitions, nil }
func (s *finiteTestSource) GetOffset(_ string, _ int32, at int64) (int64, error) {
	if at == sarama.OffsetOldest {
		return s.low, nil
	}
	return s.high, nil
}
func (s *finiteTestSource) ConsumePartition(string, int32, int64) (FinitePartition, error) {
	return s.partition, nil
}
func finiteFixture(offsets ...int64) *finiteTestSource {
	p := &finiteTestPartition{messages: make(chan *sarama.ConsumerMessage, len(offsets)), errs: make(chan *sarama.ConsumerError)}
	for _, offset := range offsets {
		p.messages <- &sarama.ConsumerMessage{Topic: "fixture", Partition: 0, Offset: offset, Value: []byte("invalid JSON")}
	}
	return &finiteTestSource{partition: p, high: 3, partitions: []int32{0}}
}
func TestFiniteReaderRetainsBadFramesAndStopsAtFrozenEnd(t *testing.T) {
	source := finiteFixture(0, 1, 2)
	var records []FiniteRecord
	result, err := ReadFinite(context.Background(), source, []FiniteRange{{Topic: "fixture", Partition: 0, Start: 0, End: 2}}, FiniteLimits{MaxRecords: 10, MaxBytes: 1024, MaxPartitions: 2, Timeout: time.Second}, func(r FiniteRecord) error { records = append(records, r); return nil })
	if err != nil || !result.Complete || result.ReferenceComplete || len(records) != 2 || !source.partition.closed {
		t.Fatalf("result=%+v err=%v records=%d", result, err, len(records))
	}
	if string(records[0].Value) != "invalid JSON" || records[0].ValueSHA256 == "" || records[0].Kind != FiniteGap || records[1].Offset != 1 {
		t.Fatalf("raw lost: %+v", records)
	}
}
func TestFiniteReaderRejectsMissingOffsetsAndRetention(t *testing.T) {
	for _, test := range []struct {
		name   string
		source *finiteTestSource
	}{{"hole", finiteFixture(1)}, {"retention", finiteFixture(0)}} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "retention" {
				test.source.low = 1
			}
			result, err := ReadFinite(context.Background(), test.source, []FiniteRange{{Topic: "fixture", Partition: 0, Start: 0, End: 2}}, FiniteLimits{MaxRecords: 10, MaxBytes: 1024, MaxPartitions: 2, Timeout: time.Second}, func(FiniteRecord) error { return nil })
			if err == nil || result.Complete {
				t.Fatalf("accepted gap: %+v", result)
			}
		})
	}
}
func TestFiniteReaderEmitFailureClosesWithoutCommit(t *testing.T) {
	s := finiteFixture(0)
	r, err := ReadFinite(context.Background(), s, []FiniteRange{{Topic: "fixture", End: 1}}, FiniteLimits{MaxRecords: 10, MaxBytes: 1024, MaxPartitions: 2, Timeout: time.Second}, func(FiniteRecord) error { return errors.New("private sink failure") })
	if err == nil || r.Complete || !s.partition.closed {
		t.Fatalf("result=%+v err=%v", r, err)
	}
}

func TestFiniteReaderActualSaramaClientHasNoGroupRequests(t *testing.T) {
	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).SetBroker(broker.Addr(), broker.BrokerID()).SetLeader("fixture", 0, broker.BrokerID()),
		"OffsetRequest":   sarama.NewMockOffsetResponse(t).SetVersion(1).SetOffset("fixture", 0, sarama.OffsetOldest, 0).SetOffset("fixture", 0, sarama.OffsetNewest, 1),
		"FetchRequest":    sarama.NewMockFetchResponse(t, 1).SetVersion(3).SetHighWaterMark("fixture", 0, 1).SetMessage("fixture", 0, 0, sarama.StringEncoder("bad JSON")),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_10_2_0
	source, err := OpenFiniteSource([]string{broker.Addr()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Consumer.Offsets.AutoCommit.Enable {
		t.Fatal("caller configuration mutated")
	}
	result, err := ReadFinite(context.Background(), source, []FiniteRange{{Topic: "fixture", End: 1}}, FiniteLimits{MaxRecords: 2, MaxBytes: 1024, MaxPartitions: 1, Timeout: 5 * time.Second}, func(FiniteRecord) error { return nil })
	if closeErr := source.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || !result.Complete || result.Records != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, request := range broker.History() {
		switch request.Request.(type) {
		case *sarama.MetadataRequest, *sarama.OffsetRequest, *sarama.FetchRequest:
		default:
			t.Fatalf("unexpected group/other request %T", request.Request)
		}
	}
}

func TestFiniteReaderCapacityCancellationAndEmptyRange(t *testing.T) {
	for _, tc := range []struct {
		name     string
		end      int64
		records  int64
		bytes    int64
		canceled bool
		want     string
	}{
		{"records", 2, 1, 1024, false, "CAPACITY_GAP"},
		{"bytes", 1, 2, 1, false, "CAPACITY_GAP"},
		{"cancel", 1, 2, 1024, true, "CANCELED_OR_TIMEOUT"},
		{"empty", 0, 2, 1024, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := finiteFixture(0, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			got, err := ReadFinite(ctx, src, []FiniteRange{{Topic: "fixture", End: tc.end}}, FiniteLimits{MaxRecords: tc.records, MaxBytes: tc.bytes, MaxPartitions: 1, Timeout: time.Second}, func(FiniteRecord) error { return nil })
			if got.Reason != tc.want || (err == nil) != (tc.want == "") {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if tc.name == "records" && (got.Records != 1 || got.Partitions[0].ReadEnd != 1) {
				t.Fatal("capacity advanced unarchived offset")
			}
		})
	}
}

func TestFiniteReaderFinalRetentionAndPartitionDrift(t *testing.T) {
	for _, shape := range []string{"retention", "population"} {
		t.Run(shape, func(t *testing.T) {
			src := finiteFixture(0)
			got, err := ReadFinite(context.Background(), src, []FiniteRange{{Topic: "fixture", End: 1}}, FiniteLimits{MaxRecords: 2, MaxBytes: 1024, MaxPartitions: 1, Timeout: time.Second}, func(FiniteRecord) error {
				if shape == "retention" {
					src.low = 1
				} else {
					src.partitions = []int32{0, 1}
				}
				return nil
			})
			if err == nil || got.Complete || got.Records != 1 || !src.partition.closed {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}
