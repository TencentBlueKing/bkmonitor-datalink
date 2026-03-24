// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/TarsCloud/TarsGo/tars/protocol"
	"github.com/TarsCloud/TarsGo/tars/protocol/codec"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/basef"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/requestf"
	"github.com/TarsCloud/TarsGo/tars/util/current"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var tarsApp = "collector"

// TarsDispatch is an interface for handling dispatch operations using the Tars protocol.
type TarsDispatch interface {
	Dispatch(context.Context, any, *requestf.RequestPacket, *requestf.ResponsePacket, bool) error
}

// TarsServant is a struct that represents a Tars service with its object name, implementation, and dispatch handler.
type TarsServant struct {
	Obj      string
	Impl     any
	Dispatch TarsDispatch
}

// NewTarsServant creates and returns a new TarsServant instance.
func NewTarsServant(o string, server string, impl any, dispatch TarsDispatch) *TarsServant {
	s := &TarsServant{Obj: fmt.Sprintf("%s.%s.%s", tarsApp, server, o), Impl: impl, Dispatch: dispatch}
	return s
}

// TarsProtocol is struct for dispatch with tars protocol.
type TarsProtocol struct {
	servants    map[string]*TarsServant
	withContext bool
}

// NewTarsProtocol creates and returns a new TarsProtocol instance.
func NewTarsProtocol(servants map[string]*TarsServant, withContext bool) *TarsProtocol {
	s := &TarsProtocol{servants: servants, withContext: withContext}
	return s
}

// Invoke puts the request as []byte and call the dispatcher, and then return the response as []byte.
func (s *TarsProtocol) Invoke(ctx context.Context, req []byte) []byte {
	var reqPackage requestf.RequestPacket
	var rspPackage requestf.ResponsePacket

	is := codec.NewReader(req[4:])
	reqPackage.ReadFrom(is)

	rspPackage.IVersion = reqPackage.IVersion
	rspPackage.IRequestId = reqPackage.IRequestId
	rspPackage.CPacketType = reqPackage.CPacketType
	if ok := current.SetPacketTypeFromContext(ctx, rspPackage.CPacketType); !ok {
		logger.Errorf("failed to set packet type (%v)", rspPackage.CPacketType)
	}
	if ok := current.SetRequestContext(ctx, reqPackage.Context); !ok {
		logger.Errorf("failed to set request context (%v)", reqPackage.Context)
	}
	if ok := current.SetRequestStatus(ctx, reqPackage.Status); !ok {
		logger.Errorf("failed to set request status (%v)", reqPackage.Status)
	}

	servant, ok := s.servants[reqPackage.SServantName]
	if !ok {
		rspPackage.IRet = basef.TARSSERVERNOSERVANTERR
		return s.rsp2Byte(&rspPackage)
	}

	err := servant.Dispatch.Dispatch(ctx, servant.Impl, &reqPackage, &rspPackage, s.withContext)
	if err != nil {
		rspPackage.IRet = 1
		rspPackage.SResultDesc = err.Error()
	}
	return s.rsp2Byte(&rspPackage)
}

// InvokeTimeout indicates how to deal with timeout.
func (s *TarsProtocol) InvokeTimeout(pkg []byte) []byte {
	var reqPackage requestf.RequestPacket
	var rspPackage requestf.ResponsePacket

	is := codec.NewReader(pkg[4:])
	reqPackage.ReadFrom(is)

	// invoke timeout need to return IRequestId
	rspPackage.IRequestId = reqPackage.IRequestId
	rspPackage.IRet = 1
	rspPackage.SResultDesc = "server invoke timeout"
	return s.rsp2Byte(&rspPackage)
}

// ParsePackage parse the []byte according to the tars protocol.
// returns header length and package integrity condition (PackageLess | PackageFull | PackageError)
func (s *TarsProtocol) ParsePackage(buff []byte) (int, int) {
	return protocol.TarsRequest(buff)
}

// GetCloseMsg return a package to close connection
func (s *TarsProtocol) GetCloseMsg() []byte {
	var rspPackage requestf.ResponsePacket
	rspPackage.IVersion = basef.TARSVERSION
	rspPackage.IRequestId = 0
	rspPackage.SResultDesc = "_reconnect_"
	return s.rsp2Byte(&rspPackage)
}

// DoClose be called when close connection
func (s *TarsProtocol) DoClose(ctx context.Context) {
	logger.Debug("DoClose...")
}

func (s *TarsProtocol) rsp2Byte(rsp *requestf.ResponsePacket) []byte {
	os := codec.NewBuffer()
	rsp.WriteTo(os)

	bs := os.ToBytes()
	sbuf := bytes.NewBuffer(nil)
	sbuf.Write(make([]byte, 4))
	sbuf.Write(bs)

	length := sbuf.Len()
	binary.BigEndian.PutUint32(sbuf.Bytes(), uint32(length))
	return sbuf.Bytes()
}
