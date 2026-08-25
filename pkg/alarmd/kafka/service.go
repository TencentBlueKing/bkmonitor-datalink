// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
)

var (
	ErrDrainTimeout        = errors.New("kafka service: drain timeout")
	ErrServiceAlreadyRun   = errors.New("kafka service: Run may only be called once")
	errErrorsChannelClosed = errors.New("kafka service: consumer group errors channel closed while running")
	errConsumeStopped      = errors.New("kafka service: consume loop stopped while running")
)

type consumerGroup interface {
	Consume(context.Context, []string, sarama.ConsumerGroupHandler) error
	Errors() <-chan error
	Close() error
}

type serviceClient interface {
	Close() error
}

type serviceHandler interface {
	sarama.ConsumerGroupHandler
	BeginDrain() <-chan struct{}
	serviceSnapshot() assignmentSnapshot
}

type serviceHandlerFactory func(func(error)) (serviceHandler, error)

// OffsetReset records one partition whose committed offset was repaired to a
// retained broker offset before the next consumer-group session starts.
type OffsetReset struct {
	Topic     string
	Partition int32
	Offset    int64
}

// ConsumerDiagnostics exposes bounded recovery events without coupling the
// reusable Kafka service to a logging or metrics implementation.
type ConsumerDiagnostics struct {
	OnOffsetReset func(OffsetReset)
}

func (d ConsumerDiagnostics) offsetReset(event OffsetReset) {
	if d.OnOffsetReset != nil {
		d.OnOffsetReset(event)
	}
}

type Service struct {
	topics       []string
	group        consumerGroup
	client       serviceClient
	handler      serviceHandler
	drainTimeout time.Duration

	groupResource  ownedResource
	clientResource ownedResource

	normalCloseOnce    sync.Once
	normalCloseDone    chan struct{}
	normalCloseErr     error
	forcedCloseOnce    sync.Once
	forcedCloseStarted chan struct{}
	forcedCloseDone    chan struct{}
	forcedCloseErr     error
	forcedClientDone   chan struct{}
	forcedClientErr    error

	runMu   sync.Mutex
	started bool
	running atomic.Bool
	closing atomic.Bool

	drainOnce     sync.Once
	drainMu       sync.Mutex
	draining      bool
	drainRecorded bool
	forcedDrain   bool
	drainTotal    [lifecycle.DrainResultCount]uint64

	cancelMu      sync.Mutex
	cancelConsume context.CancelFunc
	cycleCancel   context.CancelFunc
	offsetReset   atomic.Bool
	diagnostics   ConsumerDiagnostics
	repairOffsets func(context.Context) ([]OffsetReset, error)

	fatalOnce   sync.Once
	fatalMu     sync.Mutex
	fatalErr    error
	fatal       chan error
	fatalSignal chan struct{}

	closeRequestOnce sync.Once
	closeRequested   chan struct{}
}

func OpenService(cfg Config, newProcessor consumer.ProcessorFactory, drainTimeout time.Duration) (*Service, error) {
	if newProcessor == nil {
		return nil, errors.New("kafka service: processor factory is required")
	}
	if drainTimeout <= 0 {
		return nil, errors.New("kafka service: drain timeout must be positive")
	}
	saramaConfig, err := NewSaramaConfig(cfg)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(cfg.Brokers, saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("kafka service: open client: %w", err)
	}
	group, err := sarama.NewConsumerGroupFromClient(cfg.GroupID, client)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka service: open consumer group: %w", err), client.Close())
	}
	offsets, err := NewSaramaOffsetCommitter(client, cfg.GroupID)
	if err != nil {
		return nil, errors.Join(err, group.Close(), client.Close())
	}
	service, err := newOwnedService(cfg.Topic, group, client, newProcessor, offsets, drainTimeout)
	if err != nil {
		return nil, errors.Join(err, group.Close(), client.Close())
	}
	service.diagnostics = cfg.Diagnostics
	service.repairOffsets = newGroupOffsetRepairer(client, cfg.GroupID, []string{cfg.Topic}, saramaConfig.Consumer.Offsets.Initial).Repair
	return service, nil
}

