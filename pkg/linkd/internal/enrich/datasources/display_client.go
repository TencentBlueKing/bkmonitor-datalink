// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/enrich"
	"linkd/internal/jsonpath"
)

// DisplayCache 限定展示转换只能执行缓存读取命令。
type DisplayCache interface {
	HStrLen(context.Context, string, string) *redis.IntCmd
	HGet(context.Context, string, string) *redis.StringCmd
	GetRange(context.Context, string, int64, int64) *redis.StringCmd
}

// DisplayClient 按显式租户只读 Kingeye 展示缓存，不写回或刷新缓存。
type DisplayClient struct {
	redis  DisplayCache
	models enrich.ModelReader
	prefix string
}

// NewDisplayClient 将展示缓存与模型读取隔离在 OneModel SDK 之外。
func NewDisplayClient(client DisplayCache, models enrich.ModelReader, prefix string) *DisplayClient {
	return &DisplayClient{client, models, prefix}
}

// Format 逐个转换多实例值；合并由规则中的 join 显式决定。
func (c *DisplayClient) Format(ctx context.Context, tenant, model, field string, value any) (any, error) {
	if tenant == "" || c.redis == nil || c.models == nil {
		return nil, fmt.Errorf("display reader requires tenant and dependencies")
	}
	definition, found, err := c.models.GetModelByCode(ctx, tenant, model)
	if err != nil {
		return nil, err
	}
	if !found || definition.TenantID != tenant || definition.ModelCode != model {
		return nil, fmt.Errorf("display model not found")
	}
	object, ok := definition.Fields["bk_cmdb_obj_id"].(string)
	if !ok || object == "" {
		return value, nil
	}
	raw, err := c.hash(ctx, tenant, "cmdb_object_attribute_cache_key", object)
	if err != nil {
		return nil, err
	}
	metadata := struct {
		Enum         map[string]map[string]any `json:"enum"`
		Num          map[string]string         `json:"num"`
		Time         []string                  `json:"time"`
		User         []string                  `json:"objuser"`
		Organization []string                  `json:"organization"`
	}{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			return nil, fmt.Errorf("invalid display metadata")
		}
	}
	one := func(v any) (any, error) {
		if v == nil {
			return nil, nil
		}
		text, err := displayScalar(v)
		if err != nil {
			return nil, err
		}
		if options, ok := metadata.Enum[field]; ok {
			if mapped, ok := options[text]; ok {
				return mapped, nil
			}
			return v, nil
		}
		if unit, ok := metadata.Num[field]; ok {
			return text + unit, nil
		}
		if slices.Contains(metadata.Time, field) {
			return strings.Split(strings.Split(strings.ReplaceAll(text, "T", " "), "+")[0], ".")[0], nil
		}
		if slices.Contains(metadata.User, field) {
			users := strings.Split(text, ",")
			if len(users) > 64 {
				return nil, fmt.Errorf("display user limit exceeded")
			}
			for i, user := range users {
				raw, err := c.hash(ctx, tenant, "sync_organization_all_staff", user)
				if err != nil {
					return nil, err
				}
				if raw == "" {
					continue
				}
				var info struct {
					Username    string `json:"username"`
					DisplayName string `json:"display_name"`
				}
				if err := json.Unmarshal([]byte(raw), &info); err != nil || info.Username != user {
					return nil, fmt.Errorf("invalid user display identity")
				}
				if info.DisplayName != "" {
					users[i] = user + "(" + info.DisplayName + ")"
				}
			}
			return strings.Join(users, ";"), nil
		}
		if slices.Contains(metadata.Organization, field) {
			raw, err := c.get(ctx, tenant, "sync_organization_all_department")
			if err != nil {
				return nil, err
			}
			if raw == "" {
				return v, nil
			}
			var departments []struct {
				ID       any    `json:"id"`
				FullName string `json:"full_name"`
			}
			if err := json.Unmarshal([]byte(raw), &departments); err != nil {
				return nil, fmt.Errorf("invalid department cache")
			}
			for _, d := range departments {
				id, err := displayScalar(d.ID)
				if err == nil && id == text {
					parts := strings.Split(d.FullName, "/")
					if len(parts) > 1 {
						return strings.Join(parts[1:], "/"), nil
					}
					return d.FullName, nil
				}
			}
			return v, nil
		}
		if field == "bk_cloud_id" {
			raw, err := c.get(ctx, tenant, "cmdb_cloud_display_cache_key")
			if err != nil {
				return nil, err
			}
			if raw == "" {
				return v, nil
			}
			var clouds struct {
				KV map[string]string `json:"kv"`
			}
			if err := json.Unmarshal([]byte(raw), &clouds); err != nil {
				return nil, fmt.Errorf("invalid cloud cache")
			}
			if name := clouds.KV[text]; name != "" {
				return name + "[" + text + "]", nil
			}
		}
		return v, nil
	}
	if values, ok := value.([]any); ok {
		if len(values) > 64 {
			return nil, fmt.Errorf("display value limit exceeded")
		}
		out := make([]any, len(values))
		for i, v := range values {
			out[i], err = one(v)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return one(value)
}

func displayScalar(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool, json.Number, float64, int, int64:
		return fmt.Sprint(v), nil
	default:
		return "", fmt.Errorf("display requires scalar")
	}
}

func (c *DisplayClient) hash(ctx context.Context, tenant, base, field string) (string, error) {
	key := c.prefix + base + ":" + tenant
	length, err := c.redis.HStrLen(ctx, key, field).Result()
	if err != nil {
		return "", err
	}
	if length > 1<<20 {
		return "", fmt.Errorf("display cache too large")
	}
	value, err := c.redis.HGet(ctx, key, field).Result()
	return checkedDisplay(value, err)
}

func (c *DisplayClient) get(ctx context.Context, tenant, base string) (string, error) {
	value, err := c.redis.GetRange(ctx, c.prefix+base+":"+tenant, 0, 1<<20).Result()
	return checkedDisplay(value, err)
}

func checkedDisplay(value string, err error) (string, error) {
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(value) > 1<<20 {
		return "", fmt.Errorf("display cache too large")
	}
	if value != "" {
		var v any
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			return "", fmt.Errorf("invalid display cache JSON")
		}
		if err := jsonpath.ValidateTree(v); err != nil {
			return "", err
		}
	}
	return value, nil
}
