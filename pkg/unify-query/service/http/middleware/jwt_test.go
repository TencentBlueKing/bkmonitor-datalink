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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// generateToken 生成 bk token
func generateToken(data map[string]any, privateKey []byte) (string, error) {
	// 设置token有效时间
	nowTime := time.Now().Add(-1 * time.Hour)
	expireTime := nowTime.Add(2 * time.Hour)

	claims := jwt.MapClaims{
		"exp": expireTime.Unix(),
		"nbf": nowTime.Unix(),
	}
	for k, v := range data {
		claims[k] = v
	}

	tokenClaims := jwt.NewWithClaims(jwt.SigningMethodRS512, claims)
	// 该方法内部生成签名字符串，再用于获取完整、已签名的token

	priKey, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey)
	if err != nil {
		return "", err
	}

	token, err := tokenClaims.SignedString(priKey)
	return token, err
}

func handler(c *gin.Context) {
	c.Status(http.StatusOK)
}

func mockRSAKey() (string, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	var (
		privateBuffer bytes.Buffer
		publicBuffer  bytes.Buffer
	)

	err = pem.Encode(&privateBuffer, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err != nil {
		return "", "", err
	}

	b, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", err
	}
	err = pem.Encode(&publicBuffer, &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: b,
	})
	if err != nil {
		return "", "", err
	}

	return publicBuffer.String(), privateBuffer.String(), nil
}

func TestJwtAuthMiddleware(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	r := gin.Default()

	testPublicKey, testPrivateKey, err := mockRSAKey()
	assert.NoError(t, err)

	r.Use(MetaData(nil), JwtAuthMiddleware(testPublicKey, map[string][]string{"my_code": {"my_space_uid"}}))

	url := "/protected"
	r.GET(url, handler)

	testCases := map[string]struct {
		appCode  string
		spaceUID string
		tenantID string
		userName string

		status   int
		expected string
	}{
		"默认 appcode 可以访问所有空间": {
			appCode:  influxdb.BkAppCode,
			spaceUID: "test_1",
			status:   http.StatusOK,
		},
		"空间如果为空": {
			appCode:  "my_code",
			status:   http.StatusUnauthorized,
			expected: `{"error":"jwt auth unauthorized: bk_app_code is unauthorized in this space_uid, app_code: my_code, space_uid: "}`,
		},
		"访问无权限的空间授权 - 1": {
			appCode:  "my_code",
			spaceUID: "other_space_uid",
			status:   http.StatusUnauthorized,
			expected: `{"error":"jwt auth unauthorized: bk_app_code is unauthorized in this space_uid, app_code: my_code, space_uid: other_space_uid"}`,
		},
		"访问无权限的空间授权 - 2": {
			appCode:  "my_code_1",
			spaceUID: "my_space_uid",
			status:   http.StatusUnauthorized,
			expected: `{"error":"jwt auth unauthorized: bk_app_code is unauthorized in this space_uid, app_code: my_code_1, space_uid: my_space_uid"}`,
		},
		"访问有权限的空间授权": {
			appCode:  "my_code",
			spaceUID: "my_space_uid",
			status:   http.StatusOK,
		},
		"没有传 token": {
			spaceUID: "other_space_uid",
			status:   http.StatusOK,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			jwtPayLoad := map[string]any{
				ClaimsAppKey: map[string]any{
					"verified":  true,
					"app_code":  c.appCode,
					"tenant_id": c.tenantID,
				},
				ClaimsUserKey: map[string]any{
					"verified": true,
					"username": c.userName,
				},
			}

			// 解析 jwt
			reqWithToken, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

			if c.spaceUID != "" {
				reqWithToken.Header.Set(metadata.SpaceUIDHeader, c.spaceUID)
			}

			if c.appCode != "" {
				s, err := generateToken(jwtPayLoad, []byte(testPrivateKey))
				assert.NoError(t, err)

				reqWithToken.Header.Set(JwtHeaderKey, s)
			}

			wWithToken := httptest.NewRecorder()
			r.ServeHTTP(wWithToken, reqWithToken)
			assert.Equal(t, c.status, wWithToken.Code)

			res, _ := io.ReadAll(wWithToken.Body)
			assert.Equal(t, c.expected, string(res))
		})
	}
}
