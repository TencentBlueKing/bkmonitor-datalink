// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trap

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/gosnmp/gosnmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/test"
)

const (
	trapTestAddress       = "127.0.0.1"
	trapTestPort          = 9162
	trapTestOid           = ".1.2.1234.4.5"
	trapTestSysUptimeOid  = ".1.3.6.1.2.1.1.3.0"
	trapTestPayload       = "TRAPTEST1234"
	trapTestEnterpriseOid = ".1.2.1234"
	trapTestAgentAddress  = "127.0.0.1"
	trapTestGenericTrap   = 6
	trapTestSpecificTrap  = 55
	trapTestTimestamp     = 300
)

var expectDimension = map[string]string{
	"1_2_1234_4_5":       trapTestPayload,
	EventAgentAddressKey: trapTestAgentAddress,
	// EventAggentPortKey:    "51080",
	EventCommunityKey:    "public",
	EventDisplayNameKey:  ".1.2.1234.0.55",
	EventEnterpriseKey:   trapTestEnterpriseOid,
	EventGenericTrapKey:  strconv.Itoa(trapTestGenericTrap),
	EventSpecificTrapKey: strconv.Itoa(trapTestSpecificTrap),
	EventServerIPKey:     trapTestAddress,
	EventServerPortKey:   strconv.Itoa(trapTestPort),
	EventVersionKey:      snmpVersionToStr(trapTestVersion),
}

var testPeriod = 500 * time.Millisecond

var trapTestVersion gosnmp.SnmpVersion

var expectV1Content = map[string]interface{}{
	".1.2.1234.4.5(.1.2.1234.4.5)": "TRAPTEST1234",
	"agent_address":                "127.0.0.1",
	"agent_port":                   "44394",
	"community":                    "public",
	"display_name":                 ".1.2.1234.0.55",
	"enterprise":                   ".1.2.1234",
	"generic_trap":                 "6",
	"server_ip":                    "127.0.0.1",
	"server_port":                  "9162",
	"snmptrapoid":                  ".1.2.1234.0.55",
	"specific_trap":                "55",
	"timestamp":                    "15:39:54 2023/02/08",
	"version":                      "v1",
}

var expectV2Content = map[string]interface{}{
	".1.2.1234.4.5(.1.2.1234.4.5)":           "TRAPTEST1234",
	".1.3.6.1.2.1.1.3.0(.1.3.6.1.2.1.1.3.0)": "1675844992",
	"agent_address":                          "127.0.0.1",
	"agent_port":                             "59868",
	"community":                              "public",
	"display_name":                           ".1.2.1234.0.55",
	"enterprise":                             "",
	"generic_trap":                           "0",
	"server_ip":                              "127.0.0.1",
	"server_port":                            "9162",
	"snmptrapoid":                            ".1.2.1234.0.55",
	"specific_trap":                          "0",
	"timestamp":                              "16:29:52 2023/02/08",
	"version":                                "v2c",
}

var expectV3Content = map[string]interface{}{
	".1.2.1234.4.5(.1.2.1234.4.5)":           "TRAPTEST1234",
	".1.3.6.1.2.1.1.3.0(.1.3.6.1.2.1.1.3.0)": "1675844992",
	"agent_address":                          "127.0.0.1",
	"agent_port":                             "59868",
	"community":                              "",
	"display_name":                           ".1.2.1234.0.55",
	"enterprise":                             "",
	"generic_trap":                           "0",
	"server_ip":                              "127.0.0.1",
	"server_port":                            "9162",
	"snmptrapoid":                            ".1.2.1234.0.55",
	"specific_trap":                          "0",
	"timestamp":                              "16:29:52 2023/02/08",
	"version":                                "v3",
}

var expectCount uint32

type GatherSuit struct {
	suite.Suite
	g      *Gather
	ctx    context.Context
	Cancel context.CancelFunc
	outPut chan define.Event
}

func newGather(taskConf *configs.TrapConfig) *Gather {
	globalConf := configs.NewConfig()
	err := globalConf.Clean()
	if err != nil {
	}
	err = taskConf.Clean()
	if err != nil {
	}

	return New(globalConf, taskConf).(*Gather)
}

func (gs *GatherSuit) SetupSuite() {
	gs.outPut = make(chan define.Event)
	expectCount = 3
}

func (gs *GatherSuit) TestAllVersion() {
	taskConf := configs.NewTrapConfig()
	taskConf.Port = trapTestPort
	taskConf.IP = trapTestAddress
	taskConf.Period = testPeriod
	taskConf.Community = "public"
	if expectCount > 0 {
		taskConf.IsAggregate = true
	}
	taskConf.ReportOIDDimensions = []string{
		trapTestOid, trapTestSysUptimeOid,
	}

	gs.runV1(taskConf)
	gs.runV2(taskConf)
	gs.runV3(taskConf)
}

