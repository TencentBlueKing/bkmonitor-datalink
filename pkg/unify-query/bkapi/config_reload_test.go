package bkapi

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

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

func TestQueryAuthDuringConfigReload(t *testing.T) {
	// Exercise cold client initialization independently of the other package tests.
	const child = "UQ_TEST_QUERY_AUTH_RELOAD"
	if os.Getenv(child) == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestQueryAuthDuringConfigReload$")
		cmd.Env = append(os.Environ(), child+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"bk_username":%q}]}`, "user-"+r.Header.Get("X-Bk-Tenant-Id"))
	}))
	defer server.Close()
	t.Setenv("BK_ADMIN_USERNAME", "service-login")
	t.Setenv("BKAPP_USER_API_BASE_URL", server.URL)
	config.CustomConfigFilePath = filepath.Join(t.TempDir(), "uq.yaml")
	write := func(multi bool, token string) {
		t.Helper()
		data := fmt.Sprintf("http:\n  multi_tenant_mode: %t\nbk_api:\n  code: app\nbk_data:\n  authentication_method: user\n  token: %q\n", multi, token)
		require.NoError(t, os.WriteFile(config.CustomConfigFilePath, []byte(data), 0600))
	}
	write(false, "")
	require.NoError(t, config.InitConfigWithWriter(io.Discard))
	query := func() (string, error) {
		auth, headers, err := QueryAuth(context.Background(), GetBkDataAPI(), "resource", nil)
		if err != nil {
			return "", err
		}
		for _, key := range []string{BkAPIAuthorization, BkDataAuthorization} {
			var decoded map[string]string
			if err := json.Unmarshal([]byte(headers[key]), &decoded); err != nil {
				return "", err
			}
			if decoded[BkUserNameKey] != auth[BkUserNameKey] {
				return "", fmt.Errorf("inconsistent %s and body", key)
			}
		}
		return auth[BkUserNameKey] + "/" + auth[BkDataDataTokenKey], nil
	}
	stop, done := make(chan struct{}), make(chan error, 1)
	defer func() {
		close(stop)
		require.NoError(t, <-done)
	}()
	go func() {
		for {
			value, err := query()
			if err != nil {
				done <- err
				return
			}
			if value != "user-default/" && value != "user-resource/later" {
				done <- fmt.Errorf("inconsistent query configuration %q", value)
				return
			}
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
		}
	}()
	for n := range 8 {
		multi, token, want := false, "", "user-default/"
		if n%2 == 1 {
			multi, token, want = true, "later", "user-resource/later"
		}
		write(multi, token)
		require.NoError(t, config.ReloadConfigWithWriter(io.Discard))
		value, err := query()
		require.NoError(t, err)
		require.Equal(t, want, value)
	}
	require.NoError(t, os.WriteFile(config.CustomConfigFilePath, []byte("invalid: ["), 0600))
	require.Error(t, config.ReloadConfigWithWriter(io.Discard))
	value, err := query()
	require.NoError(t, err)
	require.Equal(t, "user-resource/later", value)
}
