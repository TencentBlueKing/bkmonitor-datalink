// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/kmstest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
)

const (
	postConfigFileEnv = "UQ_TEST_POSTCONFIG_FILE"
	postConfigDenyEnv = "UQ_TEST_POSTCONFIG_DENIED"
)

// A new process is intentional: BkAPI and BkDataAPI cache startup credentials.
// Resetting Viper alone would not exercise the restart contract.
func TestPostConfigurationProcess(t *testing.T) {
	path := os.Getenv(postConfigFileEnv)
	if path == "" {
		t.Skip("invoked by TestKMSPostConfiguration")
	}
	config.CustomConfigFilePath = path
	require.NoError(t, config.InitConfigWithWriter(io.Discard))
	metadata.InitMetadata()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := redis.NewUniversalClient(redisService.ClientOptions())
	defer client.Close()
	require.NoError(t, client.Ping(ctx).Err(), "core credentials must survive every optional update")

	api := bkapi.GetBkDataAPI()
	query := new(bksql.Client).WithUrl(api.QueryUrl("")).WithHeader(api.Headers(nil)).WithCurl(new(curl.HttpCurl))
	denied := os.Getenv(postConfigDenyEnv) == "true"
	result := query.QuerySync(metadata.InitHashID(ctx), bksql.QuerySyncRequest{SQL: "SELECT value FROM permitted_table"}, nil)
	if denied {
		require.False(t, result.Result)
		require.Contains(t, result.Message, "403")
	} else {
		require.True(t, result.Result, result.Message)
		require.Equal(t, bksql.StatusOK, result.Code)
		data, ok := result.Data.(*bksql.QuerySyncResultData)
		require.True(t, ok)
		require.Equal(t, 1, data.TotalRecords)
		require.Equal(t, json.Number("7"), data.List[0]["value"])
	}
	result = query.QuerySync(metadata.InitHashID(ctx), bksql.QuerySyncRequest{SQL: "SELECT value FROM forbidden_table"}, nil)
	require.False(t, result.Result, "an accepted deployment token must not grant every table")
	require.Contains(t, result.Message, "403")

	if !Enable {
		return
	}
	s := new(Service)
	otlpClient := s.newHTTPClient()
	if OtlpType == "grpc" {
		otlpClient = s.newGrpcClient()
	}
	exporter, err := otlptrace.New(ctx, otlpClient)
	require.NoError(t, err)
	defer func() { require.NoError(t, exporter.Shutdown(ctx)) }()
	span := tracetest.SpanStub{Name: "post-configuration-query", Resource: s.newResource()}.Snapshot()
	err = exporter.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span})
	if denied {
		require.Error(t, err, "schema-valid credentials still need receiver authorization")
	} else {
		require.NoError(t, err)
	}
}

