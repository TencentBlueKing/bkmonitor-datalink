// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenchecker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	decoderTypeFixed   = "fixed"
	decoderTypeAcs256  = "aes256"
	decoderTypeProxy   = "proxy"
	decoderTypeLogBeat = "logbeat"
)

func NewTokenDecoder(c Config) TokenDecoder {
	switch c.Type {
	case decoderTypeFixed:
		return newFixedTokenDecoder(c)
	case decoderTypeAcs256:
		return newAes256TokenDecoder(c)
	case decoderTypeProxy:
		return newProxyTokenDecoder(c)
	case decoderTypeLogBeat:
		return newLogBeatDecoder()
	}

	// 未指定 token decoder 时使用固定的解析方案（for test）
	return newFixedTokenDecoder(Config{
		Type:       decoderTypeFixed,
		FixedToken: "unspecified-token",
		AppName:    "unspecified-app",
	})
}

type TokenDecoder interface {
	Type() string
	Skip() bool
	Decode(s string) (define.Token, error)
}

// newFixedTokenDecoder 根据配置生成固定 Token 用于测试场景
func newFixedTokenDecoder(c Config) TokenDecoder {
	return fixedTokenDecoder{
		token: define.Token{
			Original:       c.FixedToken,
			TracesDataId:   c.TracesDataId,
			MetricsDataId:  c.MetricsDataId,
			ProfilesDataId: c.ProfilesDataId,
			LogsDataId:     c.LogsDataId,
			BizId:          c.BizId,
			AppName:        c.AppName,
		},
	}
}

type fixedTokenDecoder struct {
	token define.Token
}

func (d fixedTokenDecoder) Type() string {
	return decoderTypeFixed
}

func (d fixedTokenDecoder) Skip() bool {
	return true
}

func (d fixedTokenDecoder) Decode(string) (define.Token, error) {
	return d.token, nil
}

// Aes256TokenDecoder 使用 aes256 加盐算法 所有字段均由配置项指定
func newAes256TokenDecoder(c Config) *aes256TokenDecoder {
	h := sha256.New()
	h.Write([]byte(c.DecodedKey))
	key := h.Sum(nil)
	return &aes256TokenDecoder{
		salt:  c.Salt,
		key:   key,
		iv:    []byte(c.DecodedIv),
		cache: map[string]define.Token{},
	}
}

type aes256TokenDecoder struct {
	salt string
	key  []byte
	iv   []byte

	mut   sync.Mutex
	cache map[string]define.Token
}

func (d *aes256TokenDecoder) Type() string {
	return decoderTypeAcs256
}

func (d *aes256TokenDecoder) Skip() bool {
	return false
}

func (d *aes256TokenDecoder) Decode(s string) (define.Token, error) {
	d.mut.Lock()
	defer d.mut.Unlock()

	v, ok := d.cache[s]
	if ok {
		return v, nil
	}

	token, err := d.decode(s)
	if err != nil {
		return token, errors.Wrapf(err, "invalid token: %s", s)
	}

	d.cache[s] = token
	return token, err
}

func (d *aes256TokenDecoder) decode(s string) (define.Token, error) {
	var token define.Token
	enc, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return token, err
	}
	if len(enc) < aes.BlockSize {
		return token, errors.Errorf("invalid prefix-enc len: %d", len(enc))
	}

	block, err := aes.NewCipher(d.key)
	if err != nil {
		return token, err
	}

	enc = enc[aes.BlockSize:]
	stream := cipher.NewCBCDecrypter(block, d.iv)

	if len(enc)%aes.BlockSize != 0 {
		return token, errors.New("crypto/cipher: input not full blocks")
	}

	stream.CryptBlocks(enc, enc)
	if len(enc) < aes.BlockSize {
		return token, errors.Errorf("invalid suffix-enc len: %d", len(enc))
	}

	decodedRune, _ := utf8.DecodeRune(enc[len(enc)-1:])
	delta := len(enc) - int(decodedRune)
	if delta < 0 {
		return token, errors.Errorf("invalid suffix-enc delta: %d", delta)
	}
	decoded := string(enc[:delta])
	logger.Debugf("original token: %s, decoded: %v", s, decoded)

	split := strings.SplitN(decoded, d.salt, 2)
	if len(split) < 2 {
		return token, errors.Errorf("invalid split len: %d, str: %s", len(split), decoded)
	}

	if split[0] == "v1" {
		return d.parseV1Token(decoded, s)
	} else {
		return d.parseV0Token(decoded, s)
	}
}

