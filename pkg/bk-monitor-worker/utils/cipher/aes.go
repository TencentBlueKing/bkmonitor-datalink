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
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	AESPrefix = "aes_str:::"
)

type AESCipher struct {
	XKey   string
	Prefix string
	IV     []byte
}

// AESDecrypt AES解密
func (c AESCipher) AESDecrypt(encryptedPwd string) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			logger.Warnf("AESDecrypt：decrypt password [%v] failed, return '', %v\n%s", encryptedPwd, r, stack)
		}
	}()
	// 非加密串返回原密码
	if c.Prefix != "" && !strings.HasPrefix(encryptedPwd, c.Prefix) {
		return encryptedPwd, nil
	}
	// 截取实际加密数据段
	encryptedPwd = strings.TrimPrefix(encryptedPwd, c.Prefix)
	// base64解码
	decodedData, err := base64.StdEncoding.DecodeString(encryptedPwd)
	if err != nil {
		logger.Errorf("AESDecrypt：base64 decode password error, %s", err)
		return "", err
	}
	// 获取key、IV和加密密码
	key := sha256.Sum256([]byte(c.XKey))
	var iv []byte
	if len(c.IV) != 0 {
		iv = c.IV
	} else {
		iv = decodedData[:aes.BlockSize]
	}
	encryptedData := decodedData[aes.BlockSize:]

	block, err := aes.NewCipher(key[:])
	if err != nil {
		logger.Errorf("AESDecrypt：new cipher error, %s", err)
		return "", err
	}
	// CBC解密
	decrypter := cipher.NewCBCDecrypter(block, iv)
	decryptedData := make([]byte, len(encryptedData))
	decrypter.CryptBlocks(decryptedData, encryptedData)

	length := len(decryptedData)
	padSize := int(decryptedData[length-1])
	// 若填充大小大于数据长度，则说明数据不正确
	if padSize > length {
		return "", fmt.Errorf("AESDecrypt：invalid padding size")
	}
	realPwd := string(decryptedData[:(length - padSize)])
	return realPwd, nil
}

func NewAESCipher(xKey, prefix string, iv []byte) *AESCipher {
	return &AESCipher{XKey: xKey, Prefix: prefix, IV: iv}
}

var dbAESCipher *AESCipher

var aesOnce sync.Once

// GetDBAESCipher 获取db中AES字段的AESCipher
func GetDBAESCipher() *AESCipher {
	aesOnce.Do(func() {
		dbAESCipher = NewAESCipher(config.AesKey, AESPrefix, nil)
	})
	return dbAESCipher
}
