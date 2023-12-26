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
			logger.Warnf("decrypt password [%v] failed,return '', %v", encryptedPwd, r)
		}
	}()
	// 非加密串返回原密码
	if !strings.HasPrefix(encryptedPwd, AESPrefix) {
		return encryptedPwd
	} else {
		// 截取实际加密数据段
		encryptedPwd = strings.TrimPrefix(encryptedPwd, AESPrefix)
	}
	// base64解码
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedPwd)
	if err != nil {
		logger.Errorf("base64 decode password error, %s", err)
		return ""
	}
	// 获取iv和key
	iv := encryptedPwd[:aes.BlockSize]
	key := sha256.Sum256([]byte(config.AesKey))

	block, err := aes.NewCipher(key[:])
	if err != nil {
		logger.Errorf("new cipher error, %s", err)
		return ""
	}
	decrypter := cipher.NewCBCDecrypter(block, []byte(iv))
	// 解密
	decrypted := make([]byte, len(ciphertext))
	decrypter.CryptBlocks(decrypted, ciphertext)

	plainData := decrypted[aes.BlockSize:]
	length := len(plainData)
	unpadding := int(plainData[length-1])
	realPwd := string(plainData[:(length - unpadding)])
	return realPwd
}
