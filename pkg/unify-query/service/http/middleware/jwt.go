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
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	JwtHeaderKey  = "X-Bkapi-Jwt"
	ClaimsAppKey  = "app"
	ClaimsUserKey = "user"

	VerifiedKey = "verified"
)

var (
	errUnauthorized = "jwt auth token is unauthorized"
	errFormat       = "format is error"
	errVerified     = "verified is error"
)

func parseBKJWTToken(tokenString string, publicKey []byte) (jwt.MapClaims, error) {
	keyFunc := func(token *jwt.Token) (interface{}, error) {
		pubKey, err := jwt.ParseRSAPublicKeyFromPEM(publicKey)
		if err != nil {
			return pubKey, fmt.Errorf("jwt parse fail, err=%w", err)
		}
		return pubKey, nil
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, keyFunc)
	if err != nil {
		var verr *jwt.ValidationError
		if errors.As(err, &verr) {
			return nil, verr.Inner
		}
		return nil, err
	}

	if !token.Valid {
		return nil, errors.New(errUnauthorized)
	}

	return claims, nil
}

func parseData(verifiedMap map[string]any, key string, data map[string]string) (err error) {
	if data == nil {
		data = make(map[string]string)
	}

	for k, v := range verifiedMap {
		if k == VerifiedKey {
			if verified, ok := v.(bool); !ok {
				err = fmt.Errorf("%s %s, %T", k, errFormat, v)
				return
			} else {
				if !verified {
					err = fmt.Errorf("%s, %v", errVerified, v)
					return
				}
			}
		} else {
			if d, ok := v.(string); !ok {
				err = fmt.Errorf("%s %s, %T", k, errFormat, v)
				return
			} else {
				k = fmt.Sprintf("%s.%s", key, k)
				data[k] = d
			}
		}
	}
	return
}

func JwtAuthMiddleware(publicKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var (
			ctx = c.Request.Context()
			err error
		)
		ctx, span := trace.NewSpan(ctx, "jwt-auth")
		defer func() {
			span.End(&err)

			if err != nil {
				c.JSONP(http.StatusUnauthorized, gin.H{"error": "Unauthorized: " + err.Error()})
				c.Abort()
			} else {
				c.Next()
			}
		}()

		tokenString := c.Request.Header.Get(JwtHeaderKey)

		span.Set("jwt-public-key", publicKey)
		span.Set("jwt-token", tokenString)

		// 如果未配置 publicKey，则不启用 jwt 校验
		if publicKey == "" {
			return
		}

		claims, err := parseBKJWTToken(tokenString, []byte(publicKey))
		if err == nil {
			err = claims.Valid()
		}

		span.Set("jwt-claims", claims)

		jwtPayLoad := make(metadata.JwtPayLoad)
		for k, v := range claims {
			switch k {
			case ClaimsAppKey, ClaimsUserKey:
				if d, ok := v.(map[string]any); ok {
					err = parseData(d, k, jwtPayLoad)
					if err != nil {
						return
					}
				}
			}
		}
		span.Set("jwt-payload", jwtPayLoad)

		metadata.SetJwtPayLoad(ctx, jwtPayLoad)
	}
}
