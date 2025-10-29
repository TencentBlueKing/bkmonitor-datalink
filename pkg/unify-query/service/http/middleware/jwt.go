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
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	JwtHeaderKey  = "X-Bkapi-Jwt"
	ClaimsAppKey  = "app"
	ClaimsUserKey = "user"
)

const (
	AppCodeKey  = "app.app_code"
	UserNameKey = "user.username"

	AuthAll = "*"
)

var (
	errTokenEmpty      = errors.New("token is empty")
	errUnauthorized    = errors.New("token is unauthorized")
	errExpired         = errors.New("token is expired")
	errNBFInvalid      = errors.New("token nbf validation failed")
	errIATInvalid      = errors.New("token iat validation failed")
	errAppUnauthorized = errors.New("bk_app_code is unauthorized in this space_uid")
	errSpaceUidEmpty   = errors.New("space_uid is empty")
)

type JwtPayLoad map[string]any

func (j JwtPayLoad) AppCode() string {
	if v, ok := j[AppCodeKey]; ok {
		if vs, ok := v.(string); ok {
			return vs
		}
	}
	return ""
}

func (j JwtPayLoad) UserName() string {
	if v, ok := j[UserNameKey]; ok {
		if vs, ok := v.(string); ok {
			return vs
		}
	}
	return ""
}

func parseBKJWTToken(tokenString string, publicKey []byte) (jwt.MapClaims, error) {
	keyFunc := func(token *jwt.Token) (any, error) {
		pubKey, err := jwt.ParseRSAPublicKeyFromPEM(publicKey)
		if err != nil {
			return pubKey, errors.Wrap(err, "jwt parse fail")
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
		if key != "" {
			k = fmt.Sprintf("%s.%s", key, k)
		}

		switch mv := v.(type) {
		case map[string]any:
			parseData(mv, k, data)
		default:
			data[k] = v
		}
	}
	return
}

func JwtAuthMiddleware(enabled bool, publicKey string, defaultAppCodeSpaces map[string][]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var (
			ctx  = c.Request.Context()
			user = metadata.GetUser(ctx)
			err  error

			payLoad = make(JwtPayLoad)

			appCode  string
			spaceUID = user.SpaceUID
		)

		ctx, span := trace.NewSpan(ctx, "jwt-auth")

		span.Set("enabled", enabled)

		// 通过配置判断是否开启验证，如果未开启验证则不进行 504 校验
		if !enabled {
			c.Next()
			return
		}

		defer func() {
			span.End(&err)

			if err != nil {
				metric.JWTRequestInc(ctx, c.Request.URL.Path, appCode, payLoad.UserName(), user.SpaceUID, metric.StatusFailed)
				res := gin.H{
					"error": metadata.Sprintf(
						metadata.MsgHandlerAPI,
						"jwt auth unauthorized (app_code: %s, space_uid: %s)",
						appCode, spaceUID,
					).Error(ctx, err).Error(),
				}
				if span.TraceID() != "" {
					res["trace_id"] = span.TraceID()
				}

				c.JSON(http.StatusUnauthorized, res)
				c.Abort()
			} else {
				metric.JWTRequestInc(ctx, c.Request.URL.Path, appCode, payLoad.UserName(), user.SpaceUID, metric.StatusSuccess)
				c.Next()
			}
		}()

		tokenString := c.Request.Header.Get(JwtHeaderKey)

		// 如果未传 jwtToken（兼容非 apigw 调用逻辑），则不启用 jwt 校验
		if tokenString == "" {
			err = errTokenEmpty
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

		span.Set("client-ip", c.ClientIP())
		span.Set("user-agent", c.Request.Header.Get("User-Agent"))

		span.Set("jwt-claims", claims)
		parseData(claims, "", payLoad)
		span.Set("jwt-payload", payLoad)

		appCode = payLoad.AppCode()
		span.Set("bk_app_code", appCode)

		span.Set("space_uid", spaceUID)

		router, err := influxdb.GetSpaceTsDbRouter()
		if err != nil {
			return
		}

		// 获取默认配置
		defaultSpaceUIDList := defaultAppCodeSpaces[appCode]
		spaceUIDs := set.New[string](defaultSpaceUIDList...)
		span.Set("default-space-uid-list", defaultSpaceUIDList)

		// 获取路由空间配置
		bkAppCodeSpaceUIDList := router.GetSpaceUIDList(ctx, appCode)

		if bkAppCodeSpaceUIDList != nil {
			spaceUIDs.Add(*bkAppCodeSpaceUIDList...)
			span.Set("bk-app-code-space-uid-list", bkAppCodeSpaceUIDList)
		}

		// 拼接后的最终有权限的空间列表
		span.Set("space-uid-set", spaceUIDs.String())

		// 如果配置了全局查询则，通过校验
		if spaceUIDs.Existed(AuthAll) {
			return
		}

		// 如果没有做全局查询，则 spaceUID 必须要存在
		if spaceUID == "" {
			err = errSpaceUidEmpty
			return
		}

		if !spaceUIDs.Existed(spaceUID) {
			err = errAppUnauthorized
			return
		}
	}
}
