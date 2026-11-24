// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/custom"
	"linkd/internal/logging"
)

type matchFailureProcessor struct{ *testProcessor }

func (matchFailureProcessor) Match(context.Context, *Scope) (bool, error) {
	return false, errors.New("match dependency timeout")
}

func TestCustomRuleLogsOriginalCauseWithoutPersistingIt(t *testing.T) {
	var config map[string]any
	if err := json.Unmarshal([]byte(`{"rules":[{"id":"normalize","operations":[{"id":"number","type":"assign","assignments":[{"target":"$.labels.number","value":{"literal":"invalid","transforms":[{"type":"number"}]}}]}]}]}`), &config); err != nil {
		t.Fatal(err)
	}
	program, err := custom.Compile("fields", config)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &output)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := NewChain([]Processor{&testProcessor{name: "fields", fn: func(ctx context.Context, s *Scope) (ProcessorResult, error) { return s.ExecuteRules(ctx, program) }}}, Sources{}, WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), Input{Event: testAlert()})
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["processor"] != "fields" || !strings.Contains(fmt.Sprint(record["error"]), "rule normalize operation number: invalid finite number") {
		t.Fatal(record)
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "invalid finite number") {
		t.Fatal("runtime error persisted in rule trace")
	}
}

func TestProcessorFailureLogsKeepCauseAndContinue(t *testing.T) {
	for _, scenario := range []string{"match", "process", "partial", "panic", "invalid", "success", "optional", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := logging.New(logging.DefaultConfig(), &output)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var first Processor = &testProcessor{name: "resource", fn: func(ctx context.Context, scope *Scope) (ProcessorResult, error) {
				switch scenario {
				case "optional":
					_, _, _ = scope.LogTheme(ctx, 1)
				case "process":
					return ProcessorResult{}, errors.New("resource lookup timeout")
				case "partial":
					_, _, _ = scope.CWStrategy(ctx)
					return ProcessorResult{Status: domain.EnrichStatusPartial}, nil
				case "panic":
					panic("private alert body")
				case "invalid":
					return ProcessorResult{Status: "invalid"}, nil
				case "cancel":
					cancel()
					return ProcessorResult{}, ctx.Err()
				}
				return ProcessorResult{Status: domain.EnrichStatusSucceeded}, nil
			}}
			if scenario == "match" {
				first = matchFailureProcessor{first.(*testProcessor)}
			}
			continued := false
			last := &testProcessor{name: "last", fn: func(context.Context, *Scope) (ProcessorResult, error) {
				continued = true
				return ProcessorResult{Status: domain.EnrichStatusSucceeded}, nil
			}}
			chain, err := NewChain([]Processor{first, last}, Sources{}, WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(ctx, Input{Event: testAlert()})
			if scenario == "cancel" {
				if !errors.Is(err, context.Canceled) || continued || output.Len() != 0 {
					t.Fatalf("cancellation: %v %t %s", err, continued, output.String())
				}
				return
			}
			if err != nil || !continued {
				t.Fatalf("chain interrupted: %v", err)
			}
			if scenario == "success" || scenario == "optional" {
				if output.Len() != 0 {
					t.Fatal("success logged")
				}
				return
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["module"] != "enrich" || record["processor"] != "resource" || record["event_id"] != testAlert().EventID || record["bk_tenant_id"] != testAlert().BKTenantID {
				t.Fatal(record)
			}
			cause := fmt.Sprint(record["error"])
			want := map[string]string{"match": "match dependency timeout", "process": "resource lookup timeout", "partial": "strategy_publication: strategy config reader is unavailable", "panic": "logging_test.go", "invalid": "invalid processor status"}[scenario]
			if !strings.Contains(cause, want) || strings.Contains(output.String(), "private alert body") {
				t.Fatalf("cause=%s", cause)
			}
			encoded, _ := json.Marshal(result.Data)
			if strings.Contains(string(encoded), "timeout") || strings.Contains(string(encoded), "reader is unavailable") {
				t.Fatal("runtime errors persisted in enrich payload")
			}
		})
	}
}

func TestConcurrentProcessorLogsKeepEventIdentity(t *testing.T) {
	var output bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &output)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := NewChain([]Processor{&testProcessor{name: "resource", fn: func(ctx context.Context, s *Scope) (ProcessorResult, error) {
		_, _, _ = s.CWStrategy(ctx)
		return ProcessorResult{Status: domain.EnrichStatusPartial}, nil
	}}}, Sources{}, WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			event := testAlert()
			event.EventID = fmt.Sprint(i)
			event.BKTenantID = "tenant-" + event.EventID
			if _, err := chain.Enrich(t.Context(), Input{Event: event}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprint(record["event_id"])
		if record["bk_tenant_id"] != "tenant-"+id {
			t.Fatal(record)
		}
		seen[id] = true
	}
	if len(seen) != 32 {
		t.Fatal(len(seen))
	}
}