// v0 token: metricsid#logsid#tracesid#bizid#appname
func (d *aes256TokenDecoder) parseV0Token(decoded string, o string) (define.Token, error) {
	var token define.Token

	split := strings.SplitN(decoded, d.salt, 5)
	if len(split) < 5 {
		return token, errors.Errorf("invalid split len: %d, str: %s", len(split), decoded)
	}

	metricsDataId, err := strconv.Atoi(split[0])
	if err != nil {
		return token, errors.Errorf("invalid metrics dataid: %s", split[0])
	}
	tracesDataId, err := strconv.Atoi(split[1])
	if err != nil {
		return token, errors.Errorf("invalid traces dataid: %s", split[1])
	}
	logsDataId, err := strconv.Atoi(split[2])
	if err != nil {
		return token, errors.Errorf("invalid logs dataid: %s", split[2])
	}
	bizId, err := strconv.Atoi(split[3])
	if err != nil {
		return token, errors.Errorf("invalid bizid: %s", split[3])
	}
	appName := split[4]

	return define.Token{
		Original:      o,
		TracesDataId:  int32(tracesDataId),
		MetricsDataId: int32(metricsDataId),
		LogsDataId:    int32(logsDataId),
		BizId:         int32(bizId),
		AppName:       appName,
	}, nil
}

// v1 token: v1#metricsid#logsid#tracesid#profilesid#bizid#appname
func (d *aes256TokenDecoder) parseV1Token(decoded string, o string) (define.Token, error) {
	var token define.Token
	split := strings.SplitN(decoded, d.salt, 7)
	if len(split) < 7 {
		return token, errors.Errorf("invalid split len: %d, str: %s", len(split), decoded)
	}

	metricsDataId, err := strconv.Atoi(split[1])
	if err != nil {
		return token, errors.Errorf("invalid metrics dataid: %s", split[1])
	}
	tracesDataId, err := strconv.Atoi(split[2])
	if err != nil {
		return token, errors.Errorf("invalid traces dataid: %s", split[2])
	}
	logsDataId, err := strconv.Atoi(split[3])
	if err != nil {
		return token, errors.Errorf("invalid logs dataid: %s", split[3])
	}
	profilesDataId, err := strconv.Atoi(split[4])
	if err != nil {
		return token, errors.Errorf("invalid profiles dataid: %s", split[4])
	}
	bizId, err := strconv.Atoi(split[5])
	if err != nil {
		return token, errors.Errorf("invalid bizid: %s", split[5])
	}
	appName := split[6]

	return define.Token{
		Original:       o,
		TracesDataId:   int32(tracesDataId),
		MetricsDataId:  int32(metricsDataId),
		LogsDataId:     int32(logsDataId),
		ProfilesDataId: int32(profilesDataId),
		BizId:          int32(bizId),
		AppName:        appName,
	}, nil
}

func newProxyTokenDecoder(c Config) proxyTokenDecoder {
	return proxyTokenDecoder{
		token:  c.ProxyToken,
		dataId: c.ProxyDataId,
	}
}

type proxyTokenDecoder struct {
	token  string
	dataId int32
}

func (d proxyTokenDecoder) Type() string {
	return decoderTypeProxy
}

func (d proxyTokenDecoder) Skip() bool {
	return false
}

func (d proxyTokenDecoder) Decode(s string) (define.Token, error) {
	logger.Debugf("proxy token=%v", s)
	if define.WrapProxyToken(define.Token{}) == s {
		return define.Token{}, errors.New("reject empty token")
	}

	token := define.Token{Original: d.token, ProxyDataId: d.dataId}
	if define.WrapProxyToken(token) != s {
		return define.Token{}, errors.Errorf("reject invalid token: %s", s)
	}

	return token, nil
}

func newLogBeatDecoder() logBeatDecoder {
	return logBeatDecoder{}
}

type logBeatDecoder struct{}

func (d logBeatDecoder) Type() string {
	return decoderTypeLogBeat
}

func (d logBeatDecoder) Skip() bool {
	return false
}

func (d logBeatDecoder) Decode(s string) (define.Token, error) {
	logger.Debugf("logbeat token=%v", s)
	if len(s) <= 0 {
		return define.Token{}, errors.New("reject empty dataid")
	}

	i, _ := strconv.Atoi(s)
	if i <= 0 {
		return define.Token{}, errors.Errorf("reject invalid dataid: %s", s)
	}
	return define.Token{
		Original:   s,
		LogsDataId: int32(i),
	}, nil
}
