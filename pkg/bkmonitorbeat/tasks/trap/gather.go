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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/gosnmp/gosnmp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Gather :
type Gather struct {
	tasks.BaseTask
	udpLostCount int64
	output       chan<- *Event
	communityMap map[string]bool
}

type Event struct {
	dataid      int32
	eventName   string
	target      string
	content     map[string]string
	dimension   map[string]string
	metrics     map[string]uint32
	timestamp   int64
	labels      []configs.Label
	hashContent string
}

func NewTrapEvent(tasks define.Task) *Event {
	return &Event{
		dataid:    tasks.GetConfig().GetDataID(),
		eventName: snmptrapOID,
		target:    "",
		content:   map[string]string{},
		dimension: nil,
		metrics:   nil,
		timestamp: time.Now().Unix(),
		labels:    nil,
	}
}

func (e *Event) toMapStr() common.MapStr {
	mapInter := make(map[string]interface{})

	for key, val := range e.dimension {
		mapInter[key] = val
	}

	contentData, err := json.Marshal(e.content)
	if err != nil {
		logger.Errorf("marshal content failed,error:%s", err)
		contentData = []byte("")
	}

	return common.MapStr{
		"event_name": snmptrapOID,
		"event": common.MapStr{
			"content": string(contentData),
			"count":   e.metrics["count"],
		},
		"dimension": common.MapStr(mapInter),
		"target":    e.target,
		//"metrics":   e.metrics,
		// 单位为毫秒
		"timestamp": e.timestamp * 1000,
	}
}

func matchOid(oid string, oidMap map[string]string) string {
	var (
		prefix  string
		ok      bool
		val     string
		subOids = strings.Split(strings.Trim(oid, "."), ".")
	)

	for i := len(subOids); i > 0; i-- {
		prefix = strings.Join(subOids[:i], ".")
		val, ok = oidMap[prefix]
		if !ok {
			continue
		}
		// 存在oid完整匹配的场景，此时不应该在最后加.
		if i == len(subOids) {
			return val
		}
		return val + "." + strings.Join(subOids[i:], ".")
	}
	return oid
}

func snmpVersionToStr(version gosnmp.SnmpVersion) string {
	switch version {
	case gosnmp.Version1:
		return "v1"
	case gosnmp.Version2c:
		return "v2c"
	case gosnmp.Version3:
		return "v3"
	}
	return "unkown version"
}

func getV1TrapOID(genericTrap, specificTrap int, enterprise string) (oid, oidValue string) {
	switch genericTrap {
	case coldStart:
		return ".1.3.6.1.6.3.1.1.5.1", "coldStart"
	case warmStart:
		return ".1.3.6.1.6.3.1.1.5.2", "warmStart"
	case linkDown:
		return ".1.3.6.1.6.3.1.1.5.3", "linkDown"
	case linkUp:
		return ".1.3.6.1.6.3.1.1.5.4", "linkUp"
	case authenticationFailure:
		return ".1.3.6.1.6.3.1.1.5.5", "authenticationFailure"
	case egpNeighborLoss:
		return ".1.3.6.1.6.3.1.1.5.6", "egpNeighborLoss"
	case enterpriseSpecific:
		return fmt.Sprintf("%s.0.%d", enterprise, specificTrap), ""
	default:
		return "", ""
	}
}

// checkCommunity 检查community
func (g *Gather) checkCommunity(value, expect string) bool {
	if expect != "" {
		if value == "" && g.communityMap[allowCommunityEmptyKey] {
			// pass
		} else {
			if has, ok := g.communityMap[value]; !has || !ok {
				logger.Warnf("drop trap because the community:[%s] is not the same with task:[%s]", value, expect)
				return false
			}
		}
	}
	return true
}

// getInternalDimensions 生成内部维度信息
func (g *Gather) getInternalDimensions(
	packet *gosnmp.SnmpPacket, conf *configs.TrapConfig, addr *net.UDPAddr,
	trapOid, displayName string,
) map[string]string {
	internalDimensions := make(map[string]string)
	internalDimensions[EventCommunityKey] = packet.Community
	internalDimensions[EventVersionKey] = snmpVersionToStr(packet.Version)
	internalDimensions[EventEnterpriseKey] = packet.Enterprise
	internalDimensions[EventGenericTrapKey] = strconv.Itoa(packet.GenericTrap)
	internalDimensions[EventSpecificTrapKey] = strconv.Itoa(packet.SpecificTrap)
	internalDimensions[EventSnmpTrapOIDKey] = trapOid
	internalDimensions[EventDisplayNameKey] = displayName
	internalDimensions[EventAgentAddressKey] = addr.IP.String()
	if !conf.HideAgentPort {
		// agent port是随机的，没有记录的意义,但存在客户使用它做事件拆分的场景，所以上报
		internalDimensions[EventAggentPortKey] = strconv.Itoa(addr.Port)
	}

	internalDimensions[EventServerIPKey] = conf.IP
	internalDimensions[EventServerPortKey] = strconv.Itoa(conf.Port)
	return internalDimensions
}

