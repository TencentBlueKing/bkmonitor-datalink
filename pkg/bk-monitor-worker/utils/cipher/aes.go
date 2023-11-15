// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	AESPrefix = "aes_str:::"
)

// AESDecrypt AES256解密
func AESDecrypt(encryptedPwd string) string {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("decrypt password failed")
		}
	}()
	// 非加密串返回原密码
	if !strings.HasPrefix(encryptedPwd, AESPrefix) {
		return encryptedPwd
	}

	// 截取实际加密数据段
	realEncrypted := encryptedPwd[len(AESPrefix):]
	// base64解码
	base64Decoded, err := base64.StdEncoding.DecodeString(realEncrypted)
	if err != nil {
		logger.Errorf("base64 decode password error, %s", err)
		return ""
	}
	// 获取iv
	iv := base64Decoded[:aes.BlockSize]
	key := sha256.Sum256([]byte(viper.GetString(config.AesKey)))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		logger.Errorf("new cipher error, %s", err)
		return ""
	}
	decrypter := cipher.NewCBCDecrypter(block, iv)
	// 解密
	decrypter.CryptBlocks(base64Decoded, base64Decoded)
	part := base64Decoded[aes.BlockSize:]
	k := int(part[len(part)-1])
	realPwd := string(part[:len(part)-k])

	return realPwd
}
