// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
)

func sha256hex(s string) string {
	b := sha256.Sum256([]byte(s))
	return hex.EncodeToString(b[:])
}

func hmacsha256(s, key string) string {
	hashed := hmac.New(sha256.New, []byte(key))
	hashed.Write([]byte(s))
	return string(hashed.Sum(nil))
}

// 腾讯云api auth 算法
const Algorithm = "TC3-HMAC-SHA256"

// 根据请求参数生成腾讯云验证token
func GenerateAuthorization(reqTimestamp int64, tencentApiAuth define.TencentApiAuth,
	host, payload, httpRequestMethod string,
) string {
	logger := logging.GetLogger()

	// step 1: build canonical request string
	service := strings.Split(host, ".")[0]
	canonicalURI := "/"
	canonicalQueryString := ""
	canonicalHeaders := "content-type:application/json; charset=utf-8\n" + "host:" + host + "\n"
	signedHeaders := "content-type;host"
	hashedRequestPayload := sha256hex(payload)
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		httpRequestMethod,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		hashedRequestPayload)

	logger.Debugf("tencent cloud api canonicalRequest is %s", canonicalRequest)

	// step 2: build string to sign
	date := time.Unix(reqTimestamp, 0).UTC().Format("2006-01-02")
	credentialScope := fmt.Sprintf("%s/%s/tc3_request", date, service)
	hashedCanonicalRequest := sha256hex(canonicalRequest)
	string2sign := fmt.Sprintf("%s\n%d\n%s\n%s",
		Algorithm,
		reqTimestamp,
		credentialScope,
		hashedCanonicalRequest)

	logger.Debugf("tencent cloud api string2sign is %s", string2sign)

	// step 3: build sign string and signature
	secretDate := hmacsha256(date, "TC3"+tencentApiAuth.SecretKey)
	secretService := hmacsha256(service, secretDate)
	secretSigning := hmacsha256("tc3_request", secretService)
	signature := hex.EncodeToString([]byte(hmacsha256(string2sign, secretSigning)))

	logger.Debugf("tencent cloud api signature is %s", signature)

	// step 4: build tencent cloud api authorization
	authorization := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		Algorithm,
		tencentApiAuth.SecretId,
		credentialScope,
		signedHeaders,
		signature)

	curl := fmt.Sprintf(`curl -X POST https://%s\
		-H "Authorization: %s"\
		-H "Content-Type: application/json; charset=utf-8"\
		-H "Host: %s" -H "X-TC-Action: %s"\
		-H "X-TC-Timestamp: %d"\
		-H "X-TC-Version: %s"\
		-H "X-TC-Region: %s"\
		-d '%s'`, host, authorization,
		host, tencentApiAuth.Action,
		reqTimestamp, tencentApiAuth.Version,
		tencentApiAuth.Region, payload)

	logger.Debugf("tencent cloud api curl is \n %s", curl)

	return authorization
}

// 配置腾讯云认证header信息
func SetTencentAuth(request http.Request, endpoint string, tencentApiAuth define.TencentApiAuth,
	payload, method string,
) {
	requestUrl, err := url.Parse(endpoint)
	if err != nil {
		panic(err)
	}
	host := requestUrl.Host

	var timestamp int64 = time.Now().Unix()
	// build tencent cloud api authorization
	authorization := GenerateAuthorization(timestamp, tencentApiAuth, host, payload, method)

	request.Header.Add("Host", host)
	request.Header.Add("X-TC-Timestamp", fmt.Sprintf("%d", timestamp))
	request.Header.Add("X-TC-Action", tencentApiAuth.Action)
	request.Header.Add("X-TC-Version", tencentApiAuth.Version)
	request.Header.Add("X-TC-Region", tencentApiAuth.Region)
	request.Header.Add("Authorization", authorization)
}
