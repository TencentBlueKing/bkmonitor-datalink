package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFiniteRequestUsesActualTopicAndStrictFrozenBounds(t *testing.T) {
	good := `{"schema":"go-finite-capture-v1","ranges":[{"topic":"go","partition":0,"start":0,"end":1}],"max_records":10,"max_bytes":1024,"max_partitions":1,"timeout_millis":1000}`
	if _, err := decodeFiniteRequest([]byte(good), "go"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(good, `"topic":"go"`, `"topic":"business"`, 1),
		strings.Replace(good, `"end":1`, `"end":-1`, 1),
		strings.Replace(good, `"max_bytes":1024`, `"max_bytes":0`, 1),
		strings.Replace(good, `"schema":`, `"extra":1,"schema":`, 1),
		strings.Replace(good, `"max_records":10`, `"max_records":10,"max_records":20`, 1),
	} {
		if _, err := decodeFiniteRequest([]byte(bad), "go"); err == nil {
			t.Fatal("invalid range accepted")
		}
	}
}

func TestFiniteCLIActualSaramaArchivesBadFrameAndSummary(t *testing.T) {
	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).SetBroker(broker.Addr(), broker.BrokerID()).SetLeader("go", 0, broker.BrokerID()),
		"OffsetRequest":   sarama.NewMockOffsetResponse(t).SetVersion(1).SetOffset("go", 0, sarama.OffsetOldest, 0).SetOffset("go", 0, sarama.OffsetNewest, 1),
		"FetchRequest":    sarama.NewMockFetchResponse(t, 1).SetVersion(3).SetHighWaterMark("go", 0, 1).SetMessage("go", 0, 0, sarama.StringEncoder("bad JSON")),
	})
	cfg := config.DefaultComparator()
	cfg.Kafka.Brokers = []string{broker.Addr()}
	cfg.Kafka.GoDecisionTopic = "go"
	cfg.Kafka.BrokerVersion = "0.10.2.0"
	cfg.Kafka.GroupID = "configured-test-group"
	cfg.Kafka.ClientID = "finite-test"
	input := filepath.Join(t.TempDir(), "range.json")
	wire := []byte(`{"schema":"go-finite-capture-v1","ranges":[{"topic":"go","partition":0,"start":0,"end":1}],"max_records":10,"max_bytes":1024,"max_partitions":1,"timeout_millis":1000}`)
	if err := os.WriteFile(input, wire, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := runFiniteCaptureFile(context.Background(), cfg, input, &out, &stderr)
	if code != 1 {
		t.Fatal("bad frame cannot make reference complete", code)
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("records=%d stderr=%s", len(lines), stderr.String())
	}
	var summary struct {
		Summary struct {
			Complete          bool
			ReferenceComplete bool `json:"reference_complete"`
			Records           int
		}
	}
	if err := json.Unmarshal(lines[2], &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.Summary.Complete || summary.Summary.ReferenceComplete || summary.Summary.Records != 1 {
		t.Fatalf("summary=%s", lines[2])
	}
	for _, request := range broker.History() {
		switch request.Request.(type) {
		case *sarama.MetadataRequest, *sarama.OffsetRequest, *sarama.FetchRequest:
		default:
			t.Fatalf("group request %T", request.Request)
		}
	}
}
