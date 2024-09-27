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
)

const (
	JwtHeaderKey = "X-Bkapi-Jwt"
)

var (
	ErrUnauthorized = errors.New("jwtauth: token is unauthorized")

	ErrExpired    = errors.New("jwtauth: token is expired")
	ErrNBFInvalid = errors.New("jwtauth: token nbf validation failed")
	ErrIATInvalid = errors.New("jwtauth: token iat validation failed")
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
		if verr, ok := err.(*jwt.ValidationError); ok {
			switch {
			case verr.Errors&jwt.ValidationErrorExpired > 0:
				return nil, ErrExpired
			case verr.Errors&jwt.ValidationErrorIssuedAt > 0:
				return nil, ErrIATInvalid
			case verr.Errors&jwt.ValidationErrorNotValidYet > 0:
				return nil, ErrNBFInvalid
			}
		}
		return nil, err
	}

	if !token.Valid {
		return nil, ErrUnauthorized
	}

	return claims, nil
}

func JwtAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.Request.Header.Get(JwtHeaderKey)
		claims, err := parseBKJWTToken(tokenString, []byte(`-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAtDZbyWlivLSzpiAy48Qz
YC+15oejBQBP8r9vdwuBfkDO1TrqpNeCYoee0okYt4wrjX437v15Qpq3us3DWZkz
3VWrEm+weyExump5uvEU4Dm5uvBQFS5N9klzWUZK/DGcXITBXYxNMabVVDX2A3OO
8Yac/T66RtAaqUFPbRIR2r+LivapIrDrTHt0o4eUbKXjU0fz58Wxev+O5B7n0Apg
+9Tg5MmGhcAYZh0A37wCma/bhbDMLypAOm5mUyd50kjcMBQCz3YGO8OTHkElGkrW
BqJ86TDMB7fQ4SVi0zs6qbrHbePpcSZ8paGUZQNZHaMW58YI7rzrkRDeuFNj4PUG
RQIDAQAB
-----END PUBLIC KEY-----`))
		if err == nil {
			err = claims.Valid()
		}

		if err != nil {
			c.JSONP(http.StatusUnauthorized, gin.H{"error": "Unauthorized: " + err.Error()})
			c.Abort()
			return
		}

		c.Set("bk_app_code", "")
		c.Next()
	}
}
