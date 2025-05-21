// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package gse

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/elastic/beats/libbeat/logp"
)

// gse protocol type
const (
	GSE_TYPE_COMMON   = 3072 + 1  // MSG_DATA_REPORT
	GSE_TYPE_GET_CONF = 0x0A      // REPORT_SYNC_CONFIG
	GSE_TYPE_DYNAMIC  = 0x09      // REPORT_DYNAMICAL_PROTOCOL_TYPE
	GSE_TYPE_OP       = 3072 + 12 // MSG_DATA_REPORT_OPS
	GSE_TYPE_TLOGC    = 0x02      // REPORT_EXT
)

// gse protocol flags
const (
	GSE_FLAG_REDUNDENCY = 0x01 // PLUGIN_REDUNDENCY_FLAG
)

// GseConnection : gse connection
type GseConnection interface {
	Dial() error
	Close() error
	Write(b []byte) (int, error)
	Read(b []byte) (int, error)
	SetTimeoutTime(r time.Duration, w time.Duration)
	SetHost(host string)
}

var errNoConnection = errors.New("connection not inited")

// AgentInfo : get info from agent
// now can get bizid, cloudid, ip
type AgentInfo struct {
	Bizid        int32  `json:"bizid"`
	BKBizID      int32  `json:"bk_biz_id"`
	Cloudid      int32  `json:"cloudid"`
	IP           string `json:"ip"`
	BKAgentID    string `json:"bk_agent_id"`
	BKTenantID   string `json:"bk_tenant_id"`
	StaticDataID int32  `json:"static_dataid"`
	HostID       int32
	Hostname     string
}

// String
func (info *AgentInfo) String() string {
	return fmt.Sprintf("bizid=%d, cloudid=%d, ip=%s, hostname=%s bk_agent_id=%s bk_host_id=%d",
		info.Bizid, info.Cloudid, info.IP, info.Hostname, info.BKAgentID, info.HostID)
}

// IsEmpty 是否为空，用来判断是否读取到正常信息
func (info *AgentInfo) IsEmpty() bool {
	return info.IP == "" && info.BKAgentID == ""
}

// GseMsg : gse msg
type GseMsg interface {
	ToBytes() []byte
}

// --------------- GseCommonMsg ------------

// GseCommonMsgHead
type GseCommonMsgHead struct {
	msgtype uint32
	dataid  int32
	utctime uint32
	bodylen uint32
	resv    [2]uint32
}

// GseCommonMsg : msg for GSE_TYPE_COMMON
type GseCommonMsg struct {
	head GseCommonMsgHead
	data []byte
}

// NewGseCommonMsg
func NewGseCommonMsg(data []byte, dataid int32, resv1, resv2, flag uint32) *GseCommonMsg {
	var msg GseCommonMsg
	msg.head.msgtype = GSE_TYPE_COMMON
	msg.head.dataid = dataid
	msg.head.utctime = uint32(time.Now().Unix())
	msg.head.bodylen = uint32(len(data))
	msg.head.resv[0] = resv1
	msg.head.resv[1] = resv2
	msg.data = data
	return &msg
}

// ToBytes
func (msg *GseCommonMsg) ToBytes() []byte {
	buffer := new(bytes.Buffer)
	binary.Write(buffer, binary.BigEndian, msg.head)
	binary.Write(buffer, binary.LittleEndian, msg.data[:])
	return buffer.Bytes()
}

// --------------- GseDynamicMsg ------------

// GseDynamicMsgHead
type GseDynamicMsgHead struct {
	GseCommonMsgHead
	index      uint64
	flags      uint32
	metaLen    uint32
	metaMaxLen uint32
	metaCount  uint32
}

// GseDynamicMetaInfo
type GseDynamicMetaInfo struct {
	keyLen    uint32
	valueLen  uint32
	metaKey   string
	metaValue string
}

const (
	GSE_TYPE_DYNAMIC_DEFAULT_META_MAX_LEN = 408 // keep same with gse (8 + 128) * 3 = 408b
	GSE_TYPE_DYNAMIC_EXT_HEAD_LEN         = 24  // sizeof (index ... metaCount) = 24B
	GSE_TYPE_DYNAMIC_META_LEN             = 8   // len(keyLen) + len(valueLen) = 8B
)

// GseDynamicMsg :
type GseDynamicMsg struct {
	head   GseDynamicMsgHead
	meatas []GseDynamicMetaInfo
	data   []byte
}

