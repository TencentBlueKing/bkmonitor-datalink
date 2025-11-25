// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package middleware

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var (
	ErrorOfCacheMiss = fmt.Errorf("CACHE_MISS")
)

type CacheConfig struct {
	Enable       bool
	TTL          time.Duration
	KeyPrefix    string
	SkipMethods  []string
	SkipPaths    []string
	IncludePaths []string
}

func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		Enable:       true,
		TTL:          5 * time.Minute,
		KeyPrefix:    "http_cache:",
		SkipMethods:  []string{"PUT", "DELETE", "PATCH"},
		SkipPaths:    []string{"/health", "/metrics", "/ping"},
		IncludePaths: []string{"/api/v1/", "/query/"},
	}
}

type CacheMiddleware struct {
	config          *CacheConfig
	cache           *cache.Service
	skipMethodsSet  map[string]bool
	skipPathsSet    map[string]bool
	includePathsSet map[string]bool
}

func NewCacheMiddleware(c *cache.Service, config *CacheConfig) *CacheMiddleware {
	if config == nil {
		config = DefaultCacheConfig()
	}

	skipMethodsSet := make(map[string]bool)
	for _, method := range config.SkipMethods {
		skipMethodsSet[strings.ToUpper(method)] = true
	}

	skipPathsSet := make(map[string]bool)
	for _, path := range config.SkipPaths {
		skipPathsSet[path] = true
	}

	includePathsSet := make(map[string]bool)
	for _, path := range config.IncludePaths {
		includePathsSet[path] = true
	}

	return &CacheMiddleware{
		config:          config,
		cache:           c,
		skipMethodsSet:  skipMethodsSet,
		skipPathsSet:    skipPathsSet,
		includePathsSet: includePathsSet,
	}
}

func (m *CacheMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Enable {
			c.Next()
			return
		}

		if m.skipMethod(c.Request.Method) {
			c.Next()
			return
		}

		if m.skipPath(c.Request.URL.Path) || !m.includePath(c.Request.URL.Path) {
			c.Next()
			return
		}

		cacheKey := m.generateCacheKey(c)

		if cachedResp := m.getCachedResponse(c, cacheKey); cachedResp != nil {
			m.serveCachedResponse(c, cachedResp)
			return
		}

		m.executeAndCacheSync(c, cacheKey)
	}
}

func (m *CacheMiddleware) skipMethod(method string) bool {
	return m.skipMethodsSet[strings.ToUpper(method)]
}

func (m *CacheMiddleware) skipPath(path string) bool {
	for _, skipPath := range m.config.SkipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

func (m *CacheMiddleware) includePath(path string) bool {
	if len(m.config.IncludePaths) == 0 {
		return true // 如果没有指定包含路径，则包含所有路径
	}

	for _, includePath := range m.config.IncludePaths {
		if strings.HasPrefix(path, includePath) {
			return true
		}
	}
	return false
}

// generateCacheKey 生成缓存键
func (m *CacheMiddleware) generateCacheKey(c *gin.Context) string {
	ctx := c.Request.Context()
	user := metadata.GetUser(ctx)

	keyData := map[string]interface{}{
		"method":   c.Request.Method,
		"path":     c.Request.URL.Path,
		"query":    c.Request.URL.RawQuery,
		"user_id":  "",
		"space_id": "",
	}

	if user != nil {
		keyData["user_id"] = user.Key
		keyData["space_id"] = user.SpaceUID
	}

	if c.Request.Method == "POST" && c.Request.Body != nil {
		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		keyData["body"] = string(bodyBytes)
	}

	keyJSON, _ := json.Marshal(keyData)
	keyHash := fmt.Sprintf("%x", md5.Sum(keyJSON))

	return m.config.KeyPrefix + keyHash
}

func (m *CacheMiddleware) getCachedResponse(c *gin.Context, cacheKey string) *CachedResponse {
	ctx := c.Request.Context()

	result, err := m.cache.Do(ctx, cacheKey, func() (interface{}, error) {
		return nil, ErrorOfCacheMiss
	})

	if err != nil && errors.Is(err, ErrorOfCacheMiss) {
		return nil
	}

	if err != nil {
		return nil
	}

	cachedResp, ok := result.(*CachedResponse)
	if !ok {
		return nil
	}

	return cachedResp
}

func (m *CacheMiddleware) serveCachedResponse(c *gin.Context, cachedResp *CachedResponse) {
	for key, values := range cachedResp.Headers {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Status(cachedResp.StatusCode)

	c.Writer.Write(cachedResp.Body)

	log.Debugf(c.Request.Context(), "cache hit for key: %s", cachedResp.CacheKey)
}

func (m *CacheMiddleware) executeAndCacheSync(c *gin.Context, cacheKey string) {
	writer := &responseWriter{
		ResponseWriter: c.Writer,
		buffer:         bytes.NewBuffer(nil),
	}

	c.Writer = writer

	c.Next()

	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		cachedResp := &CachedResponse{
			CacheKey:   cacheKey,
			StatusCode: c.Writer.Status(),
			Headers:    make(map[string][]string),
			Body:       writer.buffer.Bytes(),
		}

		for key, values := range c.Writer.Header() {
			cachedResp.Headers[key] = values
		}

		go func() {
			ctx := c.Request.Context()
			_, err := m.cache.Do(ctx, cacheKey, func() (interface{}, error) {
				return cachedResp, nil
			})

			if err != nil {
				log.Errorf(ctx, "failed to cache response for key %s: %v", cacheKey, err)
			}
		}()

		log.Debugf(c.Request.Context(), "cached response for key: %s", cacheKey)
	}
}

type CachedResponse struct {
	CacheKey   string              `json:"cache_key"`
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
}

type responseWriter struct {
	gin.ResponseWriter
	buffer *bytes.Buffer
}

func (w *responseWriter) Write(data []byte) (int, error) {
	w.buffer.Write(data)
	return w.ResponseWriter.Write(data)
}
