package kafka

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/Shopify/sarama"
)

type FiniteRange struct {
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
}
type FiniteLimits struct {
	MaxRecords    int64
	MaxBytes      int64
	MaxPartitions int
	Timeout       time.Duration
}
type FinitePartition interface {
	Messages() <-chan *sarama.ConsumerMessage
	Errors() <-chan *sarama.ConsumerError
	Close() error
}

// The interface deliberately has no consumer-group or commit operation.
type FiniteSource interface {
	Partitions(string) ([]int32, error)
	GetOffset(string, int32, int64) (int64, error)
	ConsumePartition(string, int32, int64) (FinitePartition, error)
}
type FinitePartitionSummary struct {
	FiniteRange
	Low       int64 `json:"low"`
	High      int64 `json:"high"`
	FinalLow  int64 `json:"final_low"`
	FinalHigh int64 `json:"final_high"`
	ReadEnd   int64 `json:"read_end"`
}
type FiniteSummary struct {
	StartedAt         time.Time                `json:"started_at"`
	FinishedAt        time.Time                `json:"finished_at"`
	Complete          bool                     `json:"complete"`
	ReferenceComplete bool                     `json:"reference_complete"`
	Reason            string                   `json:"reason,omitempty"`
	Records           int64                    `json:"records"`
	Bytes             int64                    `json:"bytes"`
	Gaps              int64                    `json:"gaps"`
	Partitions        []FinitePartitionSummary `json:"partitions"`
}
type SaramaFiniteSource struct {
	client   sarama.Client
	consumer sarama.Consumer
}

// OpenFiniteSource accepts the caller's actual broker/security configuration.
// It opens a simple consumer, never a consumer group, and does not mutate cfg.
func OpenFiniteSource(brokers []string, cfg *sarama.Config) (*SaramaFiniteSource, error) {
	if cfg == nil || len(brokers) == 0 {
		return nil, errors.New("finite capture configuration incomplete")
	}
	for _, b := range brokers {
		if validateBroker(b) != nil {
			return nil, errors.New("finite capture broker invalid")
		}
	}
	copied := *cfg
	copied.Consumer.Return.Errors = true
	copied.Consumer.Offsets.AutoCommit.Enable = false
	copied.Consumer.Fetch.Default = consumerMaxRecordBytes
	copied.Consumer.Fetch.Max = consumerMaxRecordBytes
	copied.ChannelBufferSize = consumerChannelBufferRecords
	if copied.Validate() != nil {
		return nil, errors.New("finite capture configuration invalid")
	}
	client, err := sarama.NewClient(brokers, &copied)
	if err != nil {
		return nil, errors.New("finite capture client unavailable")
	}
	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		_ = client.Close()
		return nil, errors.New("finite capture consumer unavailable")
	}
	return &SaramaFiniteSource{client, consumer}, nil
}
func (s *SaramaFiniteSource) Partitions(topic string) ([]int32, error) {
	// Sarama caches partition metadata; both finite-population checks need a
	// broker refresh, otherwise a newly added partition is invisible.
	if err := s.client.RefreshMetadata(topic); err != nil {
		return nil, errors.New("finite partition metadata unavailable")
	}
	return s.client.Partitions(topic)
}
func (s *SaramaFiniteSource) GetOffset(topic string, p int32, at int64) (int64, error) {
	return s.client.GetOffset(topic, p, at)
}
func (s *SaramaFiniteSource) ConsumePartition(topic string, p int32, offset int64) (FinitePartition, error) {
	return s.consumer.ConsumePartition(topic, p, offset)
}
func (s *SaramaFiniteSource) Close() error {
	a := s.consumer.Close()
	b := s.client.Close()
	if a != nil || b != nil {
		return errors.New("finite capture close failed")
	}
	return nil
}