// NewGseDynamicMsg : new GseDynamicMsg
func NewGseDynamicMsg(data []byte, dataid int32, resv1, resv2 uint32) *GseDynamicMsg {
	var msg GseDynamicMsg
	msg.head.msgtype = GSE_TYPE_DYNAMIC
	msg.head.dataid = dataid
	msg.head.utctime = uint32(time.Now().Unix())
	msg.head.resv[0] = resv1
	msg.head.resv[1] = resv2
	msg.head.metaLen = 0
	msg.head.metaCount = 0
	msg.head.metaMaxLen = GSE_TYPE_DYNAMIC_DEFAULT_META_MAX_LEN
	msg.head.metaCount = 0
	msg.head.bodylen = uint32(len(data)) + GSE_TYPE_DYNAMIC_EXT_HEAD_LEN + msg.head.metaMaxLen
	msg.data = data
	return &msg
}

// ToBytes : change msg to bytes
func (msg *GseDynamicMsg) ToBytes() []byte {
	buffer := new(bytes.Buffer)
	// fill head
	binary.Write(buffer, binary.BigEndian, msg.head)
	logp.Debug("gse", "after fill head buffer len:%d", buffer.Len())

	// fill meta infos
	for _, meta := range msg.meatas {
		binary.Write(buffer, binary.BigEndian, meta.keyLen)
		binary.Write(buffer, binary.BigEndian, meta.valueLen)
		binary.Write(buffer, binary.LittleEndian, []byte(meta.metaKey))
		binary.Write(buffer, binary.LittleEndian, []byte(meta.metaValue))
	}

	// fill empty meta buffer
	leftLen := msg.head.metaMaxLen - msg.head.metaLen
	binary.Write(buffer, binary.LittleEndian, make([]byte, leftLen))
	logp.Debug("gse", "after fill meta buffer len:%d", buffer.Len())

	// fill data
	binary.Write(buffer, binary.LittleEndian, msg.data[:])
	logp.Debug("gse", "after fill data buffer len:%d", buffer.Len())

	return buffer.Bytes()
}

// AddMeta
func (msg *GseDynamicMsg) AddMeta(key, value string) error {
	willLen := msg.head.metaLen + GSE_TYPE_DYNAMIC_META_LEN +
		uint32(len(key)) + uint32(len(value))
	if willLen > msg.head.metaMaxLen {
		return fmt.Errorf("meta len (%d,%d) is too large", len(key), len(value))
	}

	meta := GseDynamicMetaInfo{
		keyLen:    uint32(len(key)),
		valueLen:  uint32(len(value)),
		metaKey:   key,
		metaValue: value,
	}
	msg.meatas = append(msg.meatas, meta)
	msg.head.metaCount += 1
	msg.head.metaLen = willLen
	return nil
}

/*
func (meta *GseDynamicMsg) Len() uint32 {
	return uint32(8 + len(meta.Key) + len(meta.Value))
}
*/
// --------------- GseOpMsg ------------

// GseOpMsg : msg for MSG_DATA_REPORT_OPS
type GseOpMsg struct {
	GseCommonMsg
}

// NewGseOpMsg
func NewGseOpMsg(data []byte, dataid int32, resv1, resv2, flag uint32) *GseOpMsg {
	var msg GseOpMsg
	msg.head.msgtype = GSE_TYPE_OP
	msg.head.dataid = dataid
	msg.head.utctime = uint32(time.Now().Unix())
	msg.head.bodylen = uint32(len(data))
	msg.head.resv[0] = resv1
	msg.head.resv[1] = resv2
	msg.data = data
	return &msg
}

// ToBytes() use GseCommonMsg ToBytes()

// --------------- GseRequestConfMsg ------------

// GseRequestConfMsg : msg for GSE_TYPE_GET_CONF
type GseRequestConfMsg struct {
	GseCommonMsg
}

// NewGseRequestConfMsg
func NewGseRequestConfMsg() *GseRequestConfMsg {
	var msg GseRequestConfMsg
	msg.head.msgtype = GSE_TYPE_GET_CONF
	return &msg
}

// ToBytes() use GseCommonMsg ToBytes()

// --------------- GseRequestResultMsg ------------

// GseLocalCommandMsg
type GseLocalCommandMsg struct {
	MsgType uint32
	BodyLen uint32
}