func newOwnedService(
	topic string,
	group consumerGroup,
	client serviceClient,
	newProcessor consumer.ProcessorFactory,
	offsets OffsetCommitter,
	drainTimeout time.Duration,
) (*Service, error) {
	if topic == "" || newProcessor == nil || offsets == nil {
		return nil, errors.New("kafka service: topic, group, client, processor factory and offset committer are required")
	}
	return newOwnedGroupService(
		[]string{topic}, group, client,
		func(reportFatal func(error)) (serviceHandler, error) {
			return NewHandler(newProcessor, offsets, reportFatal), nil
		},
		drainTimeout,
	)
}

func newOwnedGroupService(
	topics []string,
	group consumerGroup,
	client serviceClient,
	newHandler serviceHandlerFactory,
	drainTimeout time.Duration,
) (*Service, error) {
	if len(topics) == 0 || group == nil || client == nil || newHandler == nil {
		return nil, errors.New("kafka service: topics, group, client and handler factory are required")
	}
	if drainTimeout <= 0 {
		return nil, errors.New("kafka service: drain timeout must be positive")
	}
	ownedTopics := make([]string, len(topics))
	seenTopics := make(map[string]struct{}, len(topics))
	for index, topic := range topics {
		if err := validateKafkaTopicName("topics", topic); err != nil {
			return nil, err
		}
		if _, ok := seenTopics[topic]; ok {
			return nil, errors.New("kafka service: topics must be unique")
		}
		seenTopics[topic] = struct{}{}
		ownedTopics[index] = topic
	}
	service := &Service{
		topics:             ownedTopics,
		group:              group,
		client:             client,
		drainTimeout:       drainTimeout,
		normalCloseDone:    make(chan struct{}),
		forcedCloseStarted: make(chan struct{}),
		forcedCloseDone:    make(chan struct{}),
		forcedClientDone:   make(chan struct{}),
		fatal:              make(chan error, 1),
		fatalSignal:        make(chan struct{}),
		closeRequested:     make(chan struct{}),
	}
	service.groupResource.close = group.Close
	service.clientResource.close = client.Close
	handler, err := newHandler(service.reportFatal)
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("kafka service: handler factory returned nil")
	}
	service.handler = handler
	return service, nil
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.group == nil || s.client == nil || s.handler == nil {
		return errors.New("kafka service: initialized service is required")
	}
	if ctx == nil {
		return errors.New("kafka service: context is required")
	}
	s.runMu.Lock()
	if s.started {
		s.runMu.Unlock()
		return ErrServiceAlreadyRun
	}
	s.started = true
	if s.closing.Load() {
		s.runMu.Unlock()
		return errors.New("kafka service: service is closed")
	}
	if ctx.Err() != nil {
		s.closing.Store(true)
		s.runMu.Unlock()
		s.beginDrain()
		return s.closeResourcesWithin(false, time.Now().Add(s.drainTimeout))
	}
	s.running.Store(true)
	s.runMu.Unlock()

	defer s.running.Store(false)
	consumeContext, cancelConsume := context.WithCancel(context.Background())
	s.setCancelConsume(cancelConsume)
	defer func() {
		cancelConsume()
		s.setCancelConsume(nil)
	}()

	errorsReady := make(chan struct{})
	errorsDone := make(chan struct{})
	go s.drainErrors(errorsReady, errorsDone)
	<-errorsReady

	consumeDone := make(chan struct{})
	s.runMu.Lock()
	startConsume := !s.closing.Load() && ctx.Err() == nil && s.firstFatal() == nil
	if startConsume {
		go func() {
			defer close(consumeDone)
			if err := s.consumeLoop(consumeContext); err != nil {
				s.reportFatal(err)
			}
		}()
	} else {
		close(consumeDone)
	}
	s.runMu.Unlock()

	force := false
	select {
	case <-ctx.Done():
	case <-s.fatal:
	case <-s.closeRequested:
		force = true
	case <-consumeDone:
		if consumeContext.Err() == nil && !s.closing.Load() {
			s.reportFatal(errConsumeStopped)
		}
	}

	result := s.shutdown(cancelConsume, force, consumeDone, errorsDone)
	if fatal := s.firstFatal(); fatal != nil {
		result = errors.Join(fatal, result)
	}
	return result
}

