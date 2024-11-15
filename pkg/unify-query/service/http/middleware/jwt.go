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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	JwtHeaderKey  = "X-Bkapi-Jwt"
	ClaimsAppKey  = "app"
	ClaimsUserKey = "user"

	VerifiedKey = "verified"
)

var (
	errUnauthorized = errors.New("jwt auth token is unauthorized")
	errExpired      = errors.New("jwt auth: token is expired")
	errNBFInvalid   = errors.New("jwt auth: token nbf validation failed")
	errIATInvalid   = errors.New("jwt auth: token iat validation failed")
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
			switch {
			case verr.Errors&jwt.ValidationErrorExpired > 0:
				return nil, errExpired
			case verr.Errors&jwt.ValidationErrorIssuedAt > 0:
				return nil, errIATInvalid
			case verr.Errors&jwt.ValidationErrorNotValidYet > 0:
				return nil, errNBFInvalid
			}
		}
		return nil, err
	}

	if !token.Valid {
		return nil, errUnauthorized
	}

	return claims, nil
}

func parseData(verifiedMap map[string]any, key string, data map[string]any) {
	for k, v := range verifiedMap {
		if k != ClaimsAppKey && k != ClaimsUserKey && key == "" {
			continue
		}

		switch mv := v.(type) {
		case map[string]any:
			parseData(mv, k, data)
		default:
			if key != "" {
				k = fmt.Sprintf("%s.%s", key, k)
			}
			data[k] = v
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

			payLoad := metadata.GetJwtPayLoad(ctx)
			if err != nil {
				err = fmt.Errorf("unauthorized %s", err)
				log.Errorf(ctx, err.Error())

				metric.JWTRequestInc(ctx, c.ClientIP(), c.Request.URL.Path, payLoad.AppCode(), payLoad.UserName(), metric.StatusFailed)

				c.JSONP(http.StatusUnauthorized, gin.H{
					"trace_id": span.TraceID(),
					"error":    err.Error(),
				})
				c.Abort()
			} else {
				metric.JWTRequestInc(ctx, c.ClientIP(), c.Request.URL.Path, payLoad.AppCode(), payLoad.UserName(), metric.StatusSuccess)

				c.Next()
			}
		}()

		tokenString := c.Request.Header.Get(JwtHeaderKey)

		span.Set("jwt-public-key", publicKey)
		span.Set("jwt-token", tokenString)

		// 如果未配置 publicKey 以及未找到 jwtToken，则不启用 jwt 校验
		if publicKey == "" || tokenString == "" {
			return
		}

		claims, err := parseBKJWTToken(tokenString, []byte(publicKey))
		if err != nil {
			return
		}
		err = claims.Valid()
		if err != nil {
			return
		}

		span.Set("jwt-claims", claims)

		jwtPayLoad := make(metadata.JwtPayLoad)
		parseData(claims, "", jwtPayLoad)
		span.Set("jwt-payload", jwtPayLoad)

		metadata.SetJwtPayLoad(ctx, jwtPayLoad)
	}
}