func getTrapOidAndDisplayName(packet *gosnmp.SnmpPacket, oids map[string]string) (string, string) {
	var trapOid string
	var displayName string

	if packet.Version == gosnmp.Version1 {
		trapOid, displayName = getV1TrapOID(packet.GenericTrap, packet.SpecificTrap, packet.Enterprise)
		if displayName == "" {
			displayName = matchOid(trapOid, oids)
		}
	}
	return trapOid, displayName
}

func getValueByEncoding(b []byte, encoding string) (string, error) {
	result, err := encodeFunc[encoding](b)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func updateDimension(conf *configs.TrapConfig, v gosnmp.SnmpPDU, value string, dimension map[string]string) {
	for _, reportOID := range conf.ReportOIDDimensions {
		isIndexOID := strings.HasSuffix(reportOID, ".index")
		realReportOID := reportOID
		if isIndexOID {
			realReportOID = strings.TrimRight(reportOID, ".index")
		}
		// 如果oid的前缀匹配的上,就将其加入到维度内容里
		if strings.HasPrefix(v.Name, realReportOID) {
			// 额外增加一个规则，防止出现1.3.6.1.1234匹配进1.3.6.1.1的组里的场景
			outMatchedOID := strings.TrimLeft(v.Name, realReportOID)
			// 完整前缀或全量匹配
			if strings.HasPrefix(outMatchedOID, ".") || len(outMatchedOID) == 0 {
				// 如果是index类型的oid配置，则进行index层级调整
				if isIndexOID {
					// 先判断两个是否确实是同一个层级
					lastOIDPoint := strings.LastIndex(v.Name, ".")
					if v.Name[:lastOIDPoint] == realReportOID {
						oidPrefix := v.Name[:lastOIDPoint]
						oidIndex := v.Name[strings.LastIndex(v.Name, ".")+1:]
						var name string
						// 启用开关，则维度进行翻译,否则使用原始oid上报到维度里
						if conf.UseDisplayNameOID {
							name = strings.Replace(matchOid(oidPrefix, conf.OIDS), ".", "_", -1)
						} else {
							name = strings.Replace(strings.Trim(oidPrefix, "."), ".", "_", -1)
						}
						value = oidIndex + "::::" + value
						dimension[name] = value
						continue
					} else {
						logger.Debugf("trap", "index oid not matched:%s,%s", v.Name[:lastOIDPoint], realReportOID)
					}
				}

				// 将.转换为_是规避es的path规则
				var name string
				// 启用开关，则维度进行翻译,否则使用原始oid上报到维度里
				if conf.UseDisplayNameOID {
					name = strings.Replace(matchOid(v.Name, conf.OIDS), ".", "_", -1)
				} else {
					name = strings.Replace(strings.Trim(v.Name, "."), ".", "_", -1)
				}
				dimension[name] = value
			}
		}
	}
}

func (g *Gather) getEvent(conf *configs.TrapConfig, packet *gosnmp.SnmpPacket, addr *net.UDPAddr) *Event {
	dimension := make(map[string]string)
	contentMap := make(map[string]string)

	rawByteOIDMap := make(map[string]bool)
	for _, rawByteOID := range conf.RawByteOIDs {
		rawByteOIDMap[rawByteOID] = true
	}
	trapOid, displayName := getTrapOidAndDisplayName(packet, conf.OIDS)

	for _, v := range packet.Variables {
		var value string
		switch v.Type {
		case gosnmp.OctetString:
			b := v.Value.([]byte)
			s, err := getValueByEncoding(b, conf.Encode)
			if err != nil {
				logger.Errorf("decode value failed,error:%s", err)
				continue
			}
			value = s

		case gosnmp.ObjectIdentifier:
			b := v.Value.(string)
			trapOid = b
			displayName = matchOid(b, conf.OIDS)
			continue
		default:
			value = fmt.Sprintf("%v", v.Value)
		}

		// 如果是不需要翻译的，直接打印内容即可
		if _, ok := rawByteOIDMap[v.Name]; ok {
			value = fmt.Sprintf("%v", v.Value)
		}
		contentMap[fmt.Sprintf("%s(%s)", matchOid(v.Name, conf.OIDS), v.Name)] = value

		// 如果指定了oid，则将对应oid加入维度里
		updateDimension(conf, v, value, dimension)
	}

	internalDimensions := g.getInternalDimensions(packet, conf, addr, trapOid, displayName)

	// 将维度信息也加入到content里
	for key, value := range internalDimensions {
		contentMap[key] = value
		dimension[key] = value
	}
	event := NewTrapEvent(g)
	event.target = conf.Target
	timestamp := time.Unix(event.timestamp, 0).Format("15:04:05 2006/01/02")
	// [timestamp] [trapOID trapVal] [VariablesName VariablesVal] [address]
	// 11:30:15 2011/07/27 .1.3.6.1.6.3.1.1.5.3 Normal sysUptime(.1.3.6.1.2.1.1.3.0) 567890 127.0.0.1
	contentMap[EventTimestampKey] = timestamp

	metrics := make(map[string]uint32)
	metrics["count"]++

	// 生成hashkey依据的内容
	contentMapKeys := make([]string, 0, len(contentMap))
	for key := range contentMap {
		switch key {
		case sysUptimeOid, EventAggentPortKey, EventTimestampKey:
			continue
		default:
			contentMapKeys = append(contentMapKeys, key)
		}
	}
	sort.Strings(contentMapKeys)
	for _, contentMapKey := range contentMapKeys {
		event.hashContent += fmt.Sprintf("%s %s ", contentMapKey, dimension[contentMapKey])
	}

	event.labels = conf.Label
	event.dimension = dimension
	event.metrics = metrics
	event.content = contentMap

	return event
}

// TrapHandler 处理主逻辑
func (g *Gather) TrapHandler(packet *gosnmp.SnmpPacket, addr *net.UDPAddr) {
	logger.Debugf("got trapdata from %s:%d", addr.IP, addr.Port)
	logger.Debugf("got trapdata %#v", packet)
	logger.Debugf("got trapdata.Variables %#v", packet.Variables)
	conf := g.TaskConfig.(*configs.TrapConfig)

	// v1,v2可以验证Community，验证失败则不处理上报
	if packet.Version != gosnmp.Version3 {
		g.checkCommunity(packet.Community, conf.Community)
	}

	event := g.getEvent(conf, packet, addr)
	logger.Debugf("send trap event: %v", event.toMapStr())
	g.output <- event
}

func (g *Gather) getMsgFlags(flag string) gosnmp.SnmpV3MsgFlags {
	switch strings.ToLower(flag) {
	case "authnopriv":
		return gosnmp.AuthNoPriv
	case "authpriv":
		return gosnmp.AuthPriv
	case "reportable":
		return gosnmp.Reportable
	default:
		return gosnmp.NoAuthNoPriv
	}
}

func (g *Gather) parseEnginID(usmConf configs.USMConfig) ([]byte, error) {
	id := usmConf.AuthoritativeEngineID
	reader := bytes.NewReader([]byte(id))
	resultID := make([]byte, 0)
	buf := make([]byte, 2)
	for {
		num, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if num != 2 {
			return nil, fmt.Errorf("wrong length of enging ID")
		}
		i, err := strconv.ParseInt(string(buf), 16, 64)
		if err != nil {
			return nil, err
		}
		resultID = append(resultID, byte(i))
	}
	return resultID, nil
}

func (g *Gather) getAuthenticationProtocol(usmConf configs.USMConfig) gosnmp.SnmpV3AuthProtocol {
	auth := usmConf.AuthenticationProtocol
	switch strings.ToLower(auth) {
	case "md5":
		return gosnmp.MD5
	case "sha":
		return gosnmp.SHA
	case "sha224":
		return gosnmp.SHA224
	case "sha256":
		return gosnmp.SHA256
	case "sha384":
		return gosnmp.SHA384
	case "sha512":
		return gosnmp.SHA512
	default:
		return gosnmp.NoAuth
	}
}

func (g *Gather) getPrivacyProtocol(usmConf configs.USMConfig) gosnmp.SnmpV3PrivProtocol {
	privacy := usmConf.PrivacyProtocol
	switch strings.ToLower(privacy) {
	case "des":
		return gosnmp.DES
	case "aes":
		return gosnmp.AES
	case "aes192":
		return gosnmp.AES192
	case "aes192c":
		return gosnmp.AES192C
	case "aes256":
		return gosnmp.AES256
	case "aes256c":
		return gosnmp.AES256C
	default:
		return gosnmp.NoPriv
	}
}

func (g *Gather) getUSMConfig(usmConf configs.USMConfig) (*gosnmp.UsmSecurityParameters, error) {
	engineID, err := g.parseEnginID(usmConf)
	if err != nil {
		return nil, err
	}
	if usmConf.AuthoritativeEngineBoots == 0 {
		usmConf.AuthoritativeEngineBoots = 1
	}
	if usmConf.AuthoritativeEngineTime == 0 {
		usmConf.AuthoritativeEngineTime = 1
	}
	sp := &gosnmp.UsmSecurityParameters{
		UserName:                 usmConf.UserName,
		AuthenticationProtocol:   g.getAuthenticationProtocol(usmConf),
		AuthenticationPassphrase: usmConf.AuthenticationPassphrase,
		PrivacyProtocol:          g.getPrivacyProtocol(usmConf),
		PrivacyPassphrase:        usmConf.PrivacyPassphrase,
		AuthoritativeEngineBoots: usmConf.AuthoritativeEngineBoots,
		AuthoritativeEngineTime:  usmConf.AuthoritativeEngineTime,
		AuthoritativeEngineID:    string(engineID),
	}
	return sp, nil
}

func (g *Gather) getSnmpVersion() gosnmp.SnmpVersion {
	conf := g.TaskConfig.(*configs.TrapConfig)
	version := conf.Version
	switch strings.ToLower(version) {
	case "v1", "1":
		return gosnmp.Version1
	case "v2", "v2c", "2", "2c":
		return gosnmp.Version2c
	case "v3", "3":
		return gosnmp.Version3
	default:
		logger.Errorf("error snmp version: %s", version)
		return unKownTrapVersion
	}
}

func (g *Gather) initTrapListener() (*gosnmp.TrapListener, error) {
	tl := gosnmp.NewTrapListener()
	// 绑定处理方法
	conf := g.TaskConfig.(*configs.TrapConfig)
	logger.Debugf("init trap listenser with config : %v", conf)
	tl.OnNewTrap = g.TrapHandler
	tl.Params = gosnmp.Default

	// 切割 community，支持多community校验，若切出来有空字符串，则校验时允许community为空的trap通过
	comList := strings.Split(conf.Community, ",")
	tl.Params.Community = conf.Community
	g.communityMap = make(map[string]bool, len(comList))
	for _, comItem := range comList {
		if comItem == "" {
			g.communityMap[allowCommunityEmptyKey] = true
			continue
		}
		g.communityMap[comItem] = true
	}
	// 默认将v1,v2 community 为空设置为false
	if _, ok := g.communityMap[allowCommunityEmptyKey]; !ok {
		g.communityMap[allowCommunityEmptyKey] = false
	}

	version := g.getSnmpVersion()
	if version == unKownTrapVersion {
		return nil, ErrWrongVersion
	}
	tl.Params.Version = version

	// 只有snmp v3版本才需要认证
	if version == gosnmp.Version3 {
		setDefault := false
		tl.Params.SecurityModel = gosnmp.UserSecurityModel
		for _, usmInfo := range conf.UsmInfos {

			msgFlags := g.getMsgFlags(usmInfo.MsgFlags)
			sp, err := g.getUSMConfig(usmInfo.USMConfig)
			if err != nil {
				logger.Errorf("get usm config failed,error:%s", err)
				return nil, err
			}

			if !setDefault {
				tl.Params.MsgFlags = msgFlags
				tl.Params.ContextName = usmInfo.ContextName
				tl.Params.SecurityParameters = sp
				setDefault = true
			}
			if err = tl.Params.BkUsmMap.AddBkUsm(msgFlags, sp); err != nil {
				logger.Errorf("add Usm config error:%s", err)
				return nil, err
			}
		}
	}

	return tl, nil
}

// Run :
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tl, err := g.initTrapListener()
	if err != nil {
		logger.Errorf("error in init trap: %s", err)
		return
	}

	conf := g.TaskConfig.(*configs.TrapConfig)
	addrFormat := "%s:%d"
	if ipv4 := net.ParseIP(conf.IP).To4(); ipv4 == nil {
		addrFormat = "[%s]:%d"
	}
	s := fmt.Sprintf(addrFormat, conf.IP, conf.Port)

	output := make(chan *Event)
	logger.Debugf("new snmptrap_task sender with period: [%s]", conf.Period.String())
	sender := NewSender(conf.Period, conf.IsAggregate, e, ctx)
	sender.SetInput(output)
	g.output = output

	go func() {
		<-ctx.Done()
		logger.Debugf("task:[%d] done", g.TaskConfig.GetDataID())
		tl.Close()
	}()

	go g.watchUdpLost(ctx)

	go sender.Run()
	defer sender.Stop()

	tl.Concurrency = conf.Concurrency
	err = tl.Listen(s)
	if err != nil {
		logger.Errorf("error in listen: %s", err)
		return
	}
}