func (s *Service) Ready() bool {
	return s.LifecycleSnapshot().Ready
}

// FatalSignal is closed after the first fatal error has made the service not
// ready and started draining. Multiple observers may wait on the same signal.
func (s *Service) FatalSignal() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.fatalSignal
}

// FatalError returns the first fatal service error, if one has been reported.
func (s *Service) FatalError() error {
	return s.firstFatal()
}

func (s *Service) LifecycleSnapshot() lifecycle.Snapshot {
	if s == nil || s.handler == nil {
		return lifecycle.Snapshot{}
	}
	assignment := s.handler.serviceSnapshot()
	fatal := s.firstFatal()
	s.drainMu.Lock()
	snapshot := lifecycle.Snapshot{
		Ready:              s.running.Load() && !s.closing.Load() && fatal == nil && assignment.ready,
		AssignedClaims:     assignment.assignedClaims,
		Draining:           s.draining,
		DrainTotal:         s.drainTotal,
		InflightRecords:    assignment.inflightRecords,
		ConsumerLagRecords: assignment.consumerLagRecords,
		ConsumerLagKnown:   assignment.consumerLagKnown,
	}
	s.drainMu.Unlock()
	if fatal != nil {
		snapshot.FatalTotal = 1
	}
	return snapshot
}

// Close is an idempotent forced shutdown. Run context cancellation is the
// graceful path and should be preferred by the process coordinator.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.runMu.Lock()
	s.closeRequestOnce.Do(func() { close(s.closeRequested) })
	s.closing.Store(true)
	s.runMu.Unlock()
	if s.handler != nil {
		s.beginDrain()
	}
	s.cancelCurrentConsume()
	return s.closeResourcesWithin(true, time.Now().Add(s.drainTimeout))
}

func (s *Service) consumeLoop(ctx context.Context) error {
	for {
		if s.firstFatal() != nil {
			return nil
		}
		if s.repairOffsets != nil {
			events, err := s.repairOffsets(ctx)
			if err != nil {
				return fmt.Errorf("kafka service: repair consumer offsets: %w", err)
			}
			for _, event := range events {
				s.diagnostics.offsetReset(event)
			}
		}
		cycleContext, cancelCycle := context.WithCancel(ctx)
		s.setCycleCancel(cancelCycle)
		if s.offsetReset.Load() {
			cancelCycle()
		}
		err := s.group.Consume(cycleContext, append([]string(nil), s.topics...), s.handler)
		cancelCycle()
		s.setCycleCancel(nil)
		if ctx.Err() != nil || s.closing.Load() || s.firstFatal() != nil {
			return nil
		}
		if s.offsetReset.Swap(false) {
			continue
		}
		if err != nil {
			return fmt.Errorf("kafka service: consume group: %w", err)
		}
	}
}

func (s *Service) drainErrors(ready chan<- struct{}, done chan<- struct{}) {
	defer close(done)
	errorsChannel := s.group.Errors()
	if errorsChannel == nil {
		if !s.closing.Load() {
			s.reportFatal(errors.New("kafka service: consumer group errors channel is nil"))
		}
		close(ready)
		return
	}
	select {
	case err, ok := <-errorsChannel:
		if !ok {
			if !s.closing.Load() {
				s.reportFatal(errErrorsChannelClosed)
			}
			close(ready)
			return
		}
		if err != nil && !s.closing.Load() {
			s.handleGroupError(err)
		}
	default:
	}
	close(ready)
	for err := range errorsChannel {
		if err != nil && !s.closing.Load() {
			s.handleGroupError(err)
		}
	}
	if !s.closing.Load() {
		s.reportFatal(errErrorsChannelClosed)
	}
}

