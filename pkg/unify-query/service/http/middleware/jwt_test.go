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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// generateToken 生成 bk token
func generateToken(data map[string]any, privateKey []byte) (string, error) {
	//设置token有效时间
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
	//该方法内部生成签名字符串，再用于获取完整、已签名的token

	priKey, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey)
	if err != nil {
		return "", err
	}

	token, err := tokenClaims.SignedString(priKey)
	return token, err
}

func handler(c *gin.Context) {
	ctx := c.Request.Context()
	jwtPayLoad := metadata.GetJwtPayLoad(ctx)

	for k := range jwtPayLoad {
		if k == ClaimsExp || k == ClaimsNbf {
			delete(jwtPayLoad, k)
		}
	}

	c.JSON(http.StatusOK, jwtPayLoad)
	return
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
	r := gin.Default()

	testPublicKey, testPrivateKey, err := mockRSAKey()
	assert.NoError(t, err)

	r.Use(JwtAuthMiddleware(testPublicKey))

	url := "/protected"

	r.GET(url, handler)

	userName := "tim"
	appCode := "test-code-1"

	expected := `{"app.app_code":"test-code-1","app.tenant_id":"mo_0904","app.verified":true,"user.username":"tim","user.verified":true}`

	jwtPayLoad := map[string]any{
		ClaimsAppKey: map[string]any{
			"verified":  true,
			"app_code":  appCode,
			"tenant_id": "mo_0904",
		},
		ClaimsUserKey: map[string]any{
			"verified": true,
			"username": userName,
		},
	}

	// 解析 jwt
	s, err := generateToken(jwtPayLoad, []byte(testPrivateKey))
	assert.NoError(t, err)

	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	reqWithToken, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	reqWithToken.Header.Set(JwtHeaderKey, s)

	wWithToken := httptest.NewRecorder()
	r.ServeHTTP(wWithToken, reqWithToken)
	assert.Equal(t, http.StatusOK, wWithToken.Code)

	var payLoadByte []byte
	payLoadByte, _ = io.ReadAll(wWithToken.Body)
	assert.Equal(t, expected, string(payLoadByte))

	payLoad := metadata.GetJwtPayLoad(ctx)
	assert.Equal(t, appCode, payLoad.AppCode())
	assert.Equal(t, userName, payLoad.UserName())

	// 测试不传 jwtHeader 的请求，直接返回正常
	ctx = metadata.InitHashID(ctx)
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	payLoadByte, _ = io.ReadAll(wWithToken.Body)
	assert.Equal(t, ``, string(payLoadByte))

}
