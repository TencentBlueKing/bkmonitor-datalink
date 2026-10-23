// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

package cipher

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/credential"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestKMSAESDiagnosticsOmitInputAndKey(t *testing.T) {
	oldKey, oldCipher := config.AesKey, dbAESCipher
	t.Cleanup(func() {
		config.AesKey = oldKey
		dbAESCipher = oldCipher
		aesOnce = sync.Once{}
		logger.SetOptions(logger.Options{Stdout: true})
	})
	logFile := filepath.Join(t.TempDir(), "aes.log")
	logger.SetOptions(logger.Options{Filename: logFile})
	config.AesKey = "SECRET_KEY_MARKER"
	dbAESCipher = nil
	aesOnce = sync.Once{}
	GetDBAESCipher()
	c := NewAESCipher("SECRET_KEY_MARKER", AESPrefix, []byte("short"))
	require.Empty(t, c.AESEncrypt("SECRET_RAW_MARKER"))
	encrypted := AESPrefix + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	_, _ = c.AESDecrypt(encrypted)
	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	for _, secret := range []string{"SECRET_KEY_MARKER", "SECRET_RAW_MARKER", encrypted} {
		require.NotContains(t, string(data), secret)
	}
	require.Contains(t, string(data), "encrypt failed")
	require.Contains(t, string(data), "decrypt failed")
}

func TestKMSPreservesEmptyAESDerivationAndBkdataFallback(t *testing.T) {
	oldKey, oldBkdata, oldIV, oldSalt := config.AesKey, config.BkdataAESKey, config.BkdataAESIv, config.BkdataTokenSalt
	t.Cleanup(func() {
		config.AesKey = oldKey
		config.BkdataAESKey = oldBkdata
		config.BkdataAESIv = oldIV
		config.BkdataTokenSalt = oldSalt
	})
	config.AesKey = ""
	config.BkdataAESKey = ""
	config.BkdataAESIv = "bkbkbkbkbkbkbkbk"
	config.BkdataTokenSalt = "bk"
	before := TransformDataidToToken(1, 2, 3, 4, "test-app")
	dir, err := filepath.Abs("../../credential/testdata")
	require.NoError(t, err)
	v, raw := viper.New(), viper.New()
	raw.Set("kms.enabled", true)
	raw.Set("kms.envelope_file", filepath.Join(dir, "envelope"))
	raw.Set("kms.private_key_file", filepath.Join(dir, "private-key"))
	snapshot, err := credential.Prepare(v, raw, "bmw")
	require.NoError(t, err)
	snapshot.Apply(v)
	config.AesKey = v.GetString("aes.key")
	config.BkdataAESKey = v.GetString("aes.bkdataAESKey")
	config.BkdataAESIv = v.GetString("aes.bkdataAESIv")
	config.BkdataTokenSalt = v.GetString("aes.bkdataToken")
	require.Equal(t, before, TransformDataidToToken(1, 2, 3, 4, "test-app"))
	plain, err := NewAESCipher(config.AesKey, "", []byte(config.BkdataAESIv)).AESDecrypt(before)
	require.NoError(t, err)
	require.Equal(t, "1bk2bk3bk4bktest-app", plain)
	// This pre-existing fixed ciphertext must retain its original plaintext.
	plain, err = NewAESCipher("81be7fc6-5476-4934-9417-6d4d593728db", AESPrefix, nil).AESDecrypt("aes_str:::srCvsNoBIUsCtBfqASIAcTlQThp3GVHqu726bvhpVjo=")
	require.NoError(t, err)
	require.Equal(t, "5gYTZqvd7Z7s", plain)
}
