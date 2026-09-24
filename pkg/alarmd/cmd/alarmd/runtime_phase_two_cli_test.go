package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCLIDisabledLeavesNativeAPIUnchanged(t *testing.T) {
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(218) })
	h, closeCLI, _ := buildPhaseTwoCLI(config.Default(), native, nil, nil, nil, func() *observability.RuntimeConfigFacts { return nil }, cliControlBinding{Incarnation: "test-process"})
	defer closeCLI()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	if w.Code != 218 {
		t.Fatal("native API changed")
	}
}

func TestCLIInvalidConfigurationOnlyDisablesCLIRoutes(t *testing.T) {
	cfg := config.Default()
	cfg.CLI.Enabled = true
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(218) })
	h, closeCLI, _ := buildPhaseTwoCLI(cfg, native, nil, nil, nil, func() *observability.RuntimeConfigFacts { return nil }, cliControlBinding{Incarnation: "test-process"})
	defer closeCLI()
	for _, path := range []string{"/api/health", "/api/cli/channel", "/api/cli/auth/grants"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		want := 503
		if path == "/api/health" {
			want = 218
		}
		if w.Code != want {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestCLIConstructionNeedsNoRedisAvailabilityAndNoAnonymousGrant(t *testing.T) {
	cfg := config.Default()
	cfg.CLI = config.CLIConfig{Enabled: true, EnvironmentID: "test", EnvironmentName: "Test", PublicBaseURL: "https://ob.example/alarmd/", AdminKey: strings.Repeat("x", 32)}
	h, closeCLI, _ := buildPhaseTwoCLI(cfg, http.NotFoundHandler(), nil, nil, nil, func() *observability.RuntimeConfigFacts { return nil }, cliControlBinding{Incarnation: "test-process"})
	defer closeCLI()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/cli/auth/grants", nil))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "admin_unauthorized") {
		t.Fatalf("CLI construction or administrator isolation failed: %d %s", w.Code, w.Body.String())
	}
}

func TestCLIRuntimeFactsUseAppliedProfileAndObservationOnly(t *testing.T) {
	cache, err := platformsettings.New(platformsettings.Options{})
	if err != nil {
		t.Fatal(err)
	}
	facts := &observability.RuntimeConfigFacts{Digest: "startup-profile", GOMAXPROCS: 4}
	op := cliRuntimeOperation(func() *observability.RuntimeConfigFacts { return facts }, cache)
	out := op.Run(context.Background(), nil)
	if !out.Complete {
		t.Fatal("runtime facts missing")
	}
	value := out.Value.(cliRuntimeFacts)
	if value.Scope != "answering_replica" || value.Config != facts || value.PlatformSettings == nil {
		t.Fatalf("wrong scope: %+v", value)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "admin_key") || strings.Contains(string(raw), "LastUnavailable") {
		t.Fatal("runtime evidence includes raw config or errors")
	}
}

func TestCLIControlRPCIsBoundOnlyWhenCLIConfigured(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config.Default()
		cfg.CLI = config.CLIConfig{Enabled: enabled, EnvironmentID: "test", EnvironmentName: "Test", PublicBaseURL: "http://ob.example/alarmd/", AdminKey: strings.Repeat("x", 32)}
		cfg.PhaseTwo.Worker.ID = "test-worker"
		server, err := viewstream.NewServer(viewStreamAdmission{}, observability.NopObserver{}, viewstream.ServerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, closeCLI, _ := buildPhaseTwoCLI(cfg, http.NotFoundHandler(), nil, nil, nil, func() *observability.RuntimeConfigFacts { return nil }, cliControlBinding{Server: server, Incarnation: "test-process", StreamToken: "fixture-worker-token"})
		// The environment precondition is checked before any Redis access.
		// Constructing this surface and binding its handler need no live Redis.
		_, err = server.ReadEvidence(context.Background(), &pb.EvidenceRequest{EnvironmentId: "wrong"})
		closeCLI()
		want := codes.Unimplemented
		if enabled {
			want = codes.FailedPrecondition
		}
		if status.Code(err) != want {
			t.Fatalf("enabled=%v: %v", enabled, err)
		}
	}
}