func TestKMSPostConfiguration(t *testing.T) {
	for _, protocol := range []string{"http", "grpc"} {
		for _, kmsEnabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/kms=%t", protocol, kmsEnabled), func(t *testing.T) {
				oldConfig := viper.GetViper()
				viper.Reset()
				oldPath := config.CustomConfigFilePath
				oldHost, oldPort, oldToken, oldName, oldType, oldEnabled := otlpHost, otlpPort, otlpToken, ServiceName, OtlpType, Enable
				t.Cleanup(func() {
					*viper.GetViper() = *oldConfig
					redisService.LoadConfig()
					config.CustomConfigFilePath = oldPath
					otlpHost, otlpPort, otlpToken, ServiceName, OtlpType, Enable = oldHost, oldPort, oldToken, oldName, oldType, oldEnabled
				})
				if !kmsEnabled {
					// Legacy exporters obtain headers from their existing environment entry.
					t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=trace-header")
				}
				mr := miniredis.RunT(t)
				mr.RequireAuth("core-redis-secret")
				host, port, traces := newTraceReceiver(t, protocol, func(got receivedTrace) error {
					if got.Header == "trace-header" && traceResource(got)["bk.data.token"] == "trace-first" {
						return nil
					}
					return status.Error(codes.PermissionDenied, "permission denied")
				})
				type receivedQuery struct {
					Path           string
					Auth, DataAuth map[string]string
					Body           bksql.Params
				}
				queries := make(chan receivedQuery, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got := receivedQuery{Path: r.URL.Path}
					if json.NewDecoder(r.Body).Decode(&got.Body) != nil ||
						json.Unmarshal([]byte(r.Header.Get(bkapi.BkAPIAuthorization)), &got.Auth) != nil ||
						json.Unmarshal([]byte(r.Header.Get(bkapi.BkDataAuthorization)), &got.DataAuth) != nil {
						http.Error(w, "invalid query", http.StatusBadRequest)
						return
					}
					select {
					case queries <- got:
					case <-r.Context().Done():
						return
					}
					authorized := got.Auth[bkapi.BkAppCodeKey] == "test-app" && got.Auth[bkapi.BkSecretKey] == "core-app-secret"
					if got.Body.BkdataAuthenticationMethod == "token" {
						authorized = authorized && got.Body.BkdataDataToken == "data-second"
					}
					if !authorized || got.Body.SQL == "SELECT value FROM forbidden_table" {
						http.Error(w, "permission denied", http.StatusForbidden)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"result":true,"code":"00","data":{"totalRecords":1,"list":[{"value":7}]}}`)
				}))
				t.Cleanup(server.Close)
				config.CustomConfigFilePath = filepath.Join(t.TempDir(), "config.yaml")
				var envelope, key string
				replacePayload := func(plaintext string) {
					t.Helper()
					nextEnvelope, nextKey := kmstest.Files(t, plaintext)
					if envelope == "" {
						envelope, key = nextEnvelope, nextKey
						return
					}
					// Replace the same mounted paths; reload must keep the old snapshot.
					for destination, source := range map[string]string{envelope: nextEnvelope, key: nextKey} {
						contents, err := os.ReadFile(source)
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(destination, contents, 0600))
					}
				}
				writePayload := func(traceToken, dataToken string) {
					t.Helper()
					if !kmsEnabled {
						return
					}
					payload := map[string]any{
						"redis":           map[string]any{"default": map[string]string{"password": "core-redis-secret"}},
						"bkapp_id_secret": map[string]any{"default": map[string]string{"app_code": "test-app", "app_secret": "core-app-secret"}},
					}
					if traceToken != "" {
						payload["telemetry"] = map[string]any{"default": map[string]any{"token": traceToken, "headers": map[string]string{"authorization": "trace-header"}}}
					}
					if dataToken != "" {
						payload["bkdata"] = map[string]any{"default": map[string]string{"token": dataToken}}
					}
					data, err := json.Marshal(payload)
					require.NoError(t, err)
					replacePayload(string(data))
				}
				writeConfig := func(traceToken, dataToken, marker, plaintext string) {
					t.Helper()
					method := "user"
					if dataToken != "" {
						method = "token"
					}
					kms := "kms:\n  enabled: false\n"
					redisPassword, appSecret, configuredTraceToken := "core-redis-secret", "core-app-secret", traceToken
					if kmsEnabled {
						kms = fmt.Sprintf("kms:\n  enabled: true\n  envelope_file: %q\n  private_key_file: %q\n", envelope, key)
						redisPassword, appSecret, configuredTraceToken = "", "", ""
						dataToken = plaintext
					}
					data := fmt.Sprintf("%sredis:\n  host: %s\n  port: %s\n  password: %q\nbk_api:\n  address: %s\n  code: test-app\n  secret: %q\nbk_data:\n  address: %s\n  authentication_method: %s\n  token: %q\ntrace:\n  enable: %t\n  service_name: test-uq\n  otlp:\n    type: %s\n    host: %s\n    port: %s\n    token: %q\nquery:\n  marker: %s\n", kms, mr.Host(), mr.Port(), redisPassword, server.URL, appSecret, server.URL, method, dataToken, traceToken != "", protocol, host, port, configuredTraceToken, marker)
					require.NoError(t, os.WriteFile(config.CustomConfigFilePath, []byte(data), 0600))
				}
				restart := func() {
					t.Helper()
					viper.Reset()
					require.NoError(t, config.InitConfigWithWriter(io.Discard))
					require.Equal(t, "core-redis-secret", redisService.ClientOptions().Password)
				}
				consume := func(traceToken, dataToken string, denied bool) {
					t.Helper()
					binary, err := os.Executable()
					require.NoError(t, err)
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, "-test.run=^TestPostConfigurationProcess$", "-test.count=1")
					cmd.Env = append(os.Environ(), postConfigFileEnv+"="+config.CustomConfigFilePath, fmt.Sprintf("%s=%t", postConfigDenyEnv, denied))
					output, err := cmd.CombinedOutput()
					require.NoError(t, err, "%s", output)
					for range 2 {
						select {
						case got := <-queries:
							require.Equal(t, "/query_sync/", got.Path)
							require.Equal(t, "core-app-secret", got.Auth[bkapi.BkSecretKey])
							require.Equal(t, "test-app", got.Body.BkAppCode)
							require.Equal(t, viper.GetString("bk_data.authentication_method"), got.Body.BkdataAuthenticationMethod)
							require.Equal(t, dataToken, got.Body.BkdataDataToken)
							require.Equal(t, dataToken, got.DataAuth[bkapi.BkDataDataTokenKey])
							require.Equal(t, got.Body.BkdataAuthenticationMethod, got.DataAuth[bkapi.BkDataAuthenticationMethodKey])
						case <-ctx.Done():
							t.Fatal("BKData query did not reach the receiver")
						}
					}
					if traceToken != "" {
						select {
						case got := <-traces:
							require.Equal(t, "trace-header", got.Header)
							require.Equal(t, traceToken, traceResource(got)["bk.data.token"])
							require.Equal(t, "test-uq", traceResource(got)["service.name"])
							require.Equal(t, "post-configuration-query", got.Request.ResourceSpans[0].ScopeSpans[0].Spans[0].Name)
						case <-ctx.Done():
							t.Fatal("trace did not reach the receiver")
						}
					} else {
						require.Empty(t, traces, "trace is explicitly disabled on first installation")
					}
				}

				writePayload("", "")
				writeConfig("", "", "initial", "")
				restart()
				require.False(t, Enable)
				consume("", "", false)

				// First add telemetry, then BKData, keeping the complete core object.
				for _, step := range []struct{ dataToken, marker, missing string }{
					{"", "telemetry", "telemetry.default"},
					{"data-second", "bkdata", "bkdata.default.token"},
				} {
					writePayload("trace-first", step.dataToken)
					if kmsEnabled {
						// An accidental restart before the config rollout must also work.
						previousTraceToken := ""
						if Enable {
							previousTraceToken = "trace-first"
						}
						consume(previousTraceToken, step.dataToken, false)
					}
					writeConfig("trace-first", step.dataToken, step.marker, "")
					before := viper.AllSettings()
					err := config.ReloadConfigWithWriter(io.Discard)
					if kmsEnabled {
						require.ErrorContains(t, err, step.missing)
						require.Equal(t, before, viper.AllSettings(), "SIGUSR1 must reject a candidate needing a group absent at startup")
					} else {
						require.NoError(t, err)
					}
					restart()
					consume("trace-first", step.dataToken, false)
				}

				// Revert only the second addition; the first token and core still work.
				writePayload("trace-first", "")
				writeConfig("trace-first", "", "revert-bkdata", "")
				require.NoError(t, config.ReloadConfigWithWriter(io.Discard))
				if kmsEnabled {
					require.Equal(t, "data-second", viper.GetString("bk_data.token"), "even removal waits for restart")
				}
				restart()
				consume("trace-first", "", false)

				if kmsEnabled {
					before := viper.AllSettings()
					writePayload("trace-first", "data-second")
					writeConfig("trace-first", "data-second", "plaintext-rejected", "forbidden-plaintext")
					require.ErrorContains(t, config.ReloadConfigWithWriter(io.Discard), "plaintext credential")
					require.Equal(t, before, viper.AllSettings())
					require.ErrorContains(t, config.InitConfigWithWriter(io.Discard), "plaintext credential")
					require.Equal(t, before, viper.AllSettings())

					// Replacing an aggregate with only the new leaf cannot drop core auth.
					replacePayload(`{"bkdata":{"default":{"token":"data-second"}}}`)
					writeConfig("trace-first", "data-second", "incomplete-rejected", "")
					require.ErrorContains(t, config.InitConfigWithWriter(io.Discard), "redis.default.password")
					require.Equal(t, before, viper.AllSettings())

					// Valid schema does not confer remote authorization for either protocol.
					writePayload("unapproved-trace-token", "unapproved-data-token")
					writeConfig("unapproved-trace-token", "unapproved-data-token", "remote-rejected", "")
					restart()
					consume("unapproved-trace-token", "unapproved-data-token", true)
				}
			})
		}
	}
}

func traceResource(got receivedTrace) map[string]string {
	attributes := make(map[string]string)
	for _, resource := range got.Request.ResourceSpans {
		for _, attr := range resource.Resource.Attributes {
			attributes[attr.Key] = attr.Value.GetStringValue()
		}
	}
	return attributes
}
