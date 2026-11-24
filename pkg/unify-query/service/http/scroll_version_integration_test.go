package http

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

// BKMONITOR_TEST_SCROLL_BASELINE_BINARY 由 testdata/scroll-version-server/main.go
// 在已发布版本模块内构建。两个版本访问同一个 Redis 和 ES，输出文件在续页时保持打开。
func testScrollVersionHandoff(t *testing.T, ctx context.Context, query *structured.QueryTs, clusters []scrollCleanupCluster, expected []string, initialQueries *atomic.Int64) {
	baselineBinary := os.Getenv("BKMONITOR_TEST_SCROLL_BASELINE_BINARY")
	if baselineBinary == "" {
		t.Skip("设置已发布版本 HTTP handler 程序以运行升级和回滚测试")
	}
	oldWindow, oldLock := ScrollWindowTimeout, ScrollSessionLockTimeout
	ScrollWindowTimeout, ScrollSessionLockTimeout = "5m", "10s"
	t.Cleanup(func() { ScrollWindowTimeout, ScrollSessionLockTimeout = oldWindow, oldLock })
	query.Limit, query.SliceMax = 1, 0
	body, err := json.Marshal(query)
	require.NoError(t, err)

	if initialQueries == nil {
		initialQueries = new(atomic.Int64)
	}
	storages := make(map[string]*tsdb.Storage)
	proxyURL := func(address string) string {
		backend, parseErr := url.Parse(address)
		require.NoError(t, parseErr)
		proxy := httputil.NewSingleHostReverseProxy(backend)
		director := proxy.Director
		proxy.Director = func(request *http.Request) {
			if strings.HasSuffix(strings.TrimSuffix(request.URL.Path, "/"), "/_search") {
				initialQueries.Add(1)
			}
			director(request)
		}
		server := httptest.NewServer(proxy)
		t.Cleanup(server.Close)
		return server.URL
	}
	for _, cluster := range clusters {
		original := &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: cluster.address, Username: cluster.username, Password: cluster.password}
		storage := *original
		storage.Address = proxyURL(cluster.address)
		storages[cluster.storageID] = &storage
		tsdb.SetStorage(cluster.storageID, &storage)
		t.Cleanup(func() { tsdb.SetStorage(cluster.storageID, original) })
	}
	config := make(map[string]any)
	for _, key := range []string{bkapi.BkDataAddressConfigPath, bkapi.BkDataTokenConfigPath, bkapi.BkDataClusterSpaceUidConfigPath} {
		config[key] = viper.Get(key)
	}
	input, err := json.Marshal(map[string]any{"RedisAddr": os.Getenv("BKMONITOR_TEST_REDIS_ADDR"), "Storages": storages, "Config": config})
	require.NoError(t, err)
	command := exec.CommandContext(ctx, baselineBinary)
	command.Stdin, command.Stderr = bytes.NewReader(input), os.Stderr
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	address, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	baselineURL := "http://" + strings.TrimSpace(address)
	current := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		queryCtx := metadata.InitHashID(request.Context())
		metadata.SetUser(queryCtx, &metadata.User{Key: request.Header.Get("Bk-Query-Source"), SpaceUID: query.SpaceUid})
		ginContext, _ := gin.CreateTestContext(response)
		ginContext.Request = request.WithContext(queryCtx)
		HandlerQueryRawWithScroll(ginContext)
	}))
	t.Cleanup(current.Close)
	client := &http.Client{Timeout: 15 * time.Second}
	for _, scenario := range []struct {
		name        string
		firstOld    bool
		nextOld     bool
		alternating bool
	}{
		{name: "old_to_old", firstOld: true, nextOld: true},
		{name: "new_to_new"},
		{name: "upgrade", firstOld: true},
		{name: "rollback", nextOld: true},
		{name: "old_new_alternating", firstOld: true, alternating: true},
		{name: "new_old_alternating", nextOld: true, alternating: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			name := fmt.Sprintf("scroll-handoff-%d", time.Now().UnixNano())
			identity := name + ":" + string(body)
			t.Cleanup(func() {
				// 旧版结束时没有 Clear Scroll；测试仅回收这次下载保存的 ID。
				var session redisUtil.ScrollSession
				data := redisUtil.Client().Get(ctx, redisUtil.SessionKeyPrefix+identity).Val()
				require.NoError(t, session.UnmarshalBinary([]byte(data)))
				for _, slice := range session.Slices() {
					for _, cluster := range clusters {
						_, _ = cluster.client.ClearScroll().ScrollId(slice.ScrollID).Do(ctx)
					}
				}
				require.NoError(t, redisUtil.Client().Del(ctx, redisUtil.SessionKeyPrefix+identity).Err())
			})
			file, err := os.Create(filepath.Join(t.TempDir(), "rows.jsonl"))
			require.NoError(t, err)
			defer file.Close()
			encoder := json.NewEncoder(file)
			before := initialQueries.Load()
			for page := 0; ; page++ {
				old := scenario.nextOld
				if page == 0 || scenario.alternating && page%2 == 0 {
					old = scenario.firstOld
				}
				endpoint := current.URL
				if old {
					endpoint = baselineURL
				}
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/query/ts/raw_with_scroll/", bytes.NewReader(body))
				require.NoError(t, err)
				request.Header.Set("Bk-Query-Source", "backend:"+name)
				request.Header.Set(metadata.SpaceUIDHeader, query.SpaceUid)
				response, err := client.Do(request)
				require.NoError(t, err)
				var result ListData
				data, err := io.ReadAll(response.Body)
				require.NoError(t, response.Body.Close())
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, response.StatusCode, string(data))
				require.NoError(t, json.Unmarshal(data, &result))
				for _, row := range result.List {
					require.NoError(t, encoder.Encode(row))
				}
				if result.Done {
					break
				}
				require.Less(t, page, 40)
			}
			_, err = file.Seek(0, io.SeekStart)
			require.NoError(t, err)
			decoder := json.NewDecoder(file)
			var ids []string
			for {
				var row map[string]any
				err := decoder.Decode(&row)
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				ids = append(ids, row["event_id"].(string))
			}
			require.Equal(t, expected, ids)
			require.EqualValues(t, 1, initialQueries.Load()-before, "续页必须复用原游标")
		})
	}
}

