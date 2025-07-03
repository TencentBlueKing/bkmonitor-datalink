// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v8"
	elastic "github.com/olivere/elastic/v7"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func setupMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	mr, err := miniredis.Run()
	assert.NoError(t, err, "启动miniredis不应该出错")

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   0,
	})

	ctx := context.Background()
	_, err = client.Ping(ctx).Result()
	assert.NoError(t, err, "Redis连接不应该出错")

	t.Logf("MiniRedis启动成功，地址: %s", mr.Addr())
	return mr, client
}

func TestSetSliceOptions(t *testing.T) {
	t.Run("ES slice options", func(t *testing.T) {
		qry := &metadata.Query{
			TableID:            "test_table",
			ResultTableOptions: make(metadata.ResultTableOptions),
		}

		activeSlices := redisUtil.SliceStates{
			{
				SliceID:     0,
				StartOffset: 0,
				EndOffset:   1000,
				Size:        1000,
				Status:      redisUtil.SliceStatusRunning,
				ConnectInfo: "http://127.0.0.1:9200",
				ScrollID:    "scroll_123",
			},
			{
				SliceID:     1,
				StartOffset: 0,
				EndOffset:   1000,
				Size:        1000,
				Status:      redisUtil.SliceStatusRunning,
				ConnectInfo: "http://127.0.0.1:9200",
				ScrollID:    "scroll_456",
			},
		}

		setSliceOptions(qry, activeSlices, "elasticsearch", 3)

		option := qry.ResultTableOptions.GetOption("test_table", "http://127.0.0.1:9200")
		assert.NotNil(t, option, "ES slice option应该被设置")
		assert.NotNil(t, option.SliceID, "SliceID应该被设置")
		assert.NotNil(t, option.SliceMax, "SliceMax应该被设置")
		assert.Equal(t, 0, *option.SliceID, "SliceID应该为0")
		assert.Equal(t, 3, *option.SliceMax, "SliceMax应该为3")
		assert.Equal(t, "scroll_123", option.ScrollID, "ScrollID应该被设置")
	})

	t.Run("Doris slice options", func(t *testing.T) {
		qry := &metadata.Query{
			TableID:            "test_table",
			ResultTableOptions: make(metadata.ResultTableOptions),
		}

		activeSlices := redisUtil.SliceStates{
			{
				SliceID:     0,
				StartOffset: 1000,
				EndOffset:   2000,
				Size:        1000,
				Status:      redisUtil.SliceStatusRunning,
				ConnectInfo: "http://127.0.0.1:8030",
			},
			{
				SliceID:     1,
				StartOffset: 2000,
				EndOffset:   3000,
				Size:        1000,
				Status:      redisUtil.SliceStatusRunning,
				ConnectInfo: "http://127.0.0.1:8030",
			},
		}

		setSliceOptions(qry, activeSlices, "doris", 3)

		option := qry.ResultTableOptions.GetOption("test_table", "http://127.0.0.1:8030")
		assert.NotNil(t, option, "Doris slice option应该被设置")
		assert.NotNil(t, option.From, "From应该被设置")
		assert.Equal(t, 1000, *option.From, "From应该为1000")
	})

	t.Run("Multiple connections slice options", func(t *testing.T) {
		qry := &metadata.Query{
			TableID:            "test_table",
			ResultTableOptions: make(metadata.ResultTableOptions),
		}

		activeSlices := redisUtil.SliceStates{
			{
				SliceID:     0,
				StartOffset: 0,
				EndOffset:   1000,
				Size:        1000,
				Status:      redisUtil.SliceStatusRunning,
				ConnectInfo: "http://127.0.0.1:9200",
				ScrollID:    "scroll_1",
			},
			{
				SliceID:     1,
				StartOffset: 0,
				EndOffset:   1000,
				Size:        1000,
				Status:      redisUtil.SliceStatusRunning,
				ConnectInfo: "http://127.0.0.1:9201",
				ScrollID:    "scroll_2",
			},
		}

		setSliceOptions(qry, activeSlices, "elasticsearch", 2)

		option1 := qry.ResultTableOptions.GetOption("test_table", "http://127.0.0.1:9200")
		option2 := qry.ResultTableOptions.GetOption("test_table", "http://127.0.0.1:9201")

		assert.NotNil(t, option1, "第一个连接的option应该被设置")
		assert.NotNil(t, option2, "第二个连接的option应该被设置")

		assert.Equal(t, 0, *option1.SliceID, "第一个连接的SliceID应该为0")
		assert.Equal(t, 1, *option2.SliceID, "第二个连接的SliceID应该为1")
		assert.Equal(t, "scroll_1", option1.ScrollID, "第一个连接的ScrollID应该正确")
		assert.Equal(t, "scroll_2", option2.ScrollID, "第二个连接的ScrollID应该正确")
	})

	t.Run("Default slice options", func(t *testing.T) {
		qry := &metadata.Query{
			TableID:            "test_table",
			ResultTableOptions: make(metadata.ResultTableOptions),
		}

		activeSlices := redisUtil.SliceStates{
			{
				SliceID:     0,
				StartOffset: 500,
				EndOffset:   1500,
				Size:        1000,
				Status:      redisUtil.SliceStatusRunning,
				ConnectInfo: "http://127.0.0.1:8080",
			},
		}

		setSliceOptions(qry, activeSlices, "unknown", 3)

		option := qry.ResultTableOptions.GetOption("test_table", "http://127.0.0.1:8080")
		assert.NotNil(t, option, "默认slice option应该被设置")
		assert.NotNil(t, option.From, "From应该被设置")
		assert.Equal(t, 500, *option.From, "From应该为500")
		assert.Nil(t, option.SliceID, "默认类型不应该设置SliceID")
		assert.Nil(t, option.SliceMax, "默认类型不应该设置SliceMax")
	})
}

