// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package networkflow

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"sync"

	flowproducer "github.com/netsampler/goflow2/v2/producer"
	flowutils "github.com/netsampler/goflow2/v2/utils"
	flowtemplates "github.com/netsampler/goflow2/v2/utils/templates"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultUDPWorkers   = 1
	defaultUDPSockets   = 1
	defaultUDPBlocking  = false
	defaultUDPQueueSize = 0
)

type listenerSpec struct {
	Scheme   string
	Hostname string
	Port     int
}

type runtime struct {
	config    config
	publish   RecordPublisher
	specs     []listenerSpec
	listeners []listenerRuntime
	stopCh    chan struct{}
	errWg     sync.WaitGroup
}

type decodeFailureLog struct {
	DataID              int32  `json:"dataid"`
	Src                 string `json:"src"`
	Dst                 string `json:"dst"`
	PayloadLen          int    `json:"payload_len"`
	Version             string `json:"version,omitempty"`
	PacketType          string `json:"packet_type,omitempty"`
	ObservationDomainID string `json:"observation_domain_id,omitempty"`
	TemplateID          string `json:"template_id,omitempty"`
	Error               string `json:"error"`
}

var flowErrorPattern = regexp.MustCompile(`\[version:(\d+) type:([^\s]+) obsDomainId:([^:]+): templateId:(\d+)\]`)

type listenerRuntime struct {
	spec     listenerSpec
	receiver *flowutils.UDPReceiver
	pipe     flowutils.FlowPipe
	producer *flowRecordProducer
}

func newRuntime(cfg config, publish RecordPublisher) (runtimeHandle, error) {
	if len(cfg.Listeners) == 0 {
		return nil, fmt.Errorf("networkflow listeners are required")
	}

	specs, err := parseListeners(cfg.Listeners)
	if err != nil {
		return nil, err
	}

	return &runtime{
		config:  cfg,
		publish: publish,
		specs:   specs,
	}, nil
}

func (r *runtime) Start() error {
	r.stopCh = make(chan struct{})
	r.listeners = make([]listenerRuntime, 0, len(r.specs))

	for _, spec := range r.specs {
		prod, err := newFlowRecordProducer(r.config.DataID, r.publish)
		if err != nil {
			r.closeStopCh()
			return err
		}

		pipe := newFlowPipe(spec.Scheme, prod)

		receiver, err := flowutils.NewUDPReceiver(&flowutils.UDPReceiverConfig{
			Workers:   defaultUDPWorkers,
			Sockets:   defaultUDPSockets,
			Blocking:  defaultUDPBlocking,
			QueueSize: defaultUDPQueueSize,
		})
		if err != nil {
			pipe.Close()
			prod.Close()
			r.closeStopCh()
			return err
		}

		if err := receiver.Start(spec.Hostname, spec.Port, filterDecoder(r.config.DataID, pipe.DecodeFlow)); err != nil {
			pipe.Close()
			prod.Close()
			r.closeStopCh()
			_ = r.shutdownListeners()
			return err
		}

		lr := listenerRuntime{
			spec:     spec,
			receiver: receiver,
			pipe:     pipe,
			producer: prod,
		}
		r.listeners = append(r.listeners, lr)
		r.watchErrors(lr)
	}
	return nil
}

func (r *runtime) Stop() error {
	r.closeStopCh()
	err := r.shutdownListeners()
	r.errWg.Wait()
	r.listeners = nil
	return err
}

func (r *runtime) shutdownListeners() error {
	var stopErr error
	for _, lr := range r.listeners {
		if lr.receiver != nil {
			if err := lr.receiver.Stop(); err != nil {
				stopErr = stderrors.Join(stopErr, err)
			}
		}
		if lr.pipe != nil {
			lr.pipe.Close()
		}
		if lr.producer != nil {
			lr.producer.Close()
		}
	}
	return stopErr
}

