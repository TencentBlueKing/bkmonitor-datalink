package viewstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// A Leader that cannot publish is told from one with nothing new to publish:
// the failures run is dated, counted and named until a publication succeeds.
// A Leader whose Workers are expected and none holds a stream is dated too.
func TestAFailingPublisherAndAViewNobodyReceivesAreDated(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	start := time.UnixMilli(harness.clock.Load())
	harness.server.NotePublishFailure(viewstream.PublishFailureActivationUnreadable)
	harness.clock.Add((30 * time.Second).Milliseconds())
	harness.server.NotePublishFailure(viewstream.PublishFailureActiveSetUnreadable)
	stats := harness.server.Stats()
	if !stats.PublishFailingSince.Equal(start) || stats.PublishFailures != 2 || stats.PublishFailureReason != viewstream.PublishFailureActiveSetUnreadable ||
		stats.PublishFailuresByReason[viewstream.PublishFailureActivationUnreadable] != 1 || len(stats.PublishFailuresByReason) != len(viewstream.PublishFailureReasons) {
		t.Fatalf("failing stats = %+v", stats)
	}
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	if _, err := harness.server.Publish(ctx, desired); err != nil {
		t.Fatal(err)
	}
	stats = harness.server.Stats()
	if !stats.PublishFailingSince.IsZero() || stats.PublishFailures != 0 || stats.PublishFailureReason != "" ||
		stats.PublishFailuresByReason[viewstream.PublishFailureActiveSetUnreadable] != 1 {
		t.Fatalf("after a publication the run is over but the total stays: %+v", stats)
	}
	// w1 is expected and holds no stream.
	if stats.Counts.Expected != 1 || stats.Sessions != 0 || !stats.NoSessionsSince.Equal(time.UnixMilli(harness.clock.Load())) {
		t.Fatalf("no-session stats = expected %d sessions %d since %v", stats.Counts.Expected, stats.Sessions, stats.NoSessionsSince)
	}
	first := stats.NoSessionsSince
	harness.clock.Add((10 * time.Second).Milliseconds())
	if again := harness.server.Stats(); !again.NoSessionsSince.Equal(first) {
		t.Fatalf("the no-session start moved: %v, want %v", again.NoSessionsSince, first)
	}
	harness.server.StepDown()
	if after := harness.server.Stats(); !after.NoSessionsSince.IsZero() {
		t.Fatalf("a follower carries a no-session start: %v", after.NoSessionsSince)
	}
}