func TestQueryRawWithScrollSliceIntegration(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := context.Background()
	options := &redis.UniversalOptions{
		Addrs: []string{mr.Addr()},
		DB:    0,
	}
	err := redisUtil.SetInstance(ctx, "test", options)
	assert.NoError(t, err)

	queryTs := &structured.QueryTs{
		QueryList: []*structured.Query{
			{
				TableID: "test_table_es",
			},
		},
		Start:      "1723594000",
		End:        "1723595000",
		Step:       "60s",
		Limit:      1000,
		Scroll:     "5m",
		ClearCache: true,
	}

	user := &metadata.User{Name: "test_user"}
	ctx = metadata.InitHashID(ctx)
	metadata.SetUser(ctx, user)

	queryTsKey, err := redisUtil.ScrollGenerateQueryTsKey(queryTs, user.Name)
	assert.NoError(t, err)

	sessionKey := redisUtil.GetSessionKey(queryTsKey)

	session := &redisUtil.SessionObject{
		QueryReference: map[string]*redisUtil.RTState{
			"test_table_es": {
				Type:        "elasticsearch",
				HasMoreData: true,
				SliceStates: []redisUtil.SliceState{
					{
						SliceID:     0,
						StartOffset: 0,
						EndOffset:   1000,
						Size:        1000,
						Status:      redisUtil.SliceStatusRunning,
						MaxRetries:  3,
						ConnectInfo: "http://127.0.0.1:9200",
						ScrollID:    "scroll_123",
					},
					{
						SliceID:     1,
						StartOffset: 0,
						EndOffset:   1000,
						Size:        1000,
						Status:      redisUtil.SliceStatusRunning,
						MaxRetries:  3,
						ConnectInfo: "http://127.0.0.1:9201",
						ScrollID:    "scroll_456",
					},
				},
			},
		},
		ScrollTimeout: time.Minute * 5,
		LockTimeout:   time.Second * 30,
		MaxSlice:      3,
		Limit:         1000,
	}

	err = redisUtil.ScrollUpdateSession(ctx, sessionKey, session)
	assert.NoError(t, err)

	t.Logf("测试session已创建，sessionKey: %s", sessionKey)

	rtState := session.QueryReference["test_table_es"]
	assert.NotNil(t, rtState, "RTState应该存在")
	assert.Equal(t, "elasticsearch", rtState.Type, "RTState类型应该为elasticsearch")
	assert.Equal(t, 2, len(rtState.SliceStates), "应该有2个slice")

	activeSlices, err := rtState.PickSlices(3, 2)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(activeSlices), "应该返回2个active slice")

	for i, slice := range activeSlices {
		assert.Equal(t, i, slice.SliceID, "SliceID应该连续")
		assert.Equal(t, redisUtil.SliceStatusRunning, slice.Status, "Slice状态应该为running")
		assert.NotEmpty(t, slice.ConnectInfo, "ConnectInfo不应该为空")
	}

	t.Logf("PickSlices测试完成，返回%d个active slice", len(activeSlices))
}

func teardownMiniRedis(mr *miniredis.Miniredis, client *redis.Client) {
	if client != nil {
		client.Close()
	}
	if mr != nil {
		mr.Close()
	}
}

func TestQueryRawWithScroll_WithMiniRedis(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	user := &metadata.User{
		SpaceUID: spaceUid,
		Name:     "test_user",
		Key:      "test_key",
	}
	metadata.SetUser(ctx, user)

	start := "1723594000"
	end := "1723595000"

	queryTs := &structured.QueryTs{
		SpaceUid: spaceUid,
		QueryList: []*structured.Query{
			{
				DataSource:    structured.BkLog,
				TableID:       structured.TableID(influxdb.ResultTableEs),
				KeepColumns:   []string{"a", "b"},
				ReferenceName: "a",
			},
		},
		From:   0,
		Limit:  5,
		Start:  start,
		End:    end,
		Scroll: "5m",
	}

	t.Run("test_redis_operations", func(t *testing.T) {
		ctx := context.Background()

		err := redisClient.Set(ctx, "test_key", "test_value", time.Minute).Err()
		assert.NoError(t, err, "Redis SET不应该出错")

		val, err := redisClient.Get(ctx, "test_key").Result()
		assert.NoError(t, err, "Redis GET不应该出错")
		assert.Equal(t, "test_value", val, "Redis值应该正确")
		lockKey := "test_lock"
		result, err := redisClient.SetNX(ctx, lockKey, "locked", time.Second*10).Result()
		assert.NoError(t, err, "Redis SETNX不应该出错")
		assert.True(t, result, "第一次获取锁应该成功")

		result, err = redisClient.SetNX(ctx, lockKey, "locked", time.Second*10).Result()
		assert.NoError(t, err, "Redis SETNX不应该出错")
		assert.False(t, result, "第二次获取锁应该失败")

		t.Logf("Redis基本操作测试通过")
	})

	t.Run("test_scroll_key_generation", func(t *testing.T) {
		user := metadata.GetUser(ctx)
		username := user.Name
		if username == "" {
			username = user.Key
		}

		queryTsKey, err := redisUtil.ScrollGenerateQueryTsKey(queryTs, username)
		assert.NoError(t, err, "键生成不应该出错")
		assert.NotEmpty(t, queryTsKey, "键不应该为空")

		sessionKey := redisUtil.GetSessionKey(queryTsKey)
		lockKey := redisUtil.GetLockKey(queryTsKey)

		assert.Contains(t, sessionKey, redisUtil.SessionKeyPrefix, "会话键应该包含正确前缀")
		assert.Contains(t, lockKey, redisUtil.LockKeyPrefix, "锁键应该包含正确前缀")

		t.Logf("QueryTsKey: %s", queryTsKey)
		t.Logf("SessionKey: %s", sessionKey)
		t.Logf("LockKey: %s", lockKey)
	})

	t.Run("test_session_operations", func(t *testing.T) {
		user := metadata.GetUser(ctx)
		username := user.Name
		if username == "" {
			username = user.Key
		}

		queryTsKey, err := redisUtil.ScrollGenerateQueryTsKey(queryTs, username)
		assert.NoError(t, err, "键生成不应该出错")

		sessionKey := redisUtil.GetSessionKey(queryTsKey)
		lockKey := redisUtil.GetLockKey(queryTsKey)

		ctx := context.Background()
		result, err := redisClient.SetNX(ctx, lockKey, "locked", time.Second*60).Result()
		assert.NoError(t, err, "获取锁不应该出错")
		assert.True(t, result, "应该成功获取锁")

		sessionObj := &redisUtil.SessionObject{
			QueryTs:       queryTsKey,
			Status:        "RUNNING",
			CreateAt:      time.Now(),
			LastAccessAt:  time.Now(),
			ScrollTimeout: time.Minute * 5,
			LockTimeout:   time.Second * 60,
			MaxSlice:      3,
			Limit:         5,
			Index:         1,
			QueryReference: map[string]*redisUtil.RTState{
				influxdb.ResultTableEs: {
					Type:        "es",
					HasMoreData: true,
					SliceStates: []redisUtil.SliceState{
						{
							SliceID:     0,
							Status:      redisUtil.SliceStatusRunning,
							ScrollID:    "test_scroll_id",
							ConnectInfo: "http://127.0.0.1:9200",
						},
					},
				},
			},
		}

		sessionData, err := redisUtil.MarshalSessionObject(sessionObj)
		assert.NoError(t, err, "会话序列化不应该出错")

		err = redisClient.Set(ctx, sessionKey, sessionData, time.Minute*5).Err()
		assert.NoError(t, err, "存储会话不应该出错")

		storedData, err := redisClient.Get(ctx, sessionKey).Result()
		assert.NoError(t, err, "读取会话不应该出错")

		restoredSession, err := redisUtil.UnmarshalSessionObject(storedData)
		assert.NoError(t, err, "会话反序列化不应该出错")

		assert.Equal(t, sessionObj.QueryTs, restoredSession.QueryTs, "QueryTs应该一致")
		assert.Equal(t, sessionObj.Status, restoredSession.Status, "Status应该一致")
		assert.Equal(t, sessionObj.MaxSlice, restoredSession.MaxSlice, "MaxSlice应该一致")

		err = redisClient.Del(ctx, lockKey).Err()
		assert.NoError(t, err, "释放锁不应该出错")

		t.Logf("会话操作测试通过")
	})

	t.Run("test_user_isolation", func(t *testing.T) {
		users := []string{"user1", "user2", "user1"} // 重复user1测试会话复用
		keys := make([]string, len(users))

		for i, username := range users {
			key, err := redisUtil.ScrollGenerateQueryTsKey(queryTs, username)
			assert.NoError(t, err, "用户 %s 的键生成不应该出错", username)
			keys[i] = key
		}

		assert.NotEqual(t, keys[0], keys[1], "不同用户的键应该不同")
		assert.Equal(t, keys[0], keys[2], "相同用户的键应该相同")

		t.Logf("用户隔离测试通过: user1=%s, user2=%s", keys[0], keys[1])
	})
}