func (s *Service) handleGroupError(err error) {
	var consumerError *sarama.ConsumerError
	if errors.As(err, &consumerError) &&
		(errors.Is(consumerError.Err, sarama.ErrOffsetOutOfRange) ||
			errors.Is(consumerError.Err, errComparatorSymbolicInitialOffset)) {
		s.offsetReset.Store(true)
		s.cancelCurrentCycle()
		return
	}
	s.reportFatal(fmt.Errorf("kafka service: consumer group error: %w", err))
}

func (s *Service) reportFatal(err error) {
	if err == nil {
		return
	}
	s.fatalOnce.Do(func() {
		s.fatalMu.Lock()
		s.fatalErr = err
		s.fatalMu.Unlock()
		if s.handler != nil {
			s.beginDrain()
		}
		close(s.fatalSignal)
		s.fatal <- err
	})
}

func (s *Service) firstFatal() error {
	if s == nil {
		return nil
	}
	s.fatalMu.Lock()
	defer s.fatalMu.Unlock()
	return s.fatalErr
}

func (s *Service) shutdown(
	cancelConsume context.CancelFunc,
	force bool,
	consumeDone <-chan struct{},
	errorsDone <-chan struct{},
) error {
	s.closing.Store(true)
	drained := s.beginDrain()
	deadline := time.Now().Add(s.drainTimeout)
	if force {
		cancelConsume()
		return s.waitRuntimeClose(true, deadline, consumeDone, errorsDone)
	}
	if !waitUntil(drained, deadline) {
		cancelConsume()
		s.startForcedClose()
		s.recordDrain(lifecycle.DrainTimeout)
		return ErrDrainTimeout
	}
	cancelConsume()
	return s.waitRuntimeClose(false, deadline, consumeDone, errorsDone)
}

func (s *Service) closeNormal() error {
	groupErr := s.groupResource.Close()
	clientErr := s.clientResource.Close()
	if s.forcedCloseHasStarted() && errors.Is(groupErr, sarama.ErrClosedClient) {
		groupErr = nil
	}
	return errors.Join(groupErr, clientErr)
}

