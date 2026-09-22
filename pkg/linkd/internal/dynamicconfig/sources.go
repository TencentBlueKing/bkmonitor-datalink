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
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/redisclient"
)

// OpenSource 只构造指定来源的连接池；首次网络访问发生在 Read/Watch，失败不阻塞进程装配。
func OpenSource(c config.DynamicSourceConfig, binding config.DynamicBinding) (Source, error) {
	c = c.WithDefaults()
	validation := config.DynamicConfigConfig{Enabled: true, Sources: map[string]config.DynamicSourceConfig{binding.Source: c}, Bindings: config.DynamicBindings{Severity: &binding}}
	if err := validation.Validate(); err != nil {
		return nil, err
	}
	switch c.Type {
	case config.DynamicSourceAlarmLevel:
		x := driver.NewConfig()
		x.Net = "tcp"
		x.Addr = c.MySQL.Address
		x.DBName = c.MySQL.Database
		x.User = c.MySQL.Username
		x.Passwd = c.MySQL.Password
		x.Timeout = c.Timeout()
		x.ReadTimeout = c.Timeout()
		x.WriteTimeout = c.Timeout()
		db, err := sql.Open("mysql", x.FormatDSN())
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(30 * time.Minute)
		return &alarmLevelSource{db: db, table: c.Table, tenant: c.BKTenantID}, nil
	case config.DynamicSourceRedis:
		o := c.Redis.ClientOptions()
		o.ContextTimeoutEnabled = true
		client, err := redisclient.New(o)
		if err != nil {
			return nil, err
		}
		return &redisSource{client: client, namespace: c.RedisKeyPrefix + "dynamic_config:", tenant: c.BKTenantID, key: binding.Key}, nil
	default:
		return nil, fmt.Errorf("unknown dynamic config source")
	}
}

type alarmLevelSource struct {
	db            *sql.DB
	table, tenant string
}

func (s *alarmLevelSource) Read(ctx context.Context) (json.RawMessage, error) {
	// table 已由配置限定为普通标识符；业务租户始终绑定参数。完整 SELECT 同时观察
	// 新增、删除和 bulk_update 的排序变化，不能依赖可能不更新的 updated_at。
	rows, err := s.db.QueryContext(ctx, "SELECT name, priority FROM `"+s.table+"` WHERE bk_tenant_id = ? ORDER BY priority, name LIMIT 257", s.tenant) //nolint:gosec // G202: 表名经标识符白名单验证，租户使用绑定参数。
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	levels := make([]config.SeverityLevel, 0)
	for rows.Next() {
		var level config.SeverityLevel
		if err = rows.Scan(&level.Name, &level.Priority); err != nil {
			return nil, err
		}
		levels = append(levels, level)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(levels)
}

func (s *alarmLevelSource) Close() error { return s.db.Close() }

type redisSource struct {
	client                 *redis.Client
	namespace, tenant, key string
}

func (s *redisSource) valueKey() string {
	// QueryEscape 后替换 '+'，与 Python quote(tenant, safe="") 的 UTF-8 编码一致。
	return s.namespace + "{" + strings.ReplaceAll(url.QueryEscape(s.tenant), "+", "%20") + "}:" + s.key
}

func (s *redisSource) Read(ctx context.Context) (json.RawMessage, error) {
	p := s.client.TxPipeline()
	revision := p.GetRange(ctx, s.namespace+"revision", 0, 255)
	// GETRANGE 在服务端限制返回体；缺 key 返回空字符串，JSON null 仍为非空值。
	value := p.GetRange(ctx, s.valueKey(), 0, 1<<20)
	if _, err := p.Exec(ctx); err != nil {
		return nil, err
	}
	if revision.Val() == "" || len(revision.Val()) >= 256 {
		return nil, fmt.Errorf("dynamicconfig revision is unavailable")
	}
	if value.Val() == "" {
		return nil, fmt.Errorf("dynamicconfig field has no override")
	}
	if len(value.Val()) > 1<<20 {
		return nil, fmt.Errorf("dynamicconfig field exceeds 1 MiB")
	}
	return json.RawMessage(value.Val()), nil
}

func (s *redisSource) Watch(ctx context.Context, notify func()) error {
	for ctx.Err() == nil {
		pubsub := s.client.Subscribe(ctx, s.namespace+"events")
		for ctx.Err() == nil {
			message, err := pubsub.ReceiveTimeout(ctx, time.Second)
			if err != nil {
				var timeout net.Error
				if errors.As(err, &timeout) && timeout.Timeout() && ctx.Err() == nil {
					continue
				}
				break
			}
			switch v := message.(type) {
			case *redis.Subscription:
				if v.Kind == "subscribe" {
					notify()
				}
			case *redis.Message:
				if v.Channel != s.namespace+"events" || len(v.Payload) > 1<<20 {
					continue
				}
				var event struct {
					Protocol int    `json:"protocol_version"`
					Type     string `json:"type"`
					Tenant   string `json:"bk_tenant_id"`
				}
				if json.Unmarshal([]byte(v.Payload), &event) == nil && event.Protocol == 1 && event.Type == "dynamic_config.changed" && event.Tenant == s.tenant {
					notify()
				}
			}
		}
		_ = pubsub.Close()
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func (s *redisSource) Close() error { return s.client.Close() }
