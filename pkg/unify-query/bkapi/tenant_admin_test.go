package bkapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLookupDoesNotHoldCanceledQuery(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		fmt.Fprint(w, `{"data":[{"bk_username":"virtual"}]}`)
	}))
	defer server.Close()
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	oldClient := userAPIClient
	userAPIClient = server.Client()
	t.Cleanup(func() { userAPIClient = oldClient })
	api := &BkDataAPI{bkAPI: &BkAPI{}, adminLogin: "login", userAPIURL: server.URL}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := api.tenantAdmin(ctx, "tenant")
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled caller waited for the shared HTTP lookup")
	}
	unblock()
	username, err := api.tenantAdmin(context.Background(), "tenant")
	require.NoError(t, err)
	require.Equal(t, "virtual", username)
	require.EqualValues(t, 1, calls.Load())
}

func TestConfiguredQueryAuth(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/bk-user/prod/api/v3/open/tenant/virtual-users/-/lookup/", r.URL.Path)
		require.Equal(t, "login_name", r.URL.Query().Get("lookup_field"))
		require.Equal(t, "service+login", r.URL.Query().Get("lookups"))
		require.JSONEq(t, `{"bk_app_code":"app","bk_app_secret":"secret"}`, r.Header.Get(BkAPIAuthorization))
		tenant := r.Header.Get("X-Bk-Tenant-Id")
		if tenant == "missing" {
			fmt.Fprint(w, `{"code":0,"data":[]}`)
			return
		}
		fmt.Fprintf(w, `{"code":0,"data":[{"bk_username":%q}]}`, tenant+"-virtual")
	}))
	defer server.Close()
	oldClient := userAPIClient
	userAPIClient = server.Client()
	t.Cleanup(func() { userAPIClient = oldClient })

	t.Setenv("BKAPP_USER_API_BASE_URL", "")
	t.Setenv("BK_COMPONENT_API_URL", server.URL)
	api := &BkDataAPI{
		bkAPI:      &BkAPI{authConfig: map[string]string{BkAppCodeKey: "app", BkSecretKey: "secret", BkUserNameKey: AdminUserName}},
		authConfig: map[string]string{BkDataAuthenticationMethodKey: "user", BkUserNameKey: AdminUserName},
		adminLogin: "service+login", userAPIURL: userAPIBaseURL(),
	}
	oldMode := viper.GetBool("http.multi_tenant_mode")
	t.Cleanup(func() { viper.Set("http.multi_tenant_mode", oldMode) })
	input := map[string]string{"Content-Type": "application/json"}
	for _, tc := range []struct {
		multi            bool
		tenant, wantUser string
	}{
		{false, "system", "default-virtual"}, {false, "", "default-virtual"},
		{true, "first", "first-virtual"}, {true, "second", "second-virtual"}, {true, "first", "first-virtual"},
	} {
		viper.Set("http.multi_tenant_mode", tc.multi)
		auth, headers, err := QueryAuth(context.Background(), api, tc.tenant, input)
		require.NoError(t, err)
		require.Equal(t, tc.wantUser, auth[BkUserNameKey])
		for _, key := range []string{BkAPIAuthorization, BkDataAuthorization} {
			var decoded map[string]string
			require.NoError(t, json.Unmarshal([]byte(headers[key]), &decoded))
			require.Equal(t, tc.wantUser, decoded[BkUserNameKey])
		}
	}
	require.EqualValues(t, 3, calls.Load())
	require.Len(t, input, 1, "request headers must not mutate shared options")
	for _, tenant := range []string{"", "missing", "missing"} {
		_, _, err := QueryAuth(context.Background(), api, tenant, input)
		require.Error(t, err)
	}
	require.EqualValues(t, 5, calls.Load(), "a missing tenant fails locally, unsuccessful lookups are retried")

	api.authConfig[BkDataAuthenticationMethodKey] = "token"
	api.authConfig[BkDataDataTokenKey] = "test-token"
	auth, _, err := QueryAuth(context.Background(), api, "missing", input)
	require.NoError(t, err)
	require.Equal(t, AdminUserName, auth[BkUserNameKey])
	require.Equal(t, "test-token", auth[BkDataDataTokenKey])
	api.authConfig[BkDataAuthenticationMethodKey], api.adminLogin = "user", ""
	auth, _, err = QueryAuth(context.Background(), api, "missing", input)
	require.NoError(t, err)
	require.Equal(t, AdminUserName, auth[BkUserNameKey])
	require.EqualValues(t, 5, calls.Load())
	t.Setenv("BKAPP_USER_API_BASE_URL", server.URL+"/custom/")
	require.Equal(t, server.URL+"/custom/", userAPIBaseURL())
}