func (r *runtime) watchErrors(lr listenerRuntime) {
	r.errWg.Add(1)
	go func() {
		defer r.errWg.Done()
		for {
			select {
			case <-r.stopCh:
				return
			case err, ok := <-lr.receiver.Errors():
				if !ok {
					return
				}
				if err != nil {
					logger.Warnf(
						"networkflow listener %s://%s:%d got err: %v",
						lr.spec.Scheme,
						lr.spec.Hostname,
						lr.spec.Port,
						err,
					)
				}
			}
		}
	}()
}

func filterDecoder(dataID int32, next flowutils.DecoderFunc) flowutils.DecoderFunc {
	return func(msg interface{}) error {
		packet, ok := msg.(*flowutils.Message)
		if !ok {
			return fmt.Errorf("flow is not *utils.Message")
		}
		logger.Debugf(
			"networkflow packet received, dataid=%d, src=%s, dst=%s, payload_len=%d",
			dataID,
			packet.Src.String(),
			packet.Dst.String(),
			len(packet.Payload),
		)
		if next == nil {
			return nil
		}
		err := next(msg)
		if err != nil {
			logger.Debugf("networkflow packet decode failed, dataid=%d, src=%s, err=%v", dataID, packet.Src.String(), err)
			logger.Debugf("networkflow packet decode failed details: %s", marshalDecodeFailureLog(dataID, packet, err))
		}
		return err
	}
}

func marshalDecodeFailureLog(dataID int32, packet *flowutils.Message, err error) string {
	entry := decodeFailureLog{
		DataID:     dataID,
		Src:        packet.Src.String(),
		Dst:        packet.Dst.String(),
		PayloadLen: len(packet.Payload),
		Error:      err.Error(),
	}
	if matches := flowErrorPattern.FindStringSubmatch(err.Error()); len(matches) == 5 {
		entry.Version = matches[1]
		entry.PacketType = matches[2]
		entry.ObservationDomainID = matches[3]
		entry.TemplateID = matches[4]
	}
	b, marshalErr := json.Marshal(entry)
	if marshalErr != nil {
		return fmt.Sprintf(`{"dataid":%d,"src":%q,"dst":%q,"payload_len":%d,"error":%q,"marshal_error":%q}`,
			dataID,
			packet.Src.String(),
			packet.Dst.String(),
			len(packet.Payload),
			err.Error(),
			marshalErr.Error(),
		)
	}
	return string(b)
}

func normalizeAddr(addr netip.Addr) netip.Addr {
	if addr.Is4In6() {
		return addr.Unmap()
	}
	return addr
}

func (r *runtime) closeStopCh() {
	if r.stopCh == nil {
		return
	}
	select {
	case <-r.stopCh:
	default:
		close(r.stopCh)
	}
}

func parseListeners(uris []string) ([]listenerSpec, error) {
	specs := make([]listenerSpec, 0, len(uris))
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parse listener %q: %w", raw, err)
		}

		scheme := u.Scheme
		switch scheme {
		case "netflow", "ipfix", "sflow", "flow":
		default:
			return nil, fmt.Errorf("unsupported networkflow listener scheme %q", scheme)
		}

		host, portStr, err := net.SplitHostPort(u.Host)
		if err != nil {
			return nil, fmt.Errorf("parse listener %q host/port: %w", raw, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("parse listener %q port: %w", raw, err)
		}

		specs = append(specs, listenerSpec{
			Scheme:   scheme,
			Hostname: host,
			Port:     port,
		})
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("networkflow listeners are required")
	}
	return specs, nil
}

func newFlowPipe(scheme string, prod flowproducer.ProducerInterface) flowutils.FlowPipe {
	cfg := &flowutils.PipeConfig{Producer: prod}
	switch scheme {
	case "sflow":
		return flowutils.NewSFlowPipe(cfg)
	case "flow":
		cfg.NetFlowTemplater = flowtemplates.DefaultTemplateGenerator
		return flowutils.NewFlowPipe(cfg)
	default:
		cfg.NetFlowTemplater = flowtemplates.DefaultTemplateGenerator
		return flowutils.NewNetFlowPipe(cfg)
	}
}