func ReadFinite(ctx context.Context, source FiniteSource, ranges []FiniteRange, limits FiniteLimits, emit func(FiniteRecord) error) (result FiniteSummary, err error) {
	result.StartedAt = time.Now()
	defer func() { result.FinishedAt = time.Now() }()
	fail := func(reason string) (FiniteSummary, error) {
		result.Reason = reason
		return result, errors.New("finite capture " + reason)
	}
	if source == nil || emit == nil || len(ranges) == 0 || limits.MaxPartitions <= 0 || len(ranges) > limits.MaxPartitions || limits.MaxRecords <= 0 || limits.MaxBytes <= 0 || limits.Timeout <= 0 {
		return fail("INVALID_RANGE_OR_LIMIT")
	}
	ctx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()
	expected := map[string][]int32{}
	for _, r := range ranges {
		if r.Topic == "" || r.Partition < 0 || r.Start < 0 || r.End < r.Start {
			return fail("INVALID_RANGE_OR_LIMIT")
		}
		for _, p := range expected[r.Topic] {
			if p == r.Partition {
				return fail("DUPLICATE_PARTITION")
			}
		}
		expected[r.Topic] = append(expected[r.Topic], r.Partition)
	}
	checkPartitions := func() bool {
		for topic, want := range expected {
			got, e := source.Partitions(topic)
			if e != nil || len(got) != len(want) {
				return false
			}
			got = append([]int32(nil), got...)
			want = append([]int32(nil), want...)
			sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
			sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
			for i := range got {
				if got[i] != want[i] {
					return false
				}
			}
		}
		return true
	}
	if !checkPartitions() {
		return fail("PARTITION_SET_GAP")
	}
	for _, r := range ranges {
		if ctx.Err() != nil {
			return fail("CANCELED_OR_TIMEOUT")
		}
		low, e1 := source.GetOffset(r.Topic, r.Partition, sarama.OffsetOldest)
		high, e2 := source.GetOffset(r.Topic, r.Partition, sarama.OffsetNewest)
		fact := FinitePartitionSummary{FiniteRange: r, Low: low, High: high, FinalLow: low, FinalHigh: high, ReadEnd: r.Start}
		result.Partitions = append(result.Partitions, fact)
		if e1 != nil || e2 != nil || low < 0 || high < low || low > r.Start || high < r.End {
			return fail("OFFSET_OR_RETENTION_GAP")
		}
	}
	for i := range result.Partitions {
		p := &result.Partitions[i]
		if p.Start == p.End {
			continue
		}
		partition, e := source.ConsumePartition(p.Topic, p.Partition, p.Start)
		if e != nil {
			return fail("PARTITION_OPEN_FAILED")
		}
		reason := readFinitePartition(ctx, partition, p, limits, &result, emit)
		if e = partition.Close(); e != nil && reason == "" {
			reason = "PARTITION_CLOSE_FAILED"
		}
		if reason != "" {
			return fail(reason)
		}
	}
	if !checkPartitions() {
		return fail("PARTITION_SET_GAP")
	}
	for i := range result.Partitions {
		p := &result.Partitions[i]
		low, e1 := source.GetOffset(p.Topic, p.Partition, sarama.OffsetOldest)
		high, e2 := source.GetOffset(p.Topic, p.Partition, sarama.OffsetNewest)
		p.FinalLow, p.FinalHigh = low, high
		if e1 != nil || e2 != nil || low < 0 || high < low || low > p.Start || high < p.End {
			return fail("OFFSET_OR_RETENTION_GAP")
		}
	}
	if ctx.Err() != nil {
		return fail("CANCELED_OR_TIMEOUT")
	}
	result.Complete = true
	result.ReferenceComplete = result.Gaps == 0
	return result, nil
}
func readFinitePartition(ctx context.Context, partition FinitePartition, p *FinitePartitionSummary, limits FiniteLimits, result *FiniteSummary, emit func(FiniteRecord) error) string {
	messages, errs := partition.Messages(), partition.Errors()
	for p.ReadEnd < p.End {
		select {
		case <-ctx.Done():
			return "CANCELED_OR_TIMEOUT"
		case _, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			return "FETCH_FAILED"
		case message, ok := <-messages:
			if !ok || message == nil {
				return "PARTITION_ENDED_EARLY"
			}
			if message.Topic != p.Topic || message.Partition != p.Partition || message.Offset != p.ReadEnd {
				return "OFFSET_GAP"
			}
			size := int64(len(message.Key)) + int64(len(message.Value))
			if result.Records >= limits.MaxRecords || size > limits.MaxBytes-result.Bytes || len(message.Value) > MaxConsumerRecordBytes() {
				return "CAPACITY_GAP"
			}
			record := finiteRecord(message, time.Now(), MaxConsumerRecordBytes())
			if emit(record) != nil {
				return "OUTPUT_FAILED"
			}
			result.Records++
			result.Bytes += size
			p.ReadEnd++
			if record.Kind == FiniteGap {
				result.Gaps++
			}
		}
	}
	return ""
}