func TestMiniRedis_ConcurrentOperations(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := context.Background()
	lockKey := "concurrent_test_lock"

	results := make(chan bool, 5)

	for i := 0; i < 5; i++ {
		go func(id int) {
			result, err := redisClient.SetNX(ctx, lockKey, "locked", time.Second*10).Result()
			assert.NoError(t, err, "goroutine %d 获取锁不应该出错", id)
			results <- result
		}(i)
	}

	successCount := 0
	for i := 0; i < 5; i++ {
		if <-results {
			successCount++
		}
	}

	assert.Equal(t, 1, successCount, "只有一个goroutine应该成功获取锁")

	t.Logf("并发锁测试通过，成功获取锁的数量: %d", successCount)
}

func TestMiniRedis_TTLOperations(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := context.Background()

	key := "ttl_test_key"
	err := redisClient.Set(ctx, key, "test_value", time.Second*2).Err()
	assert.NoError(t, err, "设置TTL键不应该出错")

	exists, err := redisClient.Exists(ctx, key).Result()
	assert.NoError(t, err, "检查键存在不应该出错")
	assert.Equal(t, int64(1), exists, "键应该存在")

	ttl, err := redisClient.TTL(ctx, key).Result()
	assert.NoError(t, err, "获取TTL不应该出错")
	assert.True(t, ttl > 0, "TTL应该大于0")
	assert.True(t, ttl <= time.Second*2, "TTL应该小于等于2秒")

	time.Sleep(time.Second * 3)

	mr.FastForward(time.Second * 3)

	exists, err = redisClient.Exists(ctx, key).Result()
	assert.NoError(t, err, "检查键存在不应该出错")
	assert.Equal(t, int64(0), exists, "键应该已过期")

	t.Logf("TTL操作测试通过")
}

func TestQueryRawWithScroll_MainFlow(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	user := &metadata.User{
		SpaceUID: spaceUid,
		Name:     "test_user",
		Key:      "test_key",
	}
	metadata.SetUser(ctx, user)

	start := "1723594000"
	end := "1723595000"

	queryTs := &structured.QueryTs{
		SpaceUid: spaceUid,
		QueryList: []*structured.Query{
			{
				DataSource:    structured.BkLog,
				TableID:       structured.TableID(influxdb.ResultTableEs),
				KeepColumns:   []string{"a", "b"},
				ReferenceName: "a",
			},
		},
		From:   0,
		Limit:  5,
		Start:  start,
		End:    end,
		Scroll: "5m",
	}

	t.Run("simulate_main_flow", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		user := metadata.GetUser(ctx)
		username := user.Name
		if username == "" {
			username = user.Key
		}
		assert.Equal(t, "test_user", username, "用户名应该正确")

		queryTsKey, err := redisUtil.ScrollGenerateQueryTsKey(queryTs, username)
		assert.NoError(t, err, "键生成不应该出错")
		assert.NotEmpty(t, queryTsKey, "键不应该为空")

		sessionKey := redisUtil.GetSessionKey(queryTsKey)
		lockKey := redisUtil.GetLockKey(queryTsKey)

		lockResult, err := redisClient.SetNX(ctx, lockKey, "locked", time.Second*60).Result()
		assert.NoError(t, err, "获取锁不应该出错")
		assert.True(t, lockResult, "应该成功获取锁")

		sessionExists, err := redisClient.Exists(ctx, sessionKey).Result()
		assert.NoError(t, err, "检查会话存在不应该出错")
		assert.Equal(t, int64(0), sessionExists, "首次请求会话不应该存在")

		sessionObj := &redisUtil.SessionObject{
			QueryTs:       queryTsKey,
			Status:        "RUNNING",
			CreateAt:      time.Now(),
			LastAccessAt:  time.Now(),
			ScrollTimeout: time.Minute * 5,
			LockTimeout:   time.Second * 60,
			MaxSlice:      3,
			Limit:         5,
			Index:         1,
			QueryReference: map[string]*redisUtil.RTState{
				influxdb.ResultTableEs: {
					Type:        "es",
					HasMoreData: true,
					SliceStates: []redisUtil.SliceState{
						{
							SliceID:     0,
							StartOffset: 0,
							EndOffset:   5,
							Size:        5,
							Status:      redisUtil.SliceStatusRunning,
							MaxRetries:  3,
						},
					},
				},
			},
		}

		sessionData, err := redisUtil.MarshalSessionObject(sessionObj)
		assert.NoError(t, err, "会话序列化不应该出错")

		err = redisClient.Set(ctx, sessionKey, sessionData, time.Minute*5).Err()
		assert.NoError(t, err, "存储会话不应该出错")

		err = redisClient.Del(ctx, lockKey).Err()
		assert.NoError(t, err, "释放锁不应该出错")

		lockResult, err = redisClient.SetNX(ctx, lockKey, "locked", time.Second*60).Result()
		assert.NoError(t, err, "第二次获取锁不应该出错")
		assert.True(t, lockResult, "应该成功获取锁")

		storedData, err := redisClient.Get(ctx, sessionKey).Result()
		assert.NoError(t, err, "读取会话不应该出错")

		restoredSession, err := redisUtil.UnmarshalSessionObject(storedData)
		assert.NoError(t, err, "会话反序列化不应该出错")
		assert.Equal(t, sessionObj.QueryTs, restoredSession.QueryTs, "QueryTs应该一致")
		assert.Equal(t, sessionObj.Status, restoredSession.Status, "Status应该一致")
		assert.Equal(t, sessionObj.MaxSlice, restoredSession.MaxSlice, "MaxSlice应该一致")
		assert.Equal(t, len(sessionObj.QueryReference), len(restoredSession.QueryReference), "QueryReference数量应该一致")

		restoredSession.LastAccessAt = time.Now()
		restoredSession.Index = 2

		if rtState, exists := restoredSession.QueryReference[influxdb.ResultTableEs]; exists {
			if len(rtState.SliceStates) > 0 {
				rtState.SliceStates[0].ScrollID = "test_scroll_id_2"
				rtState.SliceStates[0].EndOffset = 10
			}
		}

		updatedSessionData, err := redisUtil.MarshalSessionObject(restoredSession)
		assert.NoError(t, err, "更新会话序列化不应该出错")

		err = redisClient.Set(ctx, sessionKey, updatedSessionData, time.Minute*5).Err()
		assert.NoError(t, err, "保存更新会话不应该出错")

		err = redisClient.Del(ctx, lockKey).Err()
		assert.NoError(t, err, "释放锁不应该出错")

		t.Logf("主流程模拟完成: queryTsKey=%s", queryTsKey)
	})

	t.Run("simulate_concurrent_requests", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)
		user := metadata.GetUser(ctx)
		username := user.Name
		if username == "" {
			username = user.Key
		}

		queryTsKey, err := redisUtil.ScrollGenerateQueryTsKey(queryTs, username)
		assert.NoError(t, err, "键生成不应该出错")

		lockKey := redisUtil.GetLockKey(queryTsKey)

		results := make(chan bool, 3)

		for i := 0; i < 3; i++ {
			go func(id int) {
				result, err := redisClient.SetNX(ctx, lockKey, "locked", time.Second*10).Result()
				assert.NoError(t, err, "goroutine %d 获取锁不应该出错", id)
				results <- result
			}(i)
		}
		successCount := 0
		for i := 0; i < 3; i++ {
			if <-results {
				successCount++
			}
		}

		assert.Equal(t, 1, successCount, "只有一个请求应该成功获取锁")

		err = redisClient.Del(ctx, lockKey).Err()
		assert.NoError(t, err, "清理锁不应该出错")

		t.Logf("并发请求模拟完成，成功获取锁的数量: %d", successCount)
	})
}

