package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
)

func runBusinessCaptureFile(ctx context.Context, cfg config.ComparatorConfig, path string, out, stderr io.Writer) int {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(stderr, "business capture unavailable")
		return 1
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var header comparator.BusinessCaptureHeader
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &header) != nil || contract.ValidateValidationEpochManifestV1(&header.Manifest) != nil || header.Manifest.AuditTopic.Name != cfg.Kafka.AuditOutputTopic {
		fmt.Fprintln(stderr, "business frozen Audit topic mismatch")
		return 1
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return 1
	}
	sink, err := enginekafka.OpenComparisonAuditSink(cfg.Kafka.AuditSinkCoordinates())
	if err != nil {
		fmt.Fprintln(stderr, "business Audit producer unavailable")
		return 1
	}
	defer sink.Close()
	return runBusinessCaptureStream(ctx, f, sink, out, stderr)
}
func runBusinessCaptureStream(ctx context.Context, input io.Reader, sink comparator.BusinessAuditSink, out, stderr io.Writer) int {
	status, offsets, err := comparator.RunBusinessCapture(ctx, input, sink)
	if err != nil {
		fmt.Fprintln(stderr, "business capture incomplete; source offsets not committed")
		return 1
	}
	type coordinate struct {
		comparator.BusinessPartition
		Next int64 `json:"next_offset"`
	}
	coordinates := make([]coordinate, 0, len(offsets))
	for p, next := range offsets {
		coordinates = append(coordinates, coordinate{p, next})
	}
	sort.Slice(coordinates, func(i, j int) bool {
		if coordinates[i].Topic == coordinates[j].Topic {
			return coordinates[i].Partition < coordinates[j].Partition
		}
		return coordinates[i].Topic < coordinates[j].Topic
	})
	if err = json.NewEncoder(out).Encode(struct {
		Status  string       `json:"abnormal_cohort_status"`
		Scope   string       `json:"scope"`
		Offsets []coordinate `json:"audit_acked_committable_offsets"`
	}{status, "ABNORMAL_COHORT_NOT_G5_VERDICT", coordinates}); err != nil {
		return 1
	}
	if status == "FAILED" || status == "UNPROVEN" || status == "PENDING" {
		return 1
	}
	return 0
}
