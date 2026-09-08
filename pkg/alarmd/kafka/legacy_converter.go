package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type LegacyAdapterConfig struct {
	URL      string        `yaml:"url"`
	TokenEnv string        `yaml:"token_env"`
	Timeout  time.Duration `yaml:"timeout"`
	Topic    string        `yaml:"topic"`
}

type LegacyConvertedEvent struct {
	EventID     string          `json:"event_id"`
	Payload     json.RawMessage `json:"-"`
	PayloadJSON string          `json:"payload_json,omitempty"`
	DedupeMD5   string          `json:"dedupe_md5"`
}

type LegacyEventConverter interface {
	ConvertBatch(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, error)
}

type legacyHTTPConverter struct {
	client   *http.Client
	config   LegacyAdapterConfig
	token    string
	maxBytes int
}

func NewLegacyHTTPConverter(config LegacyAdapterConfig, client *http.Client, maxBytes int) (LegacyEventConverter, error) {
	u, err := url.Parse(config.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || config.Timeout <= 0 || maxBytes <= 0 || !strings.HasPrefix(config.Topic, "alarmd_") {
		return nil, fmt.Errorf("invalid legacy adapter URL/timeout/topic/limit")
	}
	if client == nil {
		return nil, fmt.Errorf("legacy adapter HTTP client required")
	}
	token := os.Getenv(config.TokenEnv)
	if config.TokenEnv == "" || token == "" {
		return nil, fmt.Errorf("legacy adapter bearer token environment is not configured")
	}
	return &legacyHTTPConverter{client: client, config: config, token: token, maxBytes: maxBytes}, nil
}

type legacyRPCEvent struct {
	EventID           string                     `json:"event_id"`
	StrategyKey       string                     `json:"strategy_key"`
	EventKind         string                     `json:"event_kind"`
	PrimaryLevelID    uint32                     `json:"primary_level_id"`
	ItemID            int64                      `json:"item_id"`
	SourceTime        int64                      `json:"source_time"`
	Dimensions        map[string]json.RawMessage `json:"dimensions"`
	DimensionFields   []string                   `json:"dimension_fields"`
	Values            map[string]json.RawMessage `json:"values"`
	Value             json.RawMessage            `json:"value"`
	AnomalyTimestamps []int64                    `json:"anomaly_timestamps"`
	LevelResults      []contract.LevelResultV1   `json:"level_results"`
}

func (c *legacyHTTPConverter) ConvertBatch(ctx context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
	if len(events) == 0 {
		return []LegacyConvertedEvent{}, nil
	}
	if len(events) > 128 {
		return c.convertSplit(ctx, events, 128)
	}
	first := events[0]
	biz, err := strconv.ParseInt(first.BusinessID, 10, 64)
	if err != nil {
		return nil, err
	}
	strategies := make(map[string]json.RawMessage)
	requests := make([]legacyRPCEvent, 0, len(events))
	for _, event := range events {
		if event.TenantID != first.TenantID || event.BusinessID != first.BusinessID || event.StrategyRef != nil || event.LegacyOutput == nil || event.LegacyOutput.Configuration == nil {
			return nil, fmt.Errorf("invalid legacy conversion group/context")
		}
		metadata := event.LegacyOutput.Configuration
		key := metadata.StrategyKey()
		if _, ok := strategies[key]; !ok {
			strategies[key] = metadata.StrategyJSON()
		}
		itemID, err := strconv.ParseInt(metadata.ItemID(), 10, 64)
		if err != nil {
			return nil, err
		}
		requests = append(requests, legacyRPCEvent{EventID: event.EventID, StrategyKey: key, EventKind: event.EventKind, PrimaryLevelID: event.PrimaryLevelID, ItemID: itemID, SourceTime: event.RecordRef.SourceTime, Dimensions: event.RecordRef.Dimensions, DimensionFields: metadata.DimensionFields(), Values: event.Observed.Values, Value: event.Observed.Values["value"], AnomalyTimestamps: event.LegacyOutput.AnomalyTimestamps, LevelResults: event.LevelResults})
	}
	body, err := json.Marshal(map[string]any{"func_name": "alarmd_adapt_legacy_events", "bk_biz_id": biz, "params": map[string]any{"bk_tenant_id": first.TenantID, "bk_biz_id": biz, "strategies": strategies, "events": requests}})
	if err != nil {
		return nil, err
	}
	if len(body) > 4<<20 {
		if len(events) == 1 {
			return nil, fmt.Errorf("legacy conversion request exceeds 4MiB")
		}
		return c.convertSplit(ctx, events, len(events)/2)
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Bk-Tenant-Id", first.TenantID)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// payload_json can expand each payload byte to a six-byte JSON escape.
	limit := 6*int64(c.maxBytes)*int64(len(events)) + int64(4096*len(events)) + 4096
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("legacy adapter response exceeds byte limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("legacy adapter HTTP status %d", resp.StatusCode)
	}
	var envelope struct {
		Result bool `json:"result"`
		Data   struct {
			Result struct {
				Topic  string                 `json:"topic"`
				Events []LegacyConvertedEvent `json:"events"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Result || envelope.Data.Result.Topic != c.config.Topic {
		return nil, fmt.Errorf("legacy adapter failed or returned an unexpected topic")
	}
	for i := range envelope.Data.Result.Events {
		item := &envelope.Data.Result.Events[i]
		if item.PayloadJSON == "" {
			return nil, fmt.Errorf("legacy adapter did not return payload_json")
		}
		item.Payload = json.RawMessage(item.PayloadJSON)
	}
	return envelope.Data.Result.Events, nil
}

func (c *legacyHTTPConverter) convertSplit(ctx context.Context, events []contract.TriggerEventV1, size int) ([]LegacyConvertedEvent, error) {
	var output []LegacyConvertedEvent
	for start := 0; start < len(events); start += size {
		end := start + size
		if end > len(events) {
			end = len(events)
		}
		part, err := c.ConvertBatch(ctx, events[start:end])
		if err != nil {
			return nil, err
		}
		output = append(output, part...)
	}
	return output, nil
}
