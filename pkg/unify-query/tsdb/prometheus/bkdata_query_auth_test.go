package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
)

func TestBKDataQueryBackendIdentity(t *testing.T) {
	// Start with fresh process-wide BKAPI configuration, independently of test order.
	const child = "UQ_TEST_BKDATA_IDENTITY"
	scenario := os.Getenv(child)
	if scenario == "" {
		for _, name := range []string{"configured", "token", "unconfigured", "legacy-multitenant"} {
			t.Run(name, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestBKDataQueryBackendIdentity$")
				cmd.Env = append(os.Environ(), child+"="+name)
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)
			})
		}
		return
	}
	mock.Init()
	httpmock.Deactivate()
	method, login, username := "user", "  service-login  ", "virtual-identifier"
	wantLogin, lookupTenant, queryPath := "service-login", "default", "/query_sync/"
	switch scenario {
	case "token":
		method, username = "token", "admin"
	case "unconfigured":
		login, username = "   ", "admin"
	case "legacy-multitenant":
		login, wantLogin, lookupTenant = "", "bk_admin", "resource-tenant"
		queryPath = "/api/bk-base/prod/v3/queryengine/query_sync/"
	}
	var lookups, sqlRequests, vmRequests, esRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user/api/v3/open/tenant/virtual-users/-/lookup/" || r.URL.Path == "/api/bk-user/prod/api/v3/open/tenant/virtual-users/-/lookup/" {
			lookups.Add(1)
			if r.Header.Get(metadata.TenantIDHeader) != lookupTenant {
				http.Error(w, "unknown tenant", http.StatusNotFound)
				return
			}
			assert.Equal(t, wantLogin, r.URL.Query().Get("lookups"))
			fmt.Fprintf(w, `{"data":[{"bk_username":%q}]}`, username)
			return
		}
		assert.Equal(t, "resource-tenant", r.Header.Get(metadata.TenantIDHeader))
		assert.Equal(t, "ui:request-user", r.Header.Get(metadata.BkQuerySourceHeader))
		for _, key := range []string{bkapi.BkAPIAuthorization, bkapi.BkDataAuthorization} {
			var auth map[string]string
			if assert.NoError(t, json.Unmarshal([]byte(r.Header.Get(key)), &auth)) {
				assert.Equal(t, username, auth[bkapi.BkUserNameKey])
			}
		}
		switch r.URL.Path {
		case queryPath:
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, username, body["bk_username"])
			assert.Equal(t, method, body["bkdata_authentication_method"])
			if method == "token" {
				assert.Equal(t, "original-token", body["bkdata_data_token"])
			}
			if body["prefer_storage"] == "vm" {
				vmRequests.Add(1)
				fmt.Fprint(w, `{"result":true,"code":"00","data":{"list":[{"status":"success","data":{"resultType":"vector","result":[]}}]}}`)
			} else {
				sqlRequests.Add(1)
				fmt.Fprint(w, `{"result":true,"code":"00","data":{"list":[{"Column":"value","Type":"double"}]}}`)
			}
		case queryPath + "/es/_search/scroll/", queryPath + "es/_search/scroll/":
			esRequests.Add(1)
			fmt.Fprint(w, `{"succeeded":true,"num_freed":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BK_ADMIN_USERNAME", login)
	t.Setenv("BKAPP_USER_API_BASE_URL", server.URL+"/user/")
	viper.Set(bkapi.BkDataAddressConfigPath, server.URL)
	viper.Set(bkapi.BkDataAuthenticationMethodConfigPath, method)
	viper.Set(bkapi.BkDataTokenConfigPath, "original-token")
	viper.Set(config.MultiTenantModeConfigPath, false)
	if scenario == "legacy-multitenant" {
		// These are the existing multi-tenant chart inputs; no new environment is required.
		t.Setenv("BKAPP_USER_API_BASE_URL", "")
		t.Setenv("BK_COMPONENT_API_URL", "")
		viper.Set(bkapi.BkAPIAddressConfigPath, server.URL)
		viper.Set(bkapi.BkDataUriPathConfigPath, "api/bk-base/prod/v3/queryengine")
		viper.Set(bkapi.BkDataAddressConfigPath, "")
		viper.Set(config.MultiTenantModeConfigPath, true)
	}
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{Key: "ui:request-user", TenantID: "resource-tenant"})
	metadata.SetExpand(ctx, &metadata.VmExpand{ResultTableList: []string{"sample_rt"}})
	now := time.Now()
	sql := &metadata.Query{StorageType: metadata.BkSqlStorageType, DB: "sample_rt", Measurement: "hdfs"}
	vm := GetTsDbInstance(ctx, &metadata.Query{StorageType: metadata.VictoriaMetricsStorageType})
	es := GetTsDbInstance(ctx, &metadata.Query{StorageType: metadata.ElasticsearchStorageType, SourceType: structured.BkData})
	bkSQL := GetTsDbInstance(ctx, sql)
	require.Zero(t, lookups.Load(), "previews only construct an instance")
	_, err := bkSQL.QueryFieldMap(ctx, sql, now.Add(-time.Minute), now)
	require.NoError(t, err)
	_, err = vm.DirectQuery(ctx, "sample", now)
	require.NoError(t, err)
	require.NoError(t, es.(*elasticsearch.Instance).ClearScroll(ctx, "sample-scroll-id"))
	require.EqualValues(t, 1, sqlRequests.Load())
	require.EqualValues(t, 1, vmRequests.Load())
	require.EqualValues(t, 1, esRequests.Load())
	if scenario == "token" || scenario == "unconfigured" {
		require.Zero(t, lookups.Load())
		return
	}
	require.EqualValues(t, 1, lookups.Load())

	viper.Set(config.MultiTenantModeConfigPath, true)
	metadata.SetUser(ctx, &metadata.User{TenantID: "missing", Key: "ui:request-user"})
	_, err = vm.DirectQuery(ctx, "sample", now)
	require.ErrorContains(t, err, "404")
	require.EqualValues(t, 2, lookups.Load())
	require.EqualValues(t, 1, vmRequests.Load(), "failed lookup must stop the query")
}