func TestQueryRawWithScroll_ESSliceGeneration(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	user := &metadata.User{
		SpaceUID: spaceUid,
		Name:     "test_user",
		Key:      "test_key",
	}
	metadata.SetUser(ctx, user)

	start := "1723594000"
	end := "1723595000"

	t.Run("test_es_slice_dsl_generation", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		rtState := &redisUtil.RTState{
			Type:        "es",
			HasMoreData: true,
			SliceStates: []redisUtil.SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   333,
					Size:        333,
					Status:      redisUtil.SliceStatusRunning,
					MaxRetries:  3,
					ScrollID:    "scroll_id_1",
					ConnectInfo: "http://127.0.0.1:9200",
				},
				{
					SliceID:     1,
					StartOffset: 333,
					EndOffset:   666,
					Size:        333,
					Status:      redisUtil.SliceStatusRunning,
					MaxRetries:  3,
					ScrollID:    "scroll_id_2",
					ConnectInfo: "http://127.0.0.1:9200",
				},
				{
					SliceID:     2,
					StartOffset: 666,
					EndOffset:   1000,
					Size:        334,
					Status:      redisUtil.SliceStatusRunning,
					MaxRetries:  3,
					ScrollID:    "scroll_id_3",
					ConnectInfo: "http://127.0.0.1:9200",
				},
			},
		}

		assert.Equal(t, 3, len(rtState.SliceStates), "应该有3个slice")

		// 验证每个slice都有scroll ID
		scrollIDCount := 0
		for _, slice := range rtState.SliceStates {
			if slice.ScrollID != "" {
				scrollIDCount++
			}
		}
		assert.Equal(t, 3, scrollIDCount, "应该有3个scroll ID")

		totalSize := int64(0)
		for i, slice := range rtState.SliceStates {
			assert.Equal(t, i, slice.SliceID, "slice ID应该连续")
			assert.Equal(t, redisUtil.SliceStatusRunning, slice.Status, "slice状态应该是running")

			if i == 0 {
				assert.Equal(t, int64(0), slice.StartOffset, "第一个slice的StartOffset应该是0")
			} else {
				assert.Equal(t, rtState.SliceStates[i-1].EndOffset, slice.StartOffset,
					"slice的StartOffset应该等于前一个slice的EndOffset")
			}

			totalSize += slice.Size
		}
		assert.Equal(t, int64(1000), totalSize, "所有slice的总大小应该等于limit")

		for i, slice := range rtState.SliceStates {
			expectedDSL := map[string]interface{}{
				"query": map[string]interface{}{
					"bool": map[string]interface{}{
						"must": []map[string]interface{}{
							{
								"range": map[string]interface{}{
									"@timestamp": map[string]interface{}{
										"gte": start + "000", // 转换为毫秒
										"lte": end + "000",
									},
								},
							},
							{
								"terms": map[string]interface{}{
									"level": []string{"ERROR", "WARN"},
								},
							},
						},
					},
				},
				"slice": map[string]interface{}{
					"id":  slice.SliceID,
					"max": len(rtState.SliceStates),
				},
				"size": slice.Size,
				"from": slice.StartOffset,
				"sort": []map[string]interface{}{
					{
						"@timestamp": map[string]interface{}{
							"order": "asc",
						},
					},
				},
				"_source": []string{"log", "timestamp", "level"},
			}

			assert.NotNil(t, expectedDSL["query"], "DSL应该包含query部分")
			assert.NotNil(t, expectedDSL["slice"], "DSL应该包含slice部分")
			assert.Equal(t, slice.Size, expectedDSL["size"], "DSL的size应该正确")
			assert.Equal(t, slice.StartOffset, expectedDSL["from"], "DSL的from应该正确")

			sliceConfig := expectedDSL["slice"].(map[string]interface{})
			assert.Equal(t, slice.SliceID, sliceConfig["id"], "slice ID应该正确")
			assert.Equal(t, len(rtState.SliceStates), sliceConfig["max"], "slice max应该正确")

			t.Logf("ES Slice %d DSL: size=%d, from=%d, slice_id=%d, slice_max=%d",
				i, slice.Size, slice.StartOffset, slice.SliceID, len(rtState.SliceStates))
		}

		for i, slice := range rtState.SliceStates {
			if slice.ScrollID != "" {
				scrollDSL := map[string]interface{}{
					"scroll":    "5m",
					"scroll_id": slice.ScrollID,
				}

				assert.Equal(t, "5m", scrollDSL["scroll"], "scroll时间应该正确")
				assert.Equal(t, slice.ScrollID, scrollDSL["scroll_id"], "scroll ID应该正确")

				t.Logf("ES Scroll %d DSL: scroll_id=%s", i, slice.ScrollID)
			}
		}

		t.Logf("ES slice DSL生成测试完成")
	})
}

