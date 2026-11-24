// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package internaltoken 实现 Kingeye Internal-Token 协议；仅认证调用身份，不授予租户权限。
package internaltoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// HeaderName 是 Kingeye 内部调用的认证头，不复用 Authorization。
const HeaderName = "Internal-Token"

// Lifetime 是本地签发 Token 的有效期；接收端仍兼容 Kingeye 无 exp 的 Token。
const Lifetime = 5 * time.Minute

// MaxHeaderBytes 限制解析前的认证头大小，避免无界解析外部输入。
const MaxHeaderBytes = 8 << 10

// ErrInvalidToken 表示认证失败；不暴露 Token、密钥或不可信载荷。
var ErrInvalidToken = errors.New("invalid internal token")

// Toolkit 使用显式密钥和时钟签发、验证内部身份，可由多个请求并发使用。
// 时钟函数必须支持并发；Toolkit 不保存请求 Context，也不追踪 Token 使用次数。
type Toolkit struct {
	key []byte
	now func() time.Time
}

// New 创建认证工具；now 为 nil 时使用系统时钟，密钥不得为空或仅空白。
func New(secret string, now func() time.Time) (*Toolkit, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("internal JWT secret key is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Toolkit{key: []byte(secret), now: now}, nil
}

// Sign 返回带 Bearer 前缀的请求头值；username 是调用身份，不替代业务操作人。
// 每次调用重新计算五分钟有效期；Token 在有效期内可重用，不提供防重放保证。
func (t *Toolkit) Sign(username string) (string, error) {
	if strings.TrimSpace(username) == "" {
		return "", ErrInvalidToken
	}
	now := t.now().Unix()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": username, "iat": now, "exp": now + int64(Lifetime/time.Second),
	})
	raw, err := token.SignedString(t.key)
	if err != nil {
		return "", ErrInvalidToken
	}
	value := "Bearer " + raw
	if len(value) > MaxHeaderBytes {
		return "", ErrInvalidToken
	}
	return value, nil
}

type claims struct{ jwt.MapClaims }

func (c *claims) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	// 保留声明的数值类型；MapClaims 的 float64 路径会把 exp=0 当作未提供，
	// 因而必须使用 json.Number，确保 Unix epoch 的过期时间也被拒绝。
	decoder.UseNumber()
	return decoder.Decode(&c.MapClaims)
}

func (c claims) Validate() error {
	username, ok := c.MapClaims["username"].(string)
	if !ok || strings.TrimSpace(username) == "" {
		return ErrInvalidToken
	}
	for _, name := range []string{"exp", "nbf", "iat"} {
		value, exists := c.MapClaims[name]
		if !exists {
			continue
		}
		number, ok := value.(json.Number)
		if !ok {
			return ErrInvalidToken
		}
		seconds, err := number.Float64()
		if err != nil || math.IsInf(seconds, 0) || seconds >= math.MaxInt64 || seconds < math.MinInt64 {
			return ErrInvalidToken
		}
	}
	return nil
}

// VerifyHeader 只接受单个规范 Bearer 头；存在的时间声明必须有效，不增加时钟宽限。
// 不要求 exp 是为兼容 Kingeye 当前签发器，不代表 Token 不可重放。
func (t *Toolkit) VerifyHeader(header http.Header) (string, error) {
	values := header.Values(HeaderName)
	if len(values) != 1 || len(values[0]) > MaxHeaderBytes || !strings.HasPrefix(values[0], "Bearer ") {
		return "", ErrInvalidToken
	}
	raw := strings.TrimPrefix(values[0], "Bearer ")
	if raw == "" || strings.ContainsAny(raw, " ,\t\r\n") {
		return "", ErrInvalidToken
	}
	c := &claims{MapClaims: jwt.MapClaims{}}
	token, err := jwt.ParseWithClaims(raw, c, func(_ *jwt.Token) (any, error) { return t.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuedAt(), jwt.WithTimeFunc(t.now), jwt.WithStrictDecoding())
	if err != nil || !token.Valid {
		return "", ErrInvalidToken
	}
	return c.MapClaims["username"].(string), nil
}

type usernameKey struct{}

// WithUsername 记录已经验证的调用身份；不得传入未经验证的 JWT 声明。
func WithUsername(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, usernameKey{}, username)
}

// Username 返回上下文中的认证用户名；无内部身份时返回空字符串。
func Username(ctx context.Context) string {
	username, _ := ctx.Value(usernameKey{}).(string)
	return username
}
