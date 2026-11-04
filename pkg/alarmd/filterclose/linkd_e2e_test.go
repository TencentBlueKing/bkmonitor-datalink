// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// linkdModuleEnvironment names the alert link's module (pkg/linkd of
// Linkd at b7e5beddf) the end-to-end test builds the link from,
// and linkdToolchainEnvironment the Go toolchain that module asks for
// (its go.mod's go line; go1.26.10 at b7e5beddf). Without the module the
// test is skipped: it needs the link's source.
const (
	linkdModuleEnvironment    = "ALARMD_LINKD_MODULE"
	linkdToolchainEnvironment = "ALARMD_LINKD_TOOLCHAIN"
)

const harnessSecret = "example-secret"

// linkdHarness is the link's own code in one process (testdata/
// linkd_harness/main.go.txt): its management API's search, filtered close
// and single close handlers with its token check, cleaner, event factory
// and lifecycle processor over its in-memory repository.
type linkdHarness struct {
	t    *testing.T
	base string
	// record is alarmd's episode record each opened event is written to,
	// as a worker writes it after the broker's ACK; set by closerOn.
	record *openepisodes.Store
}

func startLinkdHarness(t *testing.T, flags ...string) *linkdHarness {
	t.Helper()
	module := os.Getenv(linkdModuleEnvironment)
	if module == "" {
		t.Skip(linkdModuleEnvironment + " names no link module to build the link from")
	}
	source, err := filepath.Abs("testdata/linkd_harness/main.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{
		filepath.Join(module, "cmd", "alarmd-filter-harness", "main.go"): source}})
	if err := os.WriteFile(filepath.Join(dir, "overlay.json"), overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "harness")
	build := exec.CommandContext(testwait.Context(t), "go", "build", "-overlay", filepath.Join(dir, "overlay.json"), "-o", binary, "./cmd/alarmd-filter-harness")
	build.Dir = module
	toolchain := os.Getenv(linkdToolchainEnvironment)
	if toolchain == "" {
		toolchain = "local"
	}
	build.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOTOOLCHAIN="+toolchain)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the link: %v\n%s", err, output)
	}
	run := exec.Command(binary, append([]string{"-secret", harnessSecret}, flags...)...)
	stdout, _ := run.StdoutPipe()
	var stderr bytes.Buffer
	run.Stderr = &stderr
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Process.Kill(); _ = run.Wait() })
	listening := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		listening <- strings.TrimSpace(line)
	}()
	select {
	case address := <-listening:
		if address == "" {
			t.Fatalf("the link did not start: %s", stderr.String())
		}
		return &linkdHarness{t: t, base: "http://" + address}
	case <-testwait.Expired(t):
		t.Fatalf("the link did not start: %s", stderr.String())
	}
	return nil
}