func (gs *GatherSuit) runV1(taskConf *configs.TrapConfig) {
	taskConf.Version = snmpVersionToStr(gosnmp.Version1)
	gs.ctx, gs.Cancel = context.WithCancel(context.Background())

	expectDimension[EventVersionKey] = taskConf.Version

	gs.g = newGather(taskConf)
	gs.gatherRun()
}

func (gs *GatherSuit) runV2(taskConf *configs.TrapConfig) {
	trapTestVersion = gosnmp.Version2c
	taskConf.Version = snmpVersionToStr(trapTestVersion)
	gs.ctx, gs.Cancel = context.WithCancel(context.Background())

	expectDimension[EventVersionKey] = taskConf.Version
	expectDimension[EventEnterpriseKey] = ""
	expectDimension[EventGenericTrapKey] = "0"
	expectDimension[EventSpecificTrapKey] = "0"

	gs.g = newGather(taskConf)
	gs.gatherRun()
}

func (gs *GatherSuit) runV3(taskConf *configs.TrapConfig) {
	trapTestVersion = gosnmp.Version3
	taskConf.Version = snmpVersionToStr(trapTestVersion)
	gs.ctx, gs.Cancel = context.WithCancel(context.Background())

	taskConf.UsmInfos = []configs.UsmInfo{
		{
			MsgFlags:    "authpriv",
			ContextName: "xxx",
			USMConfig: configs.USMConfig{
				UserName:                 "testx",
				AuthenticationProtocol:   "sha",
				AuthenticationPassphrase: "passwordx",
				PrivacyProtocol:          "des",
				PrivacyPassphrase:        "passwordx",
				AuthoritativeEngineBoots: 1,
				AuthoritativeEngineTime:  1,
				AuthoritativeEngineID:    "800000000102030400",
			},
		},
		{
			ContextName: "xxx",
			MsgFlags:    "authpriv",
			USMConfig: configs.USMConfig{
				UserName:                 "test",
				AuthenticationProtocol:   "sha",
				AuthenticationPassphrase: "password",
				PrivacyProtocol:          "des",
				PrivacyPassphrase:        "password",
				AuthoritativeEngineBoots: 1,
				AuthoritativeEngineTime:  1,
				AuthoritativeEngineID:    "8000000001020304",
			},
		},
	}

	expectDimension[EventCommunityKey] = ""
	expectDimension[EventVersionKey] = taskConf.Version
	expectDimension[EventEnterpriseKey] = ""
	expectDimension[EventGenericTrapKey] = "0"
	expectDimension[EventSpecificTrapKey] = "0"

	gs.g = newGather(taskConf)
	gs.gatherRun()
}

func (gs *GatherSuit) gatherRun() {
	var wg sync.WaitGroup
	go gs.g.Run(gs.ctx, gs.outPut)
	time.Sleep(testPeriod / 10)
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 仅接收一次 break
		item := <-gs.outPut
		fmt.Println("got trap", time.Now())
		res, err := item.AsMapStr().GetValue("data")
		gs.NoError(err, "get data error")
		datalist := res.([]common.MapStr)

		dimension, err := datalist[0].GetValue("dimension")
		gs.NoError(err, "get dimension error")
		mapDimension := dimension.(common.MapStr)

		eventContent, err := datalist[0].GetValue("event")
		gs.NoError(err, "get event content error")

		timestamp, err := datalist[0].GetValue("timestamp")
		gs.NoError(err, "get timestamp error")

		for expectKey, expectVal := range expectDimension {
			actualVal, ok := mapDimension[expectKey].(string)
			if !ok {
				gs.Failf("value error", "key %s is not string, actual %v", expectKey, mapDimension[expectKey])
				return
			}
			gs.Equal(expectVal, actualVal)
		}

		eventCntMap := eventContent.(common.MapStr)
		actualContentString := eventCntMap["content"].(string)
		actualContent := make(map[string]interface{})
		err = json.Unmarshal([]byte(actualContentString), &actualContent)
		if err != nil {
			gs.NoError(err, "decode actual content string error")
			return
		}
		actualCount := eventCntMap["count"].(uint32)
		var expectCnt map[string]interface{}
		switch trapTestVersion {
		case gosnmp.Version1:
			expectCnt = expectV1Content
			expectCnt["agent_port"] = mapDimension[EventAggentPortKey]
		case gosnmp.Version2c:
			expectCnt = expectV2Content
			fallthrough
		case gosnmp.Version3:
			if trapTestVersion == gosnmp.Version3 {
				expectCnt = expectV3Content
			}
			sysUptimeTS, ok := mapDimension[sysUptimeOid].(string)
			if !ok {
				gs.Failf("value error", "key %s is not string, actual %v", sysUptimeOid, mapDimension[sysUptimeOid])
				return
			}
			expectCnt[trapTestSysUptimeOid+"("+trapTestSysUptimeOid+")"] = sysUptimeTS
			expectCnt["agent_port"] = mapDimension[EventAggentPortKey]

		}
		timeStr := time.Unix(timestamp.(int64)/1000, 0).Format("15:04:05 2006/01/02")
		expectCnt["timestamp"] = timeStr

		gs.Equal(expectCnt, actualContent)
		gs.Equal(expectCount, actualCount)
	}()
	gs.sendTrap(expectCount)
	wg.Wait()
	fmt.Println("cancel")
	gs.Cancel()
	fmt.Println("run over")
}

