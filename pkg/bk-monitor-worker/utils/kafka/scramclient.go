// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"crypto/sha256"
	"crypto/sha512"
	"hash"

	"github.com/xdg/scram"
)

// hash算法
var (
	SHA256 scram.HashGeneratorFcn = func() hash.Hash { return sha256.New() }
	SHA512 scram.HashGeneratorFcn = func() hash.Hash { return sha512.New() }
)

// XDGSCRAMClient XDG SCRAM Client
type XDGSCRAMClient struct {
	*scram.Client
	*scram.ClientConversation
	scram.HashGeneratorFcn
}

// Begin implements the scram.Client interface.
func (x *XDGSCRAMClient) Begin(userName, password, authzID string) (err error) {
	x.Client, err = x.HashGeneratorFcn.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	x.ClientConversation = x.Client.NewConversation()
	return nil
}

// Step implements the scram.Client interface.
func (x *XDGSCRAMClient) Step(challenge string) (response string, err error) {
	response, err = x.ClientConversation.Step(challenge)
	return response, err
}

// Done implements the scram.Client interface.
func (x *XDGSCRAMClient) Done() bool {
	return x.ClientConversation.Done()
}