func (s *Service) waitRuntimeClose(
	force bool,
	deadline time.Time,
	consumeDone <-chan struct{},
	errorsDone <-chan struct{},
) error {
	var closeDone <-chan struct{}
	if force {
		closeDone = s.startForcedClose()
	} else {
		closeDone = s.startNormalClose()
	}
	var closeErr error
	for closeDone != nil || consumeDone != nil || errorsDone != nil {
		wait := time.Until(deadline)
		if wait <= 0 {
			if !force {
				s.startForcedClose()
			}
			knownCloseErr := s.knownForcedClientError()
			s.recordDrain(drainResult(force, errors.Join(closeErr, knownCloseErr), true))
			return errors.Join(ErrDrainTimeout, closeErr, knownCloseErr)
		}
		timer := time.NewTimer(wait)
		select {
		case <-closeDone:
			if force {
				closeErr = s.forcedCloseErr
			} else {
				closeErr = s.normalCloseErr
			}
			closeDone = nil
		case <-consumeDone:
			consumeDone = nil
		case <-errorsDone:
			errorsDone = nil
		case <-timer.C:
			if !force {
				s.startForcedClose()
			}
			knownCloseErr := s.knownForcedClientError()
			s.recordDrain(drainResult(force, errors.Join(closeErr, knownCloseErr), true))
			return errors.Join(ErrDrainTimeout, closeErr, knownCloseErr)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	s.recordDrain(drainResult(force, closeErr, false))
	return closeErr
}

func (s *Service) closeResourcesWithin(force bool, deadline time.Time) error {
	var done <-chan struct{}
	if force {
		done = s.startForcedClose()
	} else {
		done = s.startNormalClose()
	}
	wait := time.Until(deadline)
	if wait <= 0 {
		if !force {
			s.startForcedClose()
		}
		knownCloseErr := s.knownForcedClientError()
		s.recordDrain(drainResult(force, knownCloseErr, true))
		return errors.Join(ErrDrainTimeout, knownCloseErr)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		if force {
			s.recordDrain(drainResult(true, s.forcedCloseErr, false))
			return s.forcedCloseErr
		}
		s.recordDrain(drainResult(false, s.normalCloseErr, false))
		return s.normalCloseErr
	case <-timer.C:
		if !force {
			s.startForcedClose()
		}
		knownCloseErr := s.knownForcedClientError()
		s.recordDrain(drainResult(force, knownCloseErr, true))
		return errors.Join(ErrDrainTimeout, knownCloseErr)
	}
}

func (s *Service) beginDrain() <-chan struct{} {
	if s == nil || s.handler == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	s.drainMu.Lock()
	if !s.drainRecorded {
		s.draining = true
	}
	s.drainMu.Unlock()
	return s.handler.BeginDrain()
}

func (s *Service) recordDrain(result lifecycle.DrainResult) {
	if s == nil {
		return
	}
	s.drainOnce.Do(func() {
		s.drainMu.Lock()
		if s.forcedDrain && result == lifecycle.DrainSuccess {
			result = lifecycle.DrainTimeout
		}
		select {
		case <-s.forcedCloseDone:
			if s.forcedCloseErr != nil {
				result = lifecycle.DrainFailed
			}
		default:
		}
		if result >= lifecycle.DrainResultCount {
			result = lifecycle.DrainOther
		}
		s.drainTotal[result]++
		s.drainRecorded = true
		s.draining = false
		s.drainMu.Unlock()
	})
}

func drainResult(force bool, closeErr error, timedOut bool) lifecycle.DrainResult {
	if closeErr != nil {
		return lifecycle.DrainFailed
	}
	if force || timedOut {
		return lifecycle.DrainTimeout
	}
	return lifecycle.DrainSuccess
}

func (s *Service) startNormalClose() <-chan struct{} {
	s.normalCloseOnce.Do(func() {
		go func() {
			s.normalCloseErr = s.closeNormal()
			close(s.normalCloseDone)
		}()
	})
	return s.normalCloseDone
}

func (s *Service) startForcedClose() <-chan struct{} {
	s.forcedCloseOnce.Do(func() {
		s.drainMu.Lock()
		s.forcedDrain = true
		close(s.forcedCloseStarted)
		s.drainMu.Unlock()
		go func() {
			s.forcedClientErr = s.clientResource.Close()
			close(s.forcedClientDone)
			groupErr := s.groupResource.Close()
			if errors.Is(groupErr, sarama.ErrClosedClient) {
				groupErr = nil
			}
			s.forcedCloseErr = errors.Join(s.forcedClientErr, groupErr)
			close(s.forcedCloseDone)
		}()
	})
	return s.forcedCloseDone
}

func (s *Service) knownForcedClientError() error {
	select {
	case <-s.forcedClientDone:
		return s.forcedClientErr
	default:
		return nil
	}
}

func (s *Service) forcedCloseHasStarted() bool {
	select {
	case <-s.forcedCloseStarted:
		return true
	default:
		return false
	}
}

func (s *Service) setCancelConsume(cancel context.CancelFunc) {
	s.cancelMu.Lock()
	s.cancelConsume = cancel
	s.cancelMu.Unlock()
}

func (s *Service) setCycleCancel(cancel context.CancelFunc) {
	s.cancelMu.Lock()
	s.cycleCancel = cancel
	s.cancelMu.Unlock()
}

func (s *Service) cancelCurrentCycle() {
	s.cancelMu.Lock()
	cancel := s.cycleCancel
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) cancelCurrentConsume() {
	s.cancelMu.Lock()
	cancel := s.cancelConsume
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func waitUntil(done <-chan struct{}, deadline time.Time) bool {
	wait := time.Until(deadline)
	if wait <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

type ownedResource struct {
	once  sync.Once
	close func() error
	err   error
}

func (r *ownedResource) Close() error {
	if r == nil || r.close == nil {
		return nil
	}
	r.once.Do(func() { r.err = r.close() })
	return r.err
}