func testScrollPageSaveFailure(t *testing.T, ctx context.Context, query *structured.QueryTs, cluster scrollCleanupCluster, expected []string) {
	for _, scenario := range []struct {
		name       string
		queryError bool
	}{
		{name: "expired_lease_rejects_page"},
		{name: "expired_lease_preserves_query_error", queryError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if scenario.queryError && cluster.username == "" {
				t.Skip("ES 开启认证后验证原始查询错误")
			}
			oldWindow, oldLock := ScrollWindowTimeout, ScrollSessionLockTimeout
			ScrollWindowTimeout, ScrollSessionLockTimeout = "5m", "1s"
			t.Cleanup(func() { ScrollWindowTimeout, ScrollSessionLockTimeout = oldWindow, oldLock })
			query.Limit, query.SliceMax = 1, 0
			body, err := json.Marshal(query)
			require.NoError(t, err)
			name := fmt.Sprintf("scroll-save-failure-%d", time.Now().UnixNano())
			identity := name + ":" + string(body)
			queryCtx := func() context.Context {
				requestCtx := metadata.InitHashID(ctx)
				metadata.SetUser(requestCtx, &metadata.User{Key: "backend:" + name, SpaceUID: query.SpaceUid})
				return requestCtx
			}
			session, err := redisUtil.GetOrCreateScrollSession(queryCtx(), identity, "5m", "1s", 0, 1)
			require.NoError(t, err)
			if scenario.queryError {
				// 到达最后一次查询重试时，同时发生保存失败，仍应返回原 ES 查询错误。
				session.Slice("trace.cleanup_0|" + cluster.storageID + "|0").FailedNum = redisUtil.DefaultSliceMaxFailedNum - 1
			}
			require.NoError(t, session.Stop(queryCtx(), nil))
			t.Cleanup(func() { require.NoError(t, redisUtil.Client().Del(ctx, session.SessionKey).Err()) })
			saved := redisUtil.Client().Get(ctx, session.SessionKey).Val()
			backend, err := url.Parse(cluster.address)
			require.NoError(t, err)
			proxy := httputil.NewSingleHostReverseProxy(backend)
			director := proxy.Director
			var delayed atomic.Bool
			proxy.Director = func(request *http.Request) {
				if (scenario.queryError || strings.HasSuffix(strings.TrimSuffix(request.URL.Path, "/"), "/_search")) && delayed.CompareAndSwap(false, true) {
					time.Sleep(1250 * time.Millisecond)
				}
				request.Header.Set("Accept-Encoding", "identity")
				director(request)
			}
			scrollIDs := make(chan string, 32)
			proxy.ModifyResponse = func(response *http.Response) error {
				data, readErr := io.ReadAll(response.Body)
				if readErr != nil {
					return readErr
				}
				_ = response.Body.Close()
				response.Body = io.NopCloser(bytes.NewReader(data))
				var result struct {
					ScrollID string `json:"_scroll_id"`
				}
				if err := json.Unmarshal(data, &result); err != nil {
					return err
				}
				if result.ScrollID != "" {
					scrollIDs <- result.ScrollID
				}
				return nil
			}
			server := httptest.NewServer(proxy)
			t.Cleanup(server.Close)
			storage := &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: server.URL, Username: cluster.username, Password: cluster.password}
			if scenario.queryError {
				storage.Username = "invalid-scroll-test-user"
			}
			tsdb.SetStorage(cluster.storageID, storage)
			t.Cleanup(func() {
				tsdb.SetStorage(cluster.storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: cluster.address, Username: cluster.username, Password: cluster.password})
				var ids []string
				for len(scrollIDs) > 0 {
					ids = append(ids, <-scrollIDs)
				}
				if len(ids) > 0 {
					_, _ = cluster.client.ClearScroll().ScrollId(ids...).Do(ctx)
				}
			})
			request := func() *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				ginContext, _ := gin.CreateTestContext(response)
				ginContext.Request = httptest.NewRequest(http.MethodPost, "/query/ts/raw_with_scroll/", bytes.NewReader(body)).WithContext(queryCtx())
				HandlerQueryRawWithScroll(ginContext)
				return response
			}
			response := request()
			if scenario.queryError {
				require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "401")
				require.NotContains(t, response.Body.String(), redisUtil.ErrScrollSessionLockLost.Error())
			} else {
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), redisUtil.ErrScrollSessionLockLost.Error())
			}
			require.Equal(t, saved, redisUtil.Client().Get(ctx, session.SessionKey).Val())
			// BKLog 只追加成功响应。被拒绝的分页不进入文件，恢复后恰好写出全部日志一次。
			file, err := os.Create(filepath.Join(t.TempDir(), "rows.jsonl"))
			require.NoError(t, err)
			defer file.Close()
			encoder := json.NewEncoder(file)
			ScrollSessionLockTimeout = "10s"
			storage.Username = cluster.username
			tsdb.SetStorage(cluster.storageID, storage)
			for page := 0; ; page++ {
				response = request()
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var result ListData
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
				for _, row := range result.List {
					require.NoError(t, encoder.Encode(row))
				}
				if result.Done {
					break
				}
				require.Less(t, page, 10)
			}
			_, err = file.Seek(0, io.SeekStart)
			require.NoError(t, err)
			decoder := json.NewDecoder(file)
			var ids []string
			for {
				var row map[string]any
				err := decoder.Decode(&row)
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				ids = append(ids, row["event_id"].(string))
			}
			require.Equal(t, expected, ids)
		})
	}
}