// open sends alarmd's own standard event for the decision to the link, as
// its Kafka consumer would hand it on, and answers the alert it opened.
func (h *linkdHarness) open(event *contract.TriggerEventV1) string {
	h.t.Helper()
	converter, err := linkdoutput.NewConverter(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	written, err := converter.Convert(event)
	if err != nil {
		h.t.Fatal(err)
	}
	response, err := http.Post(h.base+"/harness/events", "application/json", bytes.NewReader(written.Payload))
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	var opened struct {
		AlertID string `json:"alert_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&opened); err != nil || response.StatusCode != http.StatusOK || opened.AlertID == "" {
		h.t.Fatalf("the link did not open an alert (%d, %v)", response.StatusCode, err)
	}
	if h.record != nil {
		change, ok := openepisodes.ChangeOf(*event, time.Now())
		if !ok {
			h.t.Fatal("the event opens nothing in the record")
		}
		if err := h.record.Apply(context.Background(), []openepisodes.Change{change}, nil); err != nil {
			h.t.Fatal(err)
		}
	}
	return opened.AlertID
}

func (h *linkdHarness) state(alertID string) (string, int) {
	h.t.Helper()
	response, err := http.Get(h.base + "/harness/alert?tenant=tenant-a&id=" + url.QueryEscape(alertID))
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	var state struct {
		Status string `json:"status"`
		Closes int    `json:"closes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		h.t.Fatal(err)
	}
	return state.Status, state.Closes
}

// closerOn is a Closer calling the harness with the link's management
// token, over a real runtime Redis.
func closerOn(t *testing.T, h *linkdHarness) (*Closer, *linkdmanage.Client) {
	t.Helper()
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	client, err := linkdmanage.New(linkdmanage.Options{BaseURL: h.base, Secret: harnessSecret, Username: "alarmd", Client: &http.Client{Timeout: 15 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	commands, _ := NewCommands(server.Client(), "alarmd-e2e")
	pending, _ := NewPending(server.Client(), "alarmd-e2e", MaxPending)
	record, err := openepisodes.NewStore(server.Client(), "alarmd-e2e")
	if err != nil {
		t.Fatal(err)
	}
	h.record = record
	// The record started two hours ago, so it vouches for the episodes the
	// test opens (recordSettle) and the closes check against it.
	server.Client().HSetNX(context.Background(), "alarmd-e2e:openepisodes:v1:tenant-a:41", openepisodes.SinceField,
		strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10))
	closer, err := New(Options{Link: client, Commands: commands, Pending: pending, Record: record})
	if err != nil {
		t.Fatal(err)
	}
	return closer, client
}

// decisionAt is an ABNORMAL of strategy 41 at a revision, with the facts
// its Plans stated, for series n: what alarmd sends the link.
func decisionAt(revision int64, facts *contract.StrategyFactsV2, n int, noData bool) *contract.TriggerEventV1 {
	event := &contract.TriggerEventV1{
		EventID: fmt.Sprintf("%064x", n+1), EventKind: contract.TriggerEventAbnormal, EventSemanticDigest: strings.Repeat("c", 64),
		PrimaryLevelID: 2, TenantID: "tenant-a", BusinessID: "2", SignalType: contract.SignalTypeMetric,
		StrategyRef: &contract.StrategySnapshotRef{TenantID: "tenant-a", BusinessID: 2, StrategyID: 41, Revision: revision},
		PlanRef:     contract.RuntimePlanRefV1{StrategyID: "41"},
		DedupeMD5:   fmt.Sprintf("%032x", n+1), EvaluationTime: 1756684860,
		RecordRef: contract.TriggerRecordRefV1{SourceTime: 1756684800, Dimensions: map[string]json.RawMessage{
			"bk_target_ip": json.RawMessage(`"192.0.2.1"`), "device": json.RawMessage(fmt.Sprintf(`"sd%d"`, n))}},
		Observed: contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`92.5`)}, Unit: "%"},
		LevelResults: []contract.LevelResultV1{{LevelID: 2, Priority: 1, Result: contract.LevelResultAbnormal,
			DecisionWindow: contract.DecisionWindowV1{Trigger: contract.TriggerWindowEvidenceV1{WindowSize: 5, RequiredAnomalies: 2,
				ObservedAnomalies: 3, AnomalyBeginTime: 1756684680}}}},
		StrategyFacts: facts,
	}
	if noData {
		event.RecordRef.Dimensions[contract.NoDataDimensionTag] = json.RawMessage("true")
		event.Observed = contract.TriggerObservedV1{Values: map[string]json.RawMessage{contract.NoDataPeriodFactField: json.RawMessage("5")}}
	}
	return event
}

// The filter close against the link's own handlers at b7e5beddf: alerts
// the link opened from alarmd's own events, an edit whose metric changed,
// and the link's search, dry run, filtered close and token check. The
// older version's alerts of the old facts close - threshold and no-data -
// and those whose facts survived, and the current version's, stay; a
// repeat of the close finds nothing; a close of a closed alert by another
// command is the link's invalid_transition, told from its text.
func TestTheFilterCloseAgainstTheLinksOwnHandlers(t *testing.T) {
	h := startLinkdHarness(t)
	closer, client := closerOn(t, h)
	old, current := factsOf("m4"), factsOf("m5")
	closing := []string{h.open(decisionAt(4, old, 1, false)), h.open(decisionAt(4, old, 2, false)), h.open(decisionAt(4, old, 3, true))}
	kept := []string{h.open(decisionAt(4, current, 4, false)), h.open(decisionAt(5, current, 5, false)), h.open(decisionAt(5, old, 6, false))}
	if word, _ := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()}); word != "" {
		t.Fatalf("word %s, stats %v", word, nonZero(closer.Stats()))
	}
	for _, id := range closing {
		if status, closes := h.state(id); status != "closed" || closes != 1 {
			t.Fatalf("%s: %s, closed %d times (stats %v)", id, status, closes, nonZero(closer.Stats()))
		}
	}
	for _, id := range kept {
		if status, _ := h.state(id); status != "active" {
			t.Fatalf("%s: %s, want active", id, status)
		}
	}
	before := closer.Stats()[OutcomeItemClosed]
	closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()})
	if closer.Stats()[OutcomeItemClosed] != before || closer.Stats()[OutcomeNothingMatched] == 0 {
		t.Fatalf("a repeat closed again: %v", nonZero(closer.Stats()))
	}
	other := mint("another", "tenant-a", "another reason", time.Now())
	_, err := client.CloseOne(context.Background(), closing[0], other.Bytes)
	if linkdmanage.ConflictKind(err) != linkdmanage.ConflictInvalidTransition {
		t.Fatalf("another command on a closed alert: %v (%s)", err, linkdmanage.ConflictKind(err))
	}
}

