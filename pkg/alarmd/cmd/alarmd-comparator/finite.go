package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
)

type finiteCaptureRequest struct {
	Schema        string                    `json:"schema"`
	Ranges        []enginekafka.FiniteRange `json:"ranges"`
	MaxRecords    int64                     `json:"max_records"`
	MaxBytes      int64                     `json:"max_bytes"`
	MaxPartitions int                       `json:"max_partitions"`
	TimeoutMillis int64                     `json:"timeout_millis"`
}

func runFiniteCaptureFile(ctx context.Context, cfg config.ComparatorConfig, path string, out, stderr io.Writer) int {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(stderr, "finite range file unavailable")
		return 1
	}
	defer f.Close()
	wire, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(wire) > 1<<20 {
		return 1
	}
	request, err := decodeFiniteRequest(wire, cfg.Kafka.GoDecisionTopic)
	if err != nil {
		fmt.Fprintln(stderr, "finite range request invalid")
		return 1
	}
	// Reuse actual configured coordinates. GroupID is existing config metadata;
	// the finite transport never opens a group or commits its offsets.
	saramaCfg, err := enginekafka.NewSaramaConfig(enginekafka.Config{Brokers: cfg.Kafka.Brokers, Topic: cfg.Kafka.GoDecisionTopic, GroupID: cfg.Kafka.GroupID, ClientID: cfg.Kafka.ClientID, BrokerVersion: cfg.Kafka.BrokerVersion})
	if err != nil {
		fmt.Fprintln(stderr, "finite client configuration invalid")
		return 1
	}
	source, err := enginekafka.OpenFiniteSource(cfg.Kafka.Brokers, saramaCfg)
	if err != nil {
		fmt.Fprintln(stderr, "finite source unavailable")
		return 1
	}
	code := runFiniteCapture(ctx, source, request, out)
	if source.Close() != nil {
		return 1
	}
	return code
}
func decodeFiniteRequest(wire []byte, topic string) (finiteCaptureRequest, error) {
	var r finiteCaptureRequest
	if _, err := contract.CanonicalJSONV2(json.RawMessage(wire)); err != nil {
		return r, err
	}
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if r.Schema != "go-finite-capture-v1" || r.TimeoutMillis <= 0 || r.TimeoutMillis > int64((1<<63-1)/time.Millisecond) || r.MaxPartitions <= 0 || len(r.Ranges) == 0 || len(r.Ranges) > r.MaxPartitions || r.MaxBytes <= 0 || r.MaxRecords <= 0 {
		return r, fmt.Errorf("finite bounds")
	}
	for _, p := range r.Ranges {
		if p.Topic != topic || p.Partition < 0 || p.Start < 0 || p.End < p.Start {
			return r, fmt.Errorf("finite configured Topic or range")
		}
	}
	return r, nil
}
func runFiniteCapture(ctx context.Context, source enginekafka.FiniteSource, r finiteCaptureRequest, out io.Writer) int {
	encoder := json.NewEncoder(out)
	// Archive the frozen input before records; callers retain complete JSONL even on GAP.
	if encoder.Encode(r) != nil {
		return 1
	}
	summary, err := enginekafka.ReadFinite(ctx, source, r.Ranges, enginekafka.FiniteLimits{MaxRecords: r.MaxRecords, MaxBytes: r.MaxBytes, MaxPartitions: r.MaxPartitions, Timeout: time.Duration(r.TimeoutMillis) * time.Millisecond}, func(record enginekafka.FiniteRecord) error { return encoder.Encode(record) })
	if encoder.Encode(struct {
		Schema  string                    `json:"schema"`
		Summary enginekafka.FiniteSummary `json:"summary"`
	}{"go-finite-capture-summary-v1", summary}) != nil {
		return 1
	}
	if err != nil || !summary.ReferenceComplete {
		return 1
	}
	return 0
}