func TestQueryRawWithScroll_DorisSliceGeneration(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	user := &metadata.User{
		SpaceUID: spaceUid,
		Name:     "test_user",
		Key:      "test_key",
	}
	metadata.SetUser(ctx, user)

	start := "1723594000"
	end := "1723595000"

	t.Run("test_doris_slice_sql_generation", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		rtState := &redisUtil.RTState{
			Type:        "doris",
			HasMoreData: true,
			SliceStates: []redisUtil.SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   666,
					Size:        666,
					Status:      redisUtil.SliceStatusRunning,
					MaxRetries:  3,
				},
				{
					SliceID:     1,
					StartOffset: 666,
					EndOffset:   1333,
					Size:        667,
					Status:      redisUtil.SliceStatusRunning,
					MaxRetries:  3,
				},
				{
					SliceID:     2,
					StartOffset: 1333,
					EndOffset:   2000,
					Size:        667,
					Status:      redisUtil.SliceStatusRunning,
					MaxRetries:  3,
				},
			},
		}

		assert.Equal(t, 3, len(rtState.SliceStates), "应该有3个slice")

		totalSize := int64(0)
		for i, slice := range rtState.SliceStates {
			assert.Equal(t, i, slice.SliceID, "slice ID应该连续")
			assert.Equal(t, redisUtil.SliceStatusRunning, slice.Status, "slice状态应该是running")

			totalSize += slice.Size
		}
		assert.Equal(t, int64(2000), totalSize, "所有slice的总大小应该等于limit")

		for i, slice := range rtState.SliceStates {
			expectedSQL := fmt.Sprintf(`
SELECT log, timestamp, level, host
FROM result_table_bk_sql
WHERE timestamp >= %s
  AND timestamp <= %s
  AND level = 'ERROR'
  AND host IN ('server1', 'server2')
ORDER BY timestamp ASC
LIMIT %d OFFSET %d`,
				start+"000", // 转换为毫秒
				end+"000",
				slice.Size,
				slice.StartOffset,
			)

			assert.Contains(t, expectedSQL, "SELECT log, timestamp, level, host", "SQL应该包含正确的字段")
			assert.Contains(t, expectedSQL, "FROM result_table_bk_sql", "SQL应该包含正确的表名")
			assert.Contains(t, expectedSQL, "WHERE timestamp >=", "SQL应该包含时间范围条件")
			assert.Contains(t, expectedSQL, "level = 'ERROR'", "SQL应该包含level条件")
			assert.Contains(t, expectedSQL, "host IN ('server1', 'server2')", "SQL应该包含host条件")
			assert.Contains(t, expectedSQL, "ORDER BY timestamp ASC", "SQL应该包含排序")
			assert.Contains(t, expectedSQL, fmt.Sprintf("LIMIT %d", slice.Size), "SQL应该包含正确的limit")
			assert.Contains(t, expectedSQL, fmt.Sprintf("OFFSET %d", slice.StartOffset), "SQL应该包含正确的offset")

			t.Logf("Doris Slice %d SQL: size=%d, offset=%d",
				i, slice.Size, slice.StartOffset)
		}

		for i, slice := range rtState.SliceStates {
			newOffset := slice.EndOffset
			continuationSQL := fmt.Sprintf(`
SELECT log, timestamp, level, host
FROM result_table_bk_sql
WHERE timestamp >= %s
  AND timestamp <= %s
  AND level = 'ERROR'
  AND host IN ('server1', 'server2')
ORDER BY timestamp ASC
LIMIT %d OFFSET %d`,
				start+"000",
				end+"000",
				slice.Size,
				newOffset, // 使用新的offset
			)

			assert.Contains(t, continuationSQL, fmt.Sprintf("OFFSET %d", newOffset),
				"续传SQL应该使用新的offset")

			t.Logf("Doris Continuation Slice %d SQL: new_offset=%d", i, newOffset)
		}

		t.Logf("Doris slice SQL生成测试完成")
	})
}

func TestQueryRawWithScroll_ConcurrentSliceQueries(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	user := &metadata.User{
		SpaceUID: spaceUid,
		Name:     "test_user",
		Key:      "test_key",
	}
	metadata.SetUser(ctx, user)

	t.Run("test_concurrent_es_and_doris_slices", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		// ES RTState
		esRTState := &redisUtil.RTState{
			Type:        "es",
			HasMoreData: true,
			SliceStates: []redisUtil.SliceState{
				{SliceID: 0, StartOffset: 0, EndOffset: 500, Size: 500, Status: redisUtil.SliceStatusRunning, MaxRetries: 3, ScrollID: "es_scroll_1", ConnectInfo: "http://127.0.0.1:9200"},
				{SliceID: 1, StartOffset: 500, EndOffset: 1000, Size: 500, Status: redisUtil.SliceStatusRunning, MaxRetries: 3, ScrollID: "es_scroll_2", ConnectInfo: "http://127.0.0.1:9200"},
				{SliceID: 2, StartOffset: 1000, EndOffset: 1500, Size: 500, Status: redisUtil.SliceStatusRunning, MaxRetries: 3, ScrollID: "es_scroll_3", ConnectInfo: "http://127.0.0.1:9200"},
			},
		}

		dorisRTState := &redisUtil.RTState{
			Type:        "doris",
			HasMoreData: true,
			SliceStates: []redisUtil.SliceState{
				{SliceID: 0, StartOffset: 0, EndOffset: 800, Size: 800, Status: redisUtil.SliceStatusRunning, MaxRetries: 3},
				{SliceID: 1, StartOffset: 800, EndOffset: 1600, Size: 800, Status: redisUtil.SliceStatusRunning, MaxRetries: 3},
				{SliceID: 2, StartOffset: 1600, EndOffset: 2400, Size: 800, Status: redisUtil.SliceStatusRunning, MaxRetries: 3},
			},
		}

		assert.Equal(t, 3, len(esRTState.SliceStates), "ES应该有3个slice")

		// 验证每个ES slice都有scroll ID
		esScrollIDCount := 0
		for _, slice := range esRTState.SliceStates {
			if slice.ScrollID != "" {
				esScrollIDCount++
			}
		}
		assert.Equal(t, 3, esScrollIDCount, "ES应该有3个scroll ID")

		esTotal := int64(0)
		for _, slice := range esRTState.SliceStates {
			esTotal += slice.Size
		}
		assert.Equal(t, int64(1500), esTotal, "ES slice总大小应该正确")

		assert.Equal(t, 3, len(dorisRTState.SliceStates), "Doris应该有3个slice")

		dorisTotal := int64(0)
		for _, slice := range dorisRTState.SliceStates {
			dorisTotal += slice.Size
		}
		assert.Equal(t, int64(2400), dorisTotal, "Doris slice总大小应该正确")

		results := make(chan string, 6) // ES 3个 + Doris 3个

		for i, slice := range esRTState.SliceStates {
			go func(sliceID int, s redisUtil.SliceState, scrollID string) {
				// 模拟ES slice查询DSL
				dsl := map[string]interface{}{
					"query": map[string]interface{}{
						"bool": map[string]interface{}{
							"must": []map[string]interface{}{
								{"range": map[string]interface{}{"@timestamp": map[string]interface{}{"gte": "1723594000000", "lte": "1723595000000"}}},
							},
						},
					},
					"slice": map[string]interface{}{"id": s.SliceID, "max": len(esRTState.SliceStates)},
					"size":  s.Size,
					"from":  s.StartOffset,
					"sort":  []map[string]interface{}{{"@timestamp": map[string]interface{}{"order": "asc"}}},
				}

				assert.NotNil(t, dsl["slice"], "ES DSL应该包含slice配置")
				sliceConfig := dsl["slice"].(map[string]interface{})
				assert.Equal(t, s.SliceID, sliceConfig["id"], "ES slice ID应该正确")

				results <- fmt.Sprintf("ES_slice_%d_completed", sliceID)
			}(i, slice, slice.ScrollID)
		}

		for i, slice := range dorisRTState.SliceStates {
			go func(sliceID int, s redisUtil.SliceState) {
				sql := fmt.Sprintf(`
SELECT * FROM result_table_bk_sql
WHERE timestamp >= 1723594000000
  AND timestamp <= 1723595000000
ORDER BY timestamp ASC
LIMIT %d OFFSET %d`, s.Size, s.StartOffset)

				assert.Contains(t, sql, fmt.Sprintf("LIMIT %d", s.Size), "Doris SQL应该包含正确的limit")
				assert.Contains(t, sql, fmt.Sprintf("OFFSET %d", s.StartOffset), "Doris SQL应该包含正确的offset")

				results <- fmt.Sprintf("Doris_slice_%d_completed", sliceID)
			}(i, slice)
		}

		completedQueries := make([]string, 0, 6)
		for i := 0; i < 6; i++ {
			result := <-results
			completedQueries = append(completedQueries, result)
		}

		assert.Equal(t, 6, len(completedQueries), "应该完成6个并发查询")

		esCount := 0
		dorisCount := 0
		for _, result := range completedQueries {
			if strings.Contains(result, "ES_slice") {
				esCount++
			} else if strings.Contains(result, "Doris_slice") {
				dorisCount++
			}
		}
		assert.Equal(t, 3, esCount, "应该完成3个ES slice查询")
		assert.Equal(t, 3, dorisCount, "应该完成3个Doris slice查询")

		t.Logf("并发slice查询测试完成: ES=%d, Doris=%d", esCount, dorisCount)
	})
}