func (gs *GatherSuit) sendTrap(count uint32) {
	// 初始化CMDB监控
	test.MakeWatcher()
	defer func() { test.CleanWatcher() }()
	ts := &gosnmp.GoSNMP{
		Target:    trapTestAddress,
		Port:      trapTestPort,
		Community: "public",
		Version:   trapTestVersion,
		Timeout:   2 * time.Millisecond,
		Retries:   3,
		MaxOids:   gosnmp.MaxOids,
	}

	if trapTestVersion == gosnmp.Version3 {
		ts.SecurityModel = gosnmp.UserSecurityModel
		ts.MsgFlags = gosnmp.AuthPriv
		ts.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 "test",
			AuthenticationProtocol:   gosnmp.SHA,
			AuthenticationPassphrase: "password",
			PrivacyProtocol:          gosnmp.DES,
			PrivacyPassphrase:        "password",
			AuthoritativeEngineBoots: 1,
			AuthoritativeEngineTime:  1,
			AuthoritativeEngineID:    string([]byte{0x80, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04}),
		}
	}

	err := ts.Connect()
	if err != nil {
		gs.Failf("sendTrap: ", "Connect() err: %v", err)
	}
	defer func() {
		_ = ts.Conn.Close()
	}()

	pdu := gosnmp.SnmpPDU{
		Name:  trapTestOid,
		Type:  gosnmp.OctetString,
		Value: trapTestPayload,
	}

	objOid, _ := getV1TrapOID(trapTestGenericTrap, trapTestSpecificTrap, trapTestEnterpriseOid)
	pduObj := gosnmp.SnmpPDU{
		Name:  "." + snmptrapOIDKey,
		Type:  gosnmp.ObjectIdentifier,
		Value: "." + objOid,
	}

	puds := make([]gosnmp.SnmpPDU, 0, 2)
	puds = append(puds, pdu)
	if trapTestVersion != gosnmp.Version1 {
		puds = append(puds, pduObj)
	}

	trap := gosnmp.SnmpTrap{
		Variables:    puds,
		Enterprise:   trapTestEnterpriseOid,
		AgentAddress: trapTestAgentAddress,
		GenericTrap:  trapTestGenericTrap,
		SpecificTrap: trapTestSpecificTrap,
		Timestamp:    trapTestTimestamp,
	}

	for i := 0; i < int(count); i++ {
		fmt.Println("sendTrap", i, time.Now())
		_, err = ts.SendTrap(trap)
		if err != nil {
			gs.Failf("", "SendTrap() err: %v", err)
			panic(err)
		}
	}
}

func TestRun(t *testing.T) {
	suite.Run(t, new(GatherSuit))
}

func TestMatchOid(t *testing.T) {
	testCases := map[string]struct {
		example string
		expect  string
	}{
		"asd": {
			example: ".0.0.0.0.1.8.3.3",
			expect:  "xxx.8.3.3",
		},
		"xx": {
			example: "1.3.56.1855.23.3.889.3.",
			expect:  "xyxyxx.23.3.889.3",
		},
		"other": {
			example: "1.3.56.1855.889.3.",
			expect:  "xyxyxx.889.3",
		},
		"test": {
			example: "1.3.6.1.2.3.1.233",
			expect:  "testabc",
		},
	}

	fakeOIDMap := map[string]string{
		"0.0.0.0.1":         "xxx",
		"1.3":               "yy",
		"1.3.56.1855":       "xyxyxx",
		"1.3.6.1.2.3.1.233": "testabc",
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			result := matchOid(testCase.example, fakeOIDMap)
			assert.Equal(t, testCase.expect, result, name)
		})
	}
}
