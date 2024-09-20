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
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
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
	logger.Infof("AESDecrypt：password -> [%v],c.Prefix -> [%v] ", encryptedPwd, c.Prefix)
	// 非加密串返回原密码
	if c.Prefix != "" && !strings.HasPrefix(encryptedPwd, c.Prefix) {
		logger.Infof("AESDecrypt：password [%v] is not encrypted, return password", encryptedPwd)
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
	logger.Errorf("AESDecrypt：padding size ->  [%d] ,length ->  [%d] ,decryptedData ->  [%s]", padSize, length, decryptedData)
	if padSize > length {
		return "", fmt.Errorf("AESDecrypt：invalid padding size")
	}
	realPwd := string(decryptedData[:(length - padSize)])
	return realPwd, nil
}

// AESEncrypt AES加密
func (c AESCipher) AESEncrypt(raw string) string {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			logger.Warnf("AESEncrypt：encrypt password [%v] failed, return '', %v\n%s", raw, r, stack)
		}
	}()
	rawBytes := []byte(raw)
	padSize := aes.BlockSize - len(rawBytes)%aes.BlockSize
	padText := make([]byte, padSize)
	for i := range padText {
		padText[i] = byte(padSize)
	}
	padData := append(rawBytes, padText...)

	key := sha256.Sum256([]byte(c.XKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		logger.Errorf("new cipher error, %s", err)
		return ""
	}
	var iv []byte
	if len(c.IV) != 0 {
		iv = c.IV
	} else {
		iv = make([]byte, aes.BlockSize)
		if _, err := io.ReadFull(rand.Reader, iv); err != nil {
			logger.Errorf("generating IV faield, %s", err)
			return ""
		}
	}
	encrypter := cipher.NewCBCEncrypter(block, iv)
	encryptedData := make([]byte, len(padData))
	encrypter.CryptBlocks(encryptedData, padData)
	// 组合数据
	var data []byte
	data = append(data, iv...)
	data = append(data, encryptedData...)
	// base64编码
	encodedData := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("%s%s", c.Prefix, encodedData)
}

func NewAESCipher(xKey, prefix string, iv []byte) *AESCipher {
	return &AESCipher{XKey: xKey, Prefix: prefix, IV: iv}
}

var dbAESCipher *AESCipher

var aesOnce sync.Once

// GetDBAESCipher 获取db中AES字段的AESCipher
func GetDBAESCipher() *AESCipher {
	aesOnce.Do(func() {
		// 从配置文件中获取AESKey,AESKey为空时会产生异常
		logger.Infof("GetDBAESCipher：config.AesKey -> [%v], AESPrefix -> [%v]", config.AesKey, AESPrefix)
		dbAESCipher = NewAESCipher(config.AesKey, AESPrefix, nil)
	})
	return dbAESCipher
}