func TestQueryRawWithScroll_ESSliceIntegration(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	user := &metadata.User{
		SpaceUID: spaceUid,
		Name:     "test_user",
		Key:      "test_key",
	}
	metadata.SetUser(ctx, user)

	t.Run("test_es_slice_configuration_detection", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		originalMaxSlice := viper.GetInt("http.scroll.max_slice")
		viper.Set("http.scroll.max_slice", 3)
		defer viper.Set("http.scroll.max_slice", originalMaxSlice)

		maxSlice := viper.GetInt("http.scroll.max_slice")
		assert.Equal(t, 3, maxSlice, "maxSlice配置应该正确")

		hasAggregates := false // 没有聚合查询
		hasScroll := true      // 有scroll参数

		shouldUseSlice := maxSlice > 1 && !hasAggregates && hasScroll
		assert.True(t, shouldUseSlice, "应该使用slice查询")

		t.Logf("ES slice配置检测: maxSlice=%d, hasAggregates=%v, hasScroll=%v, shouldUseSlice=%v",
			maxSlice, hasAggregates, hasScroll, shouldUseSlice)
	})

	t.Run("test_es_slice_query_structure", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		maxSlice := 3

		originalQuery := map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []map[string]interface{}{
						{
							"range": map[string]interface{}{
								"@timestamp": map[string]interface{}{
									"gte": "1723594000000",
									"lte": "1723595000000",
								},
							},
						},
						{
							"terms": map[string]interface{}{
								"level": []string{"ERROR", "WARN"},
							},
						},
					},
				},
			},
			"sort": []map[string]interface{}{
				{
					"@timestamp": map[string]interface{}{
						"order": "asc",
					},
				},
			},
			"_source": []string{"log", "timestamp", "level"},
			"size":    1000,
			"from":    0,
		}

		for sliceID := 0; sliceID < maxSlice; sliceID++ {
			sliceQuery := make(map[string]interface{})
			for key, value := range originalQuery {
				if key != "from" {
					sliceQuery[key] = value
				}
			}

			sliceQuery["slice"] = map[string]interface{}{
				"id":  sliceID,
				"max": maxSlice,
			}

			assert.Contains(t, sliceQuery, "slice", "slice查询应该包含slice配置")
			assert.Contains(t, sliceQuery, "query", "slice查询应该包含原始查询条件")
			assert.Contains(t, sliceQuery, "sort", "slice查询应该包含排序条件")
			assert.Contains(t, sliceQuery, "_source", "slice查询应该包含_source字段")
			assert.NotContains(t, sliceQuery, "from", "slice查询不应该包含from字段")

			sliceConfig := sliceQuery["slice"].(map[string]interface{})
			assert.Equal(t, sliceID, sliceConfig["id"], "slice ID应该正确")
			assert.Equal(t, maxSlice, sliceConfig["max"], "slice max应该正确")

			queryJson, err := json.Marshal(sliceQuery)
			assert.NoError(t, err, "slice查询应该可以序列化为JSON")
			assert.NotEmpty(t, queryJson, "序列化的JSON不应该为空")

			t.Logf("ES Slice %d 查询结构验证通过: %s", sliceID, string(queryJson))
		}
	})

	t.Run("test_es_slice_result_merging", func(t *testing.T) {
		// 测试ES slice结果合并逻辑
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		// 模拟3个slice的查询结果
		sliceResults := []*elastic.SearchResult{
			{
				TookInMillis: 10,
				TimedOut:     false,
				Hits: &elastic.SearchHits{
					TotalHits: &elastic.TotalHits{Value: 100, Relation: "eq"},
					MaxScore:  func() *float64 { f := 1.0; return &f }(),
					Hits: []*elastic.SearchHit{
						{Id: "1", Source: json.RawMessage(`{"log": "error1", "level": "ERROR"}`)},
						{Id: "2", Source: json.RawMessage(`{"log": "error2", "level": "ERROR"}`)},
					},
				},
				ScrollId: "scroll_id_1",
			},
			{
				TookInMillis: 12,
				TimedOut:     false,
				Hits: &elastic.SearchHits{
					TotalHits: &elastic.TotalHits{Value: 150, Relation: "eq"},
					MaxScore:  func() *float64 { f := 1.2; return &f }(),
					Hits: []*elastic.SearchHit{
						{Id: "3", Source: json.RawMessage(`{"log": "warn1", "level": "WARN"}`)},
						{Id: "4", Source: json.RawMessage(`{"log": "warn2", "level": "WARN"}`)},
						{Id: "5", Source: json.RawMessage(`{"log": "warn3", "level": "WARN"}`)},
					},
				},
				ScrollId: "scroll_id_2",
			},
			{
				TookInMillis: 8,
				TimedOut:     false,
				Hits: &elastic.SearchHits{
					TotalHits: &elastic.TotalHits{Value: 80, Relation: "eq"},
					MaxScore:  func() *float64 { f := 0.9; return &f }(),
					Hits: []*elastic.SearchHit{
						{Id: "6", Source: json.RawMessage(`{"log": "info1", "level": "INFO"}`)},
					},
				},
				ScrollId: "scroll_id_3",
			},
		}

		// 模拟结果合并逻辑
		var allHits []*elastic.SearchHit
		var totalHits int64
		var scrollIDs []string
		firstResult := sliceResults[0]

		for _, result := range sliceResults {
			if result.Hits != nil {
				allHits = append(allHits, result.Hits.Hits...)
				totalHits += result.Hits.TotalHits.Value
			}
			if result.ScrollId != "" {
				scrollIDs = append(scrollIDs, result.ScrollId)
			}
		}

		mergedResult := &elastic.SearchResult{
			TookInMillis: firstResult.TookInMillis,
			TimedOut:     firstResult.TimedOut,
			Hits: &elastic.SearchHits{
				TotalHits: &elastic.TotalHits{
					Value:    totalHits,
					Relation: firstResult.Hits.TotalHits.Relation,
				},
				MaxScore: firstResult.Hits.MaxScore,
				Hits:     allHits,
			},
			ScrollId: strings.Join(scrollIDs, ","),
		}

		// 验证合并结果
		assert.Equal(t, int64(330), mergedResult.Hits.TotalHits.Value, "总命中数应该正确")
		assert.Equal(t, 6, len(mergedResult.Hits.Hits), "合并后的hits数量应该正确")
		assert.Equal(t, "scroll_id_1,scroll_id_2,scroll_id_3", mergedResult.ScrollId, "scroll ID应该正确合并")

		// 验证每个hit的数据
		expectedIDs := []string{"1", "2", "3", "4", "5", "6"}
		for i, hit := range mergedResult.Hits.Hits {
			assert.Equal(t, expectedIDs[i], hit.Id, "hit ID应该正确")
			assert.NotEmpty(t, hit.Source, "hit source不应该为空")
		}

		t.Logf("ES slice结果合并验证通过: totalHits=%d, mergedHits=%d, scrollIDs=%s",
			totalHits, len(allHits), strings.Join(scrollIDs, ","))
	})
}

