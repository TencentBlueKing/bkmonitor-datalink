package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// recordedLinkd is the production closer and the production worker port
// (copy and episode writer), built from one configuration as the bundle
// builds them, against the fake link and one runtime Redis.
type recordedLinkd struct {
	route *filterRoute
	port  openAlertCopyPort
}

func newRecordedLinkd(t *testing.T, link *fakeLinkd) recordedLinkd {
	t.Helper()
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	plane := httptest.NewServer(link)
	t.Cleanup(plane.Close)
	var cfg config.Config
	cfg.Redis.StatePrefix = "alarmd-test"
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: plane.URL, JWTSecret: "example-secret"}
	// The strategies' records started two hours ago: long enough for each
	// to vouch for the episodes the test acknowledges now.
	for _, strategy := range []string{"41", "51"} {
		server.Client().HSetNX(context.Background(), cfg.Redis.StatePrefix+":openepisodes:v1:system:"+strategy, openepisodes.SinceField,
			strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10))
	}
	client, err := newLinkdManage(cfg, time.Now)
	if err != nil || client == nil {
		t.Fatalf("link client %v (%v)", client, err)
	}
	closer, err := newFilterCloser(cfg, client, server.Client(), time.Now, nil, nil)
	if err != nil || closer == nil {
		t.Fatalf("closer %v (%v)", closer, err)
	}
	copy, err := newActiveCopy(cfg, client, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := newEpisodeWriter(cfg, client, server.Client(), time.Now, nil)
	if err != nil || writer == nil {
		t.Fatalf("writer %v (%v)", writer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go writer.Run(ctx)
	return recordedLinkd{route: newFilterRoute(cfg, closer), port: openAlertCopyPort{copy: copy, record: writer}}
}

// acknowledge hands the port what the broker acknowledged and waits until
// the record has it.
func (r recordedLinkd) acknowledge(t *testing.T, events ...contract.TriggerEventV1) {
	t.Helper()
	r.port.Acknowledged(events)
	testwait.Until(t, "the episode writer drains", func() bool { return r.port.record.Pending() == 0 })
}

func recordedAbnormal(strategy string, revision int64, n int) contract.TriggerEventV1 {
	return contract.TriggerEventV1{EventKind: contract.TriggerEventAbnormal, TenantID: "system", DedupeMD5: fmt.Sprintf("%032x", n),
		PlanRef:     contract.RuntimePlanRefV1{StrategyID: strategy},
		StrategyRef: &contract.StrategySnapshotRef{TenantID: "system", Revision: revision}}
}

// What a worker acknowledges is the control leader's expectation: the
// absent close of a strategy whose workers acknowledged 9 episodes allows
// 29 and refuses the link's 30; with the tenth acknowledged it closes all
// 30, and a repeated ABNORMAL adds nothing.
func TestAWorkersAcknowledgedEpisodesAreTheLeadersExpectation(t *testing.T) {
	link := newFakeLinkd(t)
	for i := 0; i < 30; i++ {
		link.add(&fakeLinkdAlert{id: fmt.Sprintf("alert-%03d", i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	r := newRecordedLinkd(t, link)
	var events []contract.TriggerEventV1
	for i := 0; i < 9; i++ {
		events = append(events, recordedAbnormal("41", 3, i))
	}
	r.acknowledge(t, events...)
	r.acknowledge(t, events...)
	scope := filterclose.Scope{TenantID: "system", EventSourceID: r.route.source, StrategyID: "41"}
	if word := r.route.closer.Absent(context.Background(), scope, filterclose.DecisionAbsent, 3); word != filterclose.OutcomeExpectExceeded {
		t.Fatalf("word %s, want expect_exceeded", word)
	}
	if len(link.closed()) != 0 {
		t.Fatalf("closed %v past the record", link.closed())
	}
	r.acknowledge(t, recordedAbnormal("41", 3, 9))
	if word := r.route.closer.Absent(context.Background(), scope, filterclose.DecisionAbsent, 3); word != filterclose.OutcomeJobDone {
		t.Fatalf("word %s, want job_done", word)
	}
	if len(link.closed()) != 30 {
		t.Fatalf("closed %d, want 30", len(link.closed()))
	}
	if stats := r.port.record.Stats(); stats[openepisodes.OutcomeOpened] != 10 {
		t.Fatalf("record writes %v, want 10 opened", stats)
	}
}