// Against the link's own handlers with its closer answering every seventh
// close busy and saving every fifth then answering result_uncertain: the
// orphaned alerts close, each once, the busy ones found again by the
// filter and the uncertain ones retried by id through the link's single
// close until it answers already_closed; the kept ones stay active.
func TestTheFilterCloseAgainstTheLinksHandlersUnderFaults(t *testing.T) {
	h := startLinkdHarness(t, "-busy-every", "7", "-uncertain-every", "5")
	closer, _ := closerOn(t, h)
	old, current := factsOf("m4"), factsOf("m5")
	var closing, kept []string
	for n := 0; n < 60; n++ {
		closing = append(closing, h.open(decisionAt(4, old, n, n%10 == 0)))
	}
	for n := 60; n < 70; n++ {
		kept = append(kept, h.open(decisionAt(4, current, n, false)))
	}
	input := EditInput{Scope: testScope, Plan: currentPlan()}
	for round := 0; ; round++ {
		if round == 50 {
			t.Fatalf("not settled: %v", nonZero(closer.Stats()))
		}
		closer.Edit(context.Background(), input)
		closer.RetryPending(context.Background(), 100)
		done := closer.PendingSize(context.Background()) == 0
		for _, id := range closing {
			status, closes := h.state(id)
			if closes > 1 {
				t.Fatalf("%s closed %d times", id, closes)
			}
			done = done && status == "closed"
		}
		if done {
			break
		}
	}
	for _, id := range kept {
		if status, _ := h.state(id); status != "active" {
			t.Fatalf("%s: %s, want active", id, status)
		}
	}
	stats := closer.Stats()
	if stats[OutcomeItemRederived] == 0 || stats[OutcomeItemPending] == 0 || stats[OutcomePendingAlreadyClosed] == 0 {
		t.Fatalf("the faults did not all happen: %v", nonZero(stats))
	}
}

// A per-series close against the link's own handlers: the filter by
// fingerprint, bounded to the alerts opened by the decision's moment
// (range create_at lte), closes the series' alert opened before it and
// leaves the one opened after it, though both carry fingerprints it names.
func TestAPerSeriesCloseAgainstTheLinksOwnHandlers(t *testing.T) {
	h := startLinkdHarness(t)
	closer, client := closerOn(t, h)
	fingerprintOf := func(id string) string {
		query, _ := json.Marshal(map[string]any{"term": map[string]any{"alert_id": id}})
		page, err := client.Search(context.Background(), "tenant-a", query, "", 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].Fingerprint == "" {
			t.Fatalf("search %s: %v %+v", id, err, page)
		}
		return page.Items[0].Fingerprint
	}
	before := h.open(decisionAt(4, factsOf("m4"), 1, false))
	decidedAt := nextMillisecond(t, time.Now())
	nextMillisecond(t, decidedAt)
	after := h.open(decisionAt(4, factsOf("m4"), 2, false))
	word := closer.Fingerprints(context.Background(), testScope, []string{fingerprintOf(before), fingerprintOf(after)}, decisionSuperseded, 4,
		decidedAt)
	if word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	if status, closes := h.state(before); status != "closed" || closes != 1 {
		t.Fatalf("opened before the decision: %s, closed %d", status, closes)
	}
	if status, _ := h.state(after); status != "active" {
		t.Fatalf("opened after the decision: %s, want active", status)
	}
}

// nextMillisecond waits for the clock to reach a millisecond past at's and
// returns that reading: an alert the link opened by at was created before
// it, and one opened after it later, at any create_at precision down to the
// millisecond, on however fast a host.
func nextMillisecond(t *testing.T, at time.Time) time.Time {
	t.Helper()
	var now time.Time
	testwait.Until(t, "the clock past "+at.Format(time.RFC3339Nano)+"'s millisecond", func() bool {
		now = time.Now()
		return now.Truncate(time.Millisecond).After(at.Truncate(time.Millisecond))
	})
	return now
}