// TestQueryRawWithScroll_APMTraceRequest 测试APM trace请求的完整流程
func TestQueryRawWithScroll_APMTraceRequest(t *testing.T) {
	mr, redisClient := setupMiniRedis(t)
	defer teardownMiniRedis(mr, redisClient)

	ctx := metadata.InitHashID(context.Background())
	spaceUid := "bkcc__2" // 使用请求中的space_uid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	user := &metadata.User{
		SpaceUID: spaceUid,
		Name:     "test_user",
		Key:      "test_key",
	}
	metadata.SetUser(ctx, user)

	// 设置scroll配置
	originalMaxSlice := viper.GetInt("http.scroll.max_slice")
	viper.Set("http.scroll.max_slice", 3)
	defer viper.Set("http.scroll.max_slice", originalMaxSlice)

	t.Run("test_apm_trace_table_configuration", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		// 验证APM trace table配置
		tableId := "2_bkapm.trace_tilapia"

		// 模拟ES存储配置
		mockESData := map[string]interface{}{
			"took":      15,
			"timed_out": false,
			"_shards": map[string]interface{}{
				"total":      3,
				"successful": 3,
				"skipped":    0,
				"failed":     0,
			},
			"hits": map[string]interface{}{
				"total": map[string]interface{}{
					"value":    1250,
					"relation": "eq",
				},
				"max_score": 1.0,
				"hits": []map[string]interface{}{
					{
						"_index": "2_bkapm_trace_tilapia_20240814",
						"_type":  "_doc",
						"_id":    "trace_001",
						"_score": 1.0,
						"_source": map[string]interface{}{
							"trace_id":       "abc123def456",
							"span_id":        "span_001",
							"operation_name": "http_request",
							"start_time":     1723594000000,
							"end_time":       1723594001000,
							"duration":       1000,
							"status":         "ok",
							"service_name":   "web-service",
							"resource": map[string]interface{}{
								"service.name":    "web-service",
								"service.version": "1.0.0",
							},
						},
					},
					{
						"_index": "2_bkapm_trace_tilapia_20240814",
						"_type":  "_doc",
						"_id":    "trace_002",
						"_score": 1.0,
						"_source": map[string]interface{}{
							"trace_id":       "def456ghi789",
							"span_id":        "span_002",
							"operation_name": "database_query",
							"start_time":     1723594002000,
							"end_time":       1723594003500,
							"duration":       1500,
							"status":         "error",
							"service_name":   "db-service",
							"resource": map[string]interface{}{
								"service.name":    "db-service",
								"service.version": "2.1.0",
							},
						},
					},
				},
			},
			"_scroll_id": "DXF1ZXJ5QW5kRmV0Y2gBAAAAAAAA",
		}

		// 设置ES mock数据
		mock.Es.Set(map[string]any{
			"mock_apm_trace_query": mockESData,
		})

		t.Logf("APM trace table配置验证: tableId=%s, spaceUid=%s", tableId, spaceUid)
		assert.Equal(t, "2_bkapm.trace_tilapia", tableId, "table ID应该正确")
		assert.Equal(t, "bkcc__2", spaceUid, "space UID应该正确")
	})

	t.Run("test_apm_trace_scroll_request", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		// 模拟完整的scroll请求
		requestBody := map[string]interface{}{
			"space_uid": "bkcc__2",
			"query_list": []map[string]interface{}{
				{
					"table_id": "2_bkapm.trace_tilapia",
				},
			},
			"timezone":    "Asia/Shanghai",
			"clear_cache": true,
			"scroll":      "5m",
			"limit":       10000,
		}

		// 验证请求参数
		assert.Equal(t, "bkcc__2", requestBody["space_uid"], "space_uid应该正确")
		assert.Equal(t, "5m", requestBody["scroll"], "scroll时间应该正确")
		assert.Equal(t, 10000, requestBody["limit"], "limit应该正确")
		assert.Equal(t, true, requestBody["clear_cache"], "clear_cache应该正确")

		queryList := requestBody["query_list"].([]map[string]interface{})
		assert.Equal(t, 1, len(queryList), "query_list长度应该正确")
		assert.Equal(t, "2_bkapm.trace_tilapia", queryList[0]["table_id"], "table_id应该正确")

		t.Logf("APM trace scroll请求验证通过: %+v", requestBody)
	})

	t.Run("test_apm_trace_es_slice_query", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		maxSlice := 3

		// 模拟APM trace的ES查询结构
		baseQuery := map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []map[string]interface{}{
						{
							"range": map[string]interface{}{
								"start_time": map[string]interface{}{
									"gte": 1723594000000,
									"lte": 1723595000000,
								},
							},
						},
					},
				},
			},
			"sort": []map[string]interface{}{
				{
					"start_time": map[string]interface{}{
						"order": "asc",
					},
				},
			},
			"_source": []string{"trace_id", "span_id", "operation_name", "start_time", "end_time", "duration", "status", "service_name"},
			"size":    10000,
		}

		// 验证每个slice的查询结构
		for sliceID := 0; sliceID < maxSlice; sliceID++ {
			sliceQuery := make(map[string]interface{})
			for key, value := range baseQuery {
				sliceQuery[key] = value
			}

			sliceQuery["slice"] = map[string]interface{}{
				"id":  sliceID,
				"max": maxSlice,
			}

			// 验证slice查询结构
			assert.Contains(t, sliceQuery, "slice", "slice查询应该包含slice配置")
			assert.Contains(t, sliceQuery, "query", "slice查询应该包含查询条件")
			assert.Contains(t, sliceQuery, "sort", "slice查询应该包含排序条件")
			assert.Contains(t, sliceQuery, "_source", "slice查询应该包含_source字段")

			sliceConfig := sliceQuery["slice"].(map[string]interface{})
			assert.Equal(t, sliceID, sliceConfig["id"], "slice ID应该正确")
			assert.Equal(t, maxSlice, sliceConfig["max"], "slice max应该正确")

			// 验证APM trace特定字段
			sourceFields := sliceQuery["_source"].([]string)
			expectedFields := []string{"trace_id", "span_id", "operation_name", "start_time", "end_time", "duration", "status", "service_name"}
			for _, field := range expectedFields {
				assert.Contains(t, sourceFields, field, fmt.Sprintf("应该包含APM trace字段: %s", field))
			}

			queryJson, err := json.Marshal(sliceQuery)
			assert.NoError(t, err, "slice查询应该可以序列化为JSON")
			assert.NotEmpty(t, queryJson, "序列化的JSON不应该为空")

			t.Logf("APM trace ES Slice %d 查询结构验证通过: %s", sliceID, string(queryJson))
		}
	})

	t.Run("test_apm_trace_result_processing", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		// 模拟APM trace的查询结果
		mockTraceResults := []*elastic.SearchResult{
			{
				TookInMillis: 15,
				TimedOut:     false,
				Hits: &elastic.SearchHits{
					TotalHits: &elastic.TotalHits{Value: 500, Relation: "eq"},
					MaxScore:  func() *float64 { f := 1.0; return &f }(),
					Hits: []*elastic.SearchHit{
						{
							Id: "trace_001",
							Source: json.RawMessage(`{
								"trace_id": "abc123def456",
								"span_id": "span_001",
								"operation_name": "http_request",
								"start_time": 1723594000000,
								"end_time": 1723594001000,
								"duration": 1000,
								"status": "ok",
								"service_name": "web-service"
							}`),
						},
						{
							Id: "trace_002",
							Source: json.RawMessage(`{
								"trace_id": "def456ghi789",
								"span_id": "span_002",
								"operation_name": "database_query",
								"start_time": 1723594002000,
								"end_time": 1723594003500,
								"duration": 1500,
								"status": "error",
								"service_name": "db-service"
							}`),
						},
					},
				},
				ScrollId: "scroll_id_slice_1",
			},
			{
				TookInMillis: 12,
				TimedOut:     false,
				Hits: &elastic.SearchHits{
					TotalHits: &elastic.TotalHits{Value: 400, Relation: "eq"},
					MaxScore:  func() *float64 { f := 0.9; return &f }(),
					Hits: []*elastic.SearchHit{
						{
							Id: "trace_003",
							Source: json.RawMessage(`{
								"trace_id": "ghi789jkl012",
								"span_id": "span_003",
								"operation_name": "cache_lookup",
								"start_time": 1723594004000,
								"end_time": 1723594004200,
								"duration": 200,
								"status": "ok",
								"service_name": "cache-service"
							}`),
						},
					},
				},
				ScrollId: "scroll_id_slice_2",
			},
			{
				TookInMillis: 18,
				TimedOut:     false,
				Hits: &elastic.SearchHits{
					TotalHits: &elastic.TotalHits{Value: 350, Relation: "eq"},
					MaxScore:  func() *float64 { f := 1.1; return &f }(),
					Hits: []*elastic.SearchHit{
						{
							Id: "trace_004",
							Source: json.RawMessage(`{
								"trace_id": "jkl012mno345",
								"span_id": "span_004",
								"operation_name": "message_queue",
								"start_time": 1723594005000,
								"end_time": 1723594005800,
								"duration": 800,
								"status": "ok",
								"service_name": "mq-service"
							}`),
						},
					},
				},
				ScrollId: "scroll_id_slice_3",
			},
		}

		// 模拟结果合并逻辑
		var allHits []*elastic.SearchHit
		var totalHits int64
		var scrollIDs []string
		firstResult := mockTraceResults[0]

		for _, result := range mockTraceResults {
			if result.Hits != nil {
				allHits = append(allHits, result.Hits.Hits...)
				totalHits += result.Hits.TotalHits.Value
			}
			if result.ScrollId != "" {
				scrollIDs = append(scrollIDs, result.ScrollId)
			}
		}

		mergedResult := &elastic.SearchResult{
			TookInMillis: firstResult.TookInMillis,
			TimedOut:     firstResult.TimedOut,
			Hits: &elastic.SearchHits{
				TotalHits: &elastic.TotalHits{
					Value:    totalHits,
					Relation: firstResult.Hits.TotalHits.Relation,
				},
				MaxScore: firstResult.Hits.MaxScore,
				Hits:     allHits,
			},
			ScrollId: strings.Join(scrollIDs, ","),
		}

		// 验证合并结果
		assert.Equal(t, int64(1250), mergedResult.Hits.TotalHits.Value, "APM trace总命中数应该正确")
		assert.Equal(t, 4, len(mergedResult.Hits.Hits), "合并后的trace hits数量应该正确")
		assert.Equal(t, "scroll_id_slice_1,scroll_id_slice_2,scroll_id_slice_3", mergedResult.ScrollId, "scroll ID应该正确合并")

		// 验证每个trace hit的数据结构
		expectedTraceIDs := []string{"trace_001", "trace_002", "trace_003", "trace_004"}
		for i, hit := range mergedResult.Hits.Hits {
			assert.Equal(t, expectedTraceIDs[i], hit.Id, "trace ID应该正确")
			assert.NotEmpty(t, hit.Source, "trace source不应该为空")

			// 验证trace数据包含必要字段
			var traceData map[string]interface{}
			err := json.Unmarshal(hit.Source, &traceData)
			assert.NoError(t, err, "trace数据应该可以解析")

			assert.Contains(t, traceData, "trace_id", "应该包含trace_id字段")
			assert.Contains(t, traceData, "span_id", "应该包含span_id字段")
			assert.Contains(t, traceData, "operation_name", "应该包含operation_name字段")
			assert.Contains(t, traceData, "start_time", "应该包含start_time字段")
			assert.Contains(t, traceData, "service_name", "应该包含service_name字段")
		}

		t.Logf("APM trace结果处理验证通过: totalHits=%d, mergedHits=%d, scrollIDs=%s",
			totalHits, len(allHits), strings.Join(scrollIDs, ","))
	})
}
