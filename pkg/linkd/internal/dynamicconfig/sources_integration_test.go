// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dynamicconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
)

func TestRedisSourceIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS to run Redis integration")
	}
	database := 0
	if raw := os.Getenv("LINKD_TEST_REDIS_DATABASE"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
		database = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	prefix := "linkd-dynamic-test:" + strconv.FormatInt(time.Now().UnixNano(), 10) + ":"
	rc := &config.RedisConfig{Address: address, Database: database, Username: os.Getenv("LINKD_TEST_REDIS_USERNAME"), Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD")}
	settings := config.DynamicSourceConfig{Type: config.DynamicSourceRedis, Redis: rc, RedisKeyPrefix: prefix, BKTenantID: "tenant/a +中文"}
	source, err := OpenSource(settings, config.DynamicBinding{Source: "redis", Key: "levels"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	s := source.(*redisSource)
	options := redis.Options{Addr: rc.Address, DB: rc.Database, Username: rc.Username, Password: rc.Password, ClientName: prefix, ContextTimeoutEnabled: true}
	if err = s.client.Close(); err != nil {
		t.Fatal(err)
	}
	s.client = redis.NewClient(&options)
	if s.valueKey() != prefix+"dynamic_config:{tenant%2Fa%20%2B%E4%B8%AD%E6%96%87}:levels" {
		t.Fatalf("incorrect tenant escaping: %s", s.valueKey())
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = s.client.Del(cleanup, s.valueKey(), s.namespace+"revision").Err()
	}()
	if _, err = s.Read(ctx); err == nil {
		t.Fatal("unpublished revision accepted")
	}
	if err = s.client.Set(ctx, s.namespace+"revision", "initial", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(ctx); err == nil {
		t.Fatal("missing field accepted")
	}
	if err = s.client.Set(ctx, s.valueKey(), initialLevels, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if raw, err := s.Read(ctx); err != nil || string(raw) != initialLevels {
		t.Fatalf("read failed: %v", err)
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	done := make(chan error, 1)
	notified := make(chan struct{}, 8)
	go func() {
		done <- s.Watch(watchCtx, func() {
			select {
			case notified <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-notified:
	case <-ctx.Done():
		t.Fatal("subscription did not trigger catch-up")
	}
	// 中断订阅连接，确认重连再次触发补读，而不是依赖可能丢失的 Pub/Sub 历史。
	clients, err := s.client.ClientList(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	ownedID := ""
	for _, line := range strings.Split(clients, "\n") {
		fields := map[string]string{}
		for _, part := range strings.Fields(line) {
			key, value, _ := strings.Cut(part, "=")
			fields[key] = value
		}
		if fields["name"] == prefix && fields["cmd"] == "subscribe" {
			ownedID = fields["id"]
		}
	}
	if ownedID == "" {
		t.Fatal("test subscription not found")
	}
	if err = s.client.Do(ctx, "CLIENT", "KILL", "ID", ownedID).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notified:
	case <-ctx.Done():
		t.Fatal("reconnect did not catch up")
	}
	event, _ := json.Marshal(map[string]any{"protocol_version": 1, "type": "dynamic_config.changed", "bk_tenant_id": settings.BKTenantID})
	_, err = s.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, s.namespace+"revision", "next", 0)
		p.Set(ctx, s.valueKey(), `[{"name":"custom","priority":0}]`, 0)
		p.Publish(ctx, s.namespace+"events", event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-notified:
	case <-ctx.Done():
		t.Fatal("change did not notify")
	}
	raw, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `[{"name":"custom","priority":0}]` {
		t.Fatal("stale value after notification")
	}
	stopWatch()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not cancel")
	}
}

func TestAlarmLevelSourceIntegration(t *testing.T) {
	dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LINKD_TEST_MYSQL_DSN to run MySQL integration")
	}
	c, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test MySQL DSN")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("open test MySQL failed")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	table := "linkd_test_levels_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+" (bk_tenant_id VARCHAR(256),name VARCHAR(32),priority INT)"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_, _ = db.ExecContext(cleanup, "DROP TABLE "+table)
	}()
	//nolint:gosec // G202: 表名仅由固定前缀与十进制时间生成，值为测试常量。
	if _, err = db.ExecContext(ctx, "INSERT INTO "+table+" VALUES ('system','warning',1),('other','fatal',0)"); err != nil {
		t.Fatal(err)
	}
	source, err := OpenSource(config.DynamicSourceConfig{Type: config.DynamicSourceAlarmLevel, MySQL: &config.MySQLConfig{Address: c.Addr, Database: c.DBName, Username: c.User, Password: c.Passwd}, Table: table}, config.DynamicBinding{Source: "levels"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	raw, err := source.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var levels []config.SeverityLevel
	if err = json.Unmarshal(raw, &levels); err != nil {
		t.Fatal(err)
	}
	if len(levels) != 1 || levels[0].Name != "warning" {
		t.Fatal("tenant isolation failed")
	}
	//nolint:gosec // G202: 表名仅由固定前缀与十进制时间生成，值为测试常量。
	if _, err = db.ExecContext(ctx, "UPDATE "+table+" SET name='custom',priority=4 WHERE bk_tenant_id='system'"); err != nil {
		t.Fatal(err)
	}
	raw, err = source.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &levels); err != nil || len(levels) != 1 || levels[0].Name != "custom" || levels[0].Priority != 4 {
		t.Fatal("full polling did not observe update")
	}
}
