package taskdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestRepairHoldSurvivesReplanningAndNeedsExactResume(t *testing.T) {
	state, release, now := fixture()
	Reconcile(&state, []eventsource.Release{release}, now)
	id := taskID(release.ID, "lifecycle", 0)
	failed := state.Tasks[id]
	failed.Phase, failed.Blocked = "stopped", true
	failed.Retired = []string{ConsumerName(failed)}
	state.Tasks[id] = failed
	Reconcile(&state, []eventsource.Release{release}, now)
	if count(state, "cleaner") != 3 {
		t.Fatal("repair hold affected another role")
	}
	for _, task := range state.Tasks {
		if task.Role == "lifecycle" && task.ID != id && task.Phase != "stopping" {
			t.Fatal("another lifecycle replica may reclaim the blocked message")
		}
	}
	if err := resumeTask(&state, release.ID, id, failed.Version, failed.Epoch); !errors.Is(err, eventsource.ErrConflict) {
		t.Fatal("resumed before source role stop handshake")
	}
	for key, task := range state.Tasks {
		if task.Role == "lifecycle" {
			task.Phase = "stopped"
			state.Tasks[key] = task
		}
	}
	// 序列化模拟中心重启；扩容、时间推进及新配置不得绕过修复要求。
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		future := now.Add(time.Duration(i+1) * time.Hour)
		for key, worker := range state.Workers {
			worker.Seen = future
			state.Workers[key] = worker
		}
		candidate := release
		candidate.Version++
		Reconcile(&state, []eventsource.Release{candidate}, future)
		if count(state, "lifecycle") != 0 || state.Tasks[id].Epoch != failed.Epoch {
			t.Fatal("permanent failure retried automatically")
		}
	}
	for _, test := range []struct {
		source         string
		version, epoch int64
		want           error
	}{
		{"other-source", failed.Version, failed.Epoch, eventsource.ErrNotFound},
		{release.ID, failed.Version + 1, failed.Epoch, eventsource.ErrConflict},
		{release.ID, failed.Version, failed.Epoch - 1, eventsource.ErrConflict},
	} {
		if err := resumeTask(&state, test.source, id, test.version, test.epoch); !errors.Is(err, test.want) {
			t.Fatalf("stale or wrong source resume accepted: %v", err)
		}
	}
	for range 2 {
		if err := resumeTask(&state, release.ID, id, failed.Version, failed.Epoch); err != nil {
			t.Fatal(err)
		}
	}
	future := now.Add(5 * time.Hour)
	for key, worker := range state.Workers {
		worker.Seen = future
		state.Workers[key] = worker
	}
	Reconcile(&state, []eventsource.Release{release}, future)
	replacement := state.Tasks[id]
	if replacement.Blocked || replacement.Epoch <= failed.Epoch || count(state, "lifecycle") == 0 || len(replacement.Retired) == 0 {
		t.Fatal("resume lost new epoch or retired PEL owner")
	}
	if err := resumeTask(&state, release.ID, id, failed.Version, failed.Epoch); !errors.Is(err, eventsource.ErrConflict) {
		t.Fatal("old request affected successor")
	}
}

func TestRepairMarkerPreservesErrorChain(t *testing.T) {
	cause := errors.New("fixed failure")
	marked := WithTaskStage("consume", errors.Join(context.Canceled, RequireTaskRepair(cause)))
	if !requiresTaskRepair(marked) || !errors.Is(marked, cause) || requiresTaskRepair(cause) || RequireTaskRepair(nil) != nil {
		t.Fatal("repair classification or error chain lost")
	}
}

func TestAgentReportsRepairWithoutRestartingSameEpoch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	reported := make(chan Report, 1)
	runtime := workerRuntime(config.Config{}, []string{"lifecycle"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/releases/") {
			writeAgentTestResponse(t, w, eventsource.Release{ID: "source", Version: 1, Spec: config.EventSource{EventSourceID: "source"}})
			return
		}
		var heartbeat Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
			t.Error(err)
			return
		}
		for _, report := range heartbeat.Reports {
			if report.Phase == "stopped" {
				select {
				case reported <- report:
				default:
				}
			}
		}
		writeAgentTestResponse(t, w, []Task{{ID: "source:lifecycle:0", Source: "source", Role: "lifecycle", Epoch: 1, Version: 1, Phase: "starting", RemainingMillis: 60000, Concurrency: runtime.Lifecycle.Concurrency, InflightBytes: runtime.Lifecycle.InflightBytes}})
	}))
	defer server.Close()
	var started atomic.Int64
	agent := Agent{Config: config.DispatchConfig{URL: server.URL}, Runtime: runtime, Roles: []string{"lifecycle"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RunTask: func(context.Context, Task, config.EventSource) error {
		started.Add(1)
		return WithTaskStage("consume", RequireTaskRepair(fmt.Errorf("content missing")))
	}}
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	select {
	case report := <-reported:
		if !report.Blocked || report.Error != "task failed; inspect worker logs" {
			t.Errorf("unsafe or missing repair report: %+v", report)
		}
	case <-ctx.Done():
		t.Error("repair report missing")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("agent failed to stop")
	}
	if started.Load() != 1 {
		t.Fatal("same failed epoch restarted")
	}
}
