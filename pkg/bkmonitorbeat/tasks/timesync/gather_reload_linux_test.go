//go:build linux

package timesync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/beevik/ntp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestGatherReloadDuringRunKeepsConfigAndClientConsistent(t *testing.T) {
	ntpdPath := filepath.Join(t.TempDir(), "ntp.conf")
	if err := os.WriteFile(ntpdPath, []byte("server 10.0.0.31 iburst\n"), 0o644); err != nil {
		t.Fatalf("write ntp.conf: %v", err)
	}

	confA := &configs.TimeSyncConfig{
		BaseTaskParam: configs.NewBaseTaskParam(),
		Env:           "test",
		QueryTimeout:  time.Second,
		NtpdPath:      ntpdPath,
		BkNTPSource:   "bk-a",
	}
	confB := &configs.TimeSyncConfig{
		BaseTaskParam: configs.NewBaseTaskParam(),
		Env:           "test",
		QueryTimeout:  2 * time.Second,
		NtpdPath:      ntpdPath,
		BkNTPSource:   "bk-b",
	}
	task := New(nil, confA).(*Gather)

	localQueryStarted := make(chan struct{})
	releaseLocalQuery := make(chan struct{})
	var blockLocalQueryOnce sync.Once
	withQueryNTP(t, func(address string, options ntp.QueryOptions) (*ntp.Response, error) {
		switch address {
		case "10.0.0.31":
			blockLocalQueryOnce.Do(func() {
				close(localQueryStarted)
				<-releaseLocalQuery
			})
			return &ntp.Response{
				Time:        time.Unix(1700000000, 0),
				ClockOffset: -time.Second,
				Stratum:     1,
			}, nil
		case "bk-a":
			return &ntp.Response{ClockOffset: -time.Second, Stratum: 1}, nil
		case "bk-b":
			return &ntp.Response{ClockOffset: -2 * time.Second, Stratum: 1}, nil
		default:
			return nil, fmt.Errorf("unexpected ntp address %q", address)
		}
	})

	events := make(chan define.Event, 8)
	done := make(chan struct{})
	go func() {
		task.Run(context.Background(), events)
		close(done)
	}()

	select {
	case <-localQueryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("local ntp query did not start")
	}

	// Reload while the current Run is still querying the local source.
	task.SetConfig(confB)
	task.Reload()
	close(releaseLocalQuery)

	firstRunEvents := waitAndDrainEvents(t, done, events, 3)
	assertBatchEvent(t, firstRunEvents, "bk-a", 1)
	if batchEventWithSource(firstRunEvents, "bk-b") != nil {
		t.Fatal("current run used the reloaded blueking source")
	}

	secondDone := make(chan struct{})
	go func() {
		task.Run(context.Background(), events)
		close(secondDone)
	}()
	secondRunEvents := waitAndDrainEvents(t, secondDone, events, 3)
	assertBatchEvent(t, secondRunEvents, "bk-b", 2)
	if batchEventWithSource(secondRunEvents, "bk-a") != nil {
		t.Fatal("next run kept the old blueking source")
	}
}

func waitAndDrainEvents(
	t *testing.T,
	done <-chan struct{},
	events <-chan define.Event,
	want int,
) []define.Event {
	t.Helper()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timesync run did not finish")
	}

	result := make([]define.Event, 0, want)
	for len(result) < want {
		select {
		case event := <-events:
			result = append(result, event)
		case <-time.After(time.Second):
			t.Fatalf("got %d events, want %d", len(result), want)
		}
	}
	return result
}

func batchEventWithSource(events []define.Event, source string) *BatchEvent {
	for _, event := range events {
		batch, ok := event.(*BatchEvent)
		if !ok || len(batch.Data) == 0 {
			continue
		}
		if batch.Data[0].Dimension["time_source"] == source {
			return batch
		}
	}
	return nil
}

func assertBatchEvent(t *testing.T, events []define.Event, source string, offset float64) {
	t.Helper()

	batch := batchEventWithSource(events, source)
	if batch == nil {
		t.Fatalf("missing batch event for source %q", source)
	}
	if got := batch.Data[0].Metrics["bk_offset"]; got != offset {
		t.Fatalf("bk_offset for source %q = %v, want %v", source, got, offset)
	}
}
