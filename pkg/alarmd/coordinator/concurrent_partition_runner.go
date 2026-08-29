// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type RoutedMessageTask interface {
	RuntimeKeys() []RuntimeKey
	Prepare(context.Context) error
	Evaluate(context.Context) (MessageOutcome, error)
}

type RoutedMessageTaskBuilder interface {
	BuildMessageTask(context.Context, []byte) (RoutedMessageTask, error)
}

type outcomeRouterTaskBuilder struct {
	router MessageOutcomeRouter
}

// AdaptMessageOutcomeRouter preserves the legacy router boundary for tests and
// non-FULL messages. Production FULL evaluation uses MessageRouter's staged
// BuildMessageTask path so RuntimeKeys are known before concurrent work starts.
func AdaptMessageOutcomeRouter(router MessageOutcomeRouter) RoutedMessageTaskBuilder {
	return &outcomeRouterTaskBuilder{router: router}
}

func (builder *outcomeRouterTaskBuilder) BuildMessageTask(
	_ context.Context,
	payload []byte,
) (RoutedMessageTask, error) {
	if builder == nil || builder.router == nil {
		return nil, errors.New("alarmd coordinator: message outcome router is required")
	}
	return newDeferredMessageTask(func(ctx context.Context) (MessageOutcome, error) {
		return builder.router.Route(ctx, payload)
	}), nil
}

type ConcurrentRunnerLimits struct {
	PreparationWorkers       int
	StatefulWorkers          int
	MaxInflightMessages      int
	MaxInflightBytes         int
	MaxRuntimeKeysPerMessage int
	MaxPendingKeyRefs        int
}

func DefaultConcurrentRunnerLimits() ConcurrentRunnerLimits {
	return ConcurrentRunnerLimits{
		PreparationWorkers: 4, StatefulWorkers: 4, MaxInflightMessages: 16,
		MaxInflightBytes: 8 << 20, MaxRuntimeKeysPerMessage: 8_192, MaxPendingKeyRefs: 128_000,
	}
}

func (limits ConcurrentRunnerLimits) validate() error {
	if limits.PreparationWorkers <= 0 || limits.StatefulWorkers <= 0 || limits.MaxInflightMessages <= 0 ||
		limits.MaxInflightBytes <= 0 || limits.MaxRuntimeKeysPerMessage <= 0 || limits.MaxPendingKeyRefs <= 0 {
		return errors.New("alarmd coordinator: concurrent runner limits must be positive")
	}
	return nil
}

type ConcurrentRunnerCallbacks struct {
	OnTaskFinished func(offset int64, err error)
}

// ConcurrentRunnerResources owns the process-wide worker and admission
// budgets shared by every assigned Kafka partition. Partition-local ordering
// and completion state remain on ConcurrentRoutedPartitionRunner.
type ConcurrentRunnerResources struct {
	limits ConcurrentRunnerLimits

	preparationTokens chan struct{}
	statefulTokens    chan struct{}
	admission         *runnerAdmission
}

func NewConcurrentRunnerResources(limits ConcurrentRunnerLimits) (*ConcurrentRunnerResources, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &ConcurrentRunnerResources{
		limits: limits, preparationTokens: make(chan struct{}, limits.PreparationWorkers),
		statefulTokens: make(chan struct{}, limits.StatefulWorkers), admission: newRunnerAdmission(limits),
	}, nil
}

// ConcurrentRoutedPartitionRunner owns the bounded message pipeline for one
// Kafka partition. Admission remains synchronous and bounded; message
// construction runs in the preparation pool, while RuntimeKey registration
// stays in receive order and disjoint-key stateful work runs concurrently.
type ConcurrentRoutedPartitionRunner struct {
	ctx    context.Context
	cancel context.CancelFunc

	builder   RoutedMessageTaskBuilder
	critical  CriticalCompletion
	rejected  RejectedMessageObserver
	tracker   *PartitionCompletionTracker
	committer *PartitionCommitter
	keyGate   *OrderedKeyGate
	admission *runnerAdmission
	callbacks ConcurrentRunnerCallbacks

	preparationTokens chan struct{}
	statefulTokens    chan struct{}
	registrationMu    sync.Mutex
	registrationTail  chan struct{}
	completionReady   chan struct{}
	commitDone        chan struct{}
	allDone           chan struct{}
	errors            chan error

	mu         sync.Mutex
	closed     bool
	submitters sync.WaitGroup
	tasks      sync.WaitGroup
	closeOnce  sync.Once
	fatalOnce  sync.Once
	fatalErr   error
}

func NewConcurrentRoutedPartitionRunner(
	ctx context.Context,
	builder RoutedMessageTaskBuilder,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
	rejected RejectedMessageObserver,
	limits ConcurrentRunnerLimits,
	callbacks *ConcurrentRunnerCallbacks,
) (*ConcurrentRoutedPartitionRunner, error) {
	resources, err := NewConcurrentRunnerResources(limits)
	if err != nil {
		return nil, err
	}
	return NewConcurrentRoutedPartitionRunnerWithResources(
		ctx, builder, critical, offsets, receipts, rejected, resources, callbacks,
	)
}

func NewConcurrentRoutedPartitionRunnerWithResources(
	ctx context.Context,
	builder RoutedMessageTaskBuilder,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
	rejected RejectedMessageObserver,
	resources *ConcurrentRunnerResources,
	callbacks *ConcurrentRunnerCallbacks,
) (*ConcurrentRoutedPartitionRunner, error) {
	if ctx == nil || builder == nil || critical == nil || offsets == nil || receipts == nil {
		return nil, errors.New("alarmd coordinator: concurrent runner dependencies are required")
	}
	if resources == nil || resources.admission == nil || resources.preparationTokens == nil || resources.statefulTokens == nil {
		return nil, errors.New("alarmd coordinator: concurrent runner resources are required")
	}
	tracker := NewPartitionCompletionTracker()
	committer, err := NewPartitionCommitter(tracker, offsets, receipts)
	if err != nil {
		return nil, err
	}
	runnerCtx, cancel := context.WithCancel(ctx)
	registrationTail := make(chan struct{})
	close(registrationTail)
	runner := &ConcurrentRoutedPartitionRunner{
		ctx: runnerCtx, cancel: cancel, builder: builder, critical: critical, rejected: rejected,
		tracker: tracker, committer: committer, keyGate: NewOrderedKeyGate(), admission: resources.admission,
		preparationTokens: resources.preparationTokens,
		statefulTokens:    resources.statefulTokens,
		registrationTail:  registrationTail,
		completionReady:   make(chan struct{}, resources.limits.MaxInflightMessages), commitDone: make(chan struct{}),
		allDone: make(chan struct{}), errors: make(chan error, 1),
	}
	if callbacks != nil {
		runner.callbacks = *callbacks
	}
	go runner.commitLoop()
	return runner, nil
}

func (runner *ConcurrentRoutedPartitionRunner) Submit(offset int64, payload []byte) error {
	if runner == nil || runner.builder == nil || runner.admission == nil || runner.keyGate == nil || runner.tracker == nil {
		return errors.New("alarmd coordinator: initialized concurrent runner is required")
	}
	if offset < 0 {
		return errors.New("alarmd coordinator: concurrent runner requires a non-negative offset")
	}
	if !runner.beginSubmit() {
		return errors.New("alarmd coordinator: concurrent runner is closed")
	}
	defer runner.submitters.Done()

	lease, err := runner.admission.acquire(runner.ctx, len(payload))
	if err != nil {
		return err
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			lease.release()
		}
	}()
	if err := runner.tracker.Register(offset); err != nil {
		return err
	}
	registrationPrevious, registrationDone := runner.nextRegistration()
	runner.tasks.Add(1)
	releaseLease = false
	go runner.runTask(offset, payload, registrationPrevious, registrationDone, lease)
	return nil
}

func (runner *ConcurrentRoutedPartitionRunner) nextRegistration() (<-chan struct{}, chan struct{}) {
	runner.registrationMu.Lock()
	defer runner.registrationMu.Unlock()
	previous := runner.registrationTail
	done := make(chan struct{})
	runner.registrationTail = done
	return previous, done
}

func (runner *ConcurrentRoutedPartitionRunner) Errors() <-chan error {
	if runner == nil {
		closed := make(chan error)
		close(closed)
		return closed
	}
	return runner.errors
}

func (runner *ConcurrentRoutedPartitionRunner) Drain(ctx context.Context) error {
	if runner == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("alarmd coordinator: drain context is required")
	}
	runner.closeInput()
	select {
	case <-runner.allDone:
		return runner.result()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runner *ConcurrentRoutedPartitionRunner) Stop(ctx context.Context) error {
	if runner == nil {
		return nil
	}
	runner.Cancel()
	return runner.Drain(ctx)
}

func (runner *ConcurrentRoutedPartitionRunner) Cancel() {
	if runner == nil {
		return
	}
	runner.cancel()
	runner.closeInput()
}

func (runner *ConcurrentRoutedPartitionRunner) beginSubmit() bool {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.closed {
		return false
	}
	runner.submitters.Add(1)
	return true
}

func (runner *ConcurrentRoutedPartitionRunner) closeInput() {
	runner.closeOnce.Do(func() {
		runner.mu.Lock()
		runner.closed = true
		runner.mu.Unlock()
		go func() {
			runner.submitters.Wait()
			runner.tasks.Wait()
			close(runner.completionReady)
			<-runner.commitDone
			close(runner.allDone)
		}()
	})
}

func (runner *ConcurrentRoutedPartitionRunner) runTask(
	offset int64,
	payload []byte,
	registrationPrevious <-chan struct{},
	registrationDone chan struct{},
	lease *runnerAdmissionLease,
) {
	var taskErr error
	var reservation *KeyReservation
	registrationFinished := false
	defer func() {
		if !registrationFinished {
			close(registrationDone)
		}
		if reservation != nil {
			reservation.Cancel()
		}
		lease.release()
		if runner.callbacks.OnTaskFinished != nil {
			runner.callbacks.OnTaskFinished(offset, taskErr)
		}
		runner.tasks.Done()
	}()

	var task RoutedMessageTask
	if taskErr = runner.withToken(runner.preparationTokens, func() error {
		var err error
		task, err = runner.builder.BuildMessageTask(runner.ctx, payload)
		if err != nil {
			return err
		}
		if task == nil {
			return errors.New("alarmd coordinator: message task builder returned nil")
		}
		return nil
	}); taskErr != nil {
		runner.failTask(taskErr)
		return
	}
	select {
	case <-registrationPrevious:
	case <-runner.ctx.Done():
		taskErr = runner.ctx.Err()
		return
	}
	keys := canonicalRuntimeKeys(task.RuntimeKeys())
	if taskErr = lease.addKeys(runner.ctx, len(keys)); taskErr != nil {
		runner.failTask(taskErr)
		return
	}
	if len(keys) > 0 {
		reservation, taskErr = runner.keyGate.Register(uint64(offset), keys)
		if taskErr != nil {
			runner.failTask(taskErr)
			return
		}
	}
	close(registrationDone)
	registrationFinished = true
	if taskErr = runner.withToken(runner.preparationTokens, func() error { return task.Prepare(runner.ctx) }); taskErr != nil {
		runner.failTask(taskErr)
		return
	}
	if reservation != nil {
		if taskErr = reservation.Wait(runner.ctx); taskErr != nil {
			runner.failTask(taskErr)
			return
		}
		taskErr = runner.withToken(runner.statefulTokens, func() error {
			return runner.evaluateAndComplete(task, offset)
		})
		if taskErr != nil {
			runner.failTask(taskErr)
			reservation.Cancel()
			reservation = nil
			return
		}
		reservation.Release()
		reservation = nil
	} else {
		taskErr = runner.withToken(runner.statefulTokens, func() error {
			return runner.evaluateAndComplete(task, offset)
		})
	}
	if taskErr != nil {
		runner.failTask(taskErr)
		return
	}
	select {
	case runner.completionReady <- struct{}{}:
	case <-runner.ctx.Done():
		taskErr = runner.ctx.Err()
	}
}

func (runner *ConcurrentRoutedPartitionRunner) evaluateAndComplete(task RoutedMessageTask, offset int64) error {
	outcome, err := task.Evaluate(runner.ctx)
	if err != nil {
		return err
	}
	switch outcome.Kind {
	case MessageOutcomeRejected:
		if outcome.Message != nil || outcome.Rejected == nil {
			return errors.New("alarmd coordinator: invalid rejected message outcome")
		}
		if runner.rejected != nil {
			runner.rejected.ObserveRejected(offset, *outcome.Rejected)
		}
		return runner.tracker.Complete(offset, outcome.Rejected.Receipt)
	case MessageOutcomeCompleted:
		if outcome.Message == nil || outcome.Rejected != nil || outcome.Message.Receipt == nil {
			return errors.New("alarmd coordinator: invalid completed message outcome")
		}
		if phased, ok := runner.critical.(CriticalPhaseCompletion); ok {
			if err := phased.CompleteEvents(runner.ctx, outcome.Message.Events); err != nil {
				return err
			}
			if err := phased.CompleteState(runner.ctx, outcome.Message.StateWrite); err != nil {
				return err
			}
		} else if err := runner.critical.Complete(runner.ctx, outcome.Message.CriticalResult); err != nil {
			return err
		}
		return runner.tracker.Complete(offset, outcome.Message.Receipt)
	default:
		return errors.New("alarmd coordinator: unsupported message outcome")
	}
}

func (runner *ConcurrentRoutedPartitionRunner) withToken(tokens chan struct{}, operation func() error) error {
	if err := runner.ctx.Err(); err != nil {
		return err
	}
	select {
	case tokens <- struct{}{}:
		defer func() { <-tokens }()
		if err := runner.ctx.Err(); err != nil {
			return err
		}
		if err := operation(); err != nil {
			return err
		}
		return runner.ctx.Err()
	case <-runner.ctx.Done():
		return runner.ctx.Err()
	}
}

func (runner *ConcurrentRoutedPartitionRunner) commitLoop() {
	defer close(runner.commitDone)
	for range runner.completionReady {
		if err := runner.committer.CommitReady(runner.ctx); err != nil {
			runner.fail(err)
			return
		}
	}
}

func (runner *ConcurrentRoutedPartitionRunner) failTask(err error) {
	if err == nil || runner.ctx.Err() != nil && errors.Is(err, runner.ctx.Err()) {
		return
	}
	runner.fail(err)
}

func (runner *ConcurrentRoutedPartitionRunner) fail(err error) {
	if err == nil {
		return
	}
	runner.fatalOnce.Do(func() {
		runner.mu.Lock()
		runner.fatalErr = err
		runner.mu.Unlock()
		runner.cancel()
		select {
		case runner.errors <- err:
		default:
		}
	})
}

func (runner *ConcurrentRoutedPartitionRunner) result() error {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.fatalErr != nil {
		return runner.fatalErr
	}
	if runner.ctx.Err() != nil {
		return runner.ctx.Err()
	}
	return nil
}

type runnerAdmission struct {
	mu sync.Mutex

	limits   ConcurrentRunnerLimits
	messages int
	bytes    int
	keyRefs  int
	changed  chan struct{}
}

type runnerAdmissionLease struct {
	admission *runnerAdmission
	bytes     int
	keyRefs   int
	once      sync.Once
}

func newRunnerAdmission(limits ConcurrentRunnerLimits) *runnerAdmission {
	return &runnerAdmission{limits: limits, changed: make(chan struct{})}
}

func (admission *runnerAdmission) acquire(ctx context.Context, bytes int) (*runnerAdmissionLease, error) {
	if bytes < 0 || bytes > admission.limits.MaxInflightBytes {
		return nil, fmt.Errorf("alarmd coordinator: message bytes %d exceed concurrent runner budget", bytes)
	}
	for {
		admission.mu.Lock()
		if admission.messages < admission.limits.MaxInflightMessages && admission.bytes+bytes <= admission.limits.MaxInflightBytes {
			admission.messages++
			admission.bytes += bytes
			admission.mu.Unlock()
			return &runnerAdmissionLease{admission: admission, bytes: bytes}, nil
		}
		changed := admission.changed
		admission.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (lease *runnerAdmissionLease) addKeys(ctx context.Context, keyRefs int) error {
	if lease == nil || lease.admission == nil || keyRefs < 0 ||
		keyRefs > lease.admission.limits.MaxRuntimeKeysPerMessage || keyRefs > lease.admission.limits.MaxPendingKeyRefs {
		return errors.New("alarmd coordinator: message key references exceed concurrent runner budget")
	}
	admission := lease.admission
	for {
		admission.mu.Lock()
		if admission.keyRefs+keyRefs <= admission.limits.MaxPendingKeyRefs {
			admission.keyRefs += keyRefs
			lease.keyRefs = keyRefs
			admission.mu.Unlock()
			return nil
		}
		changed := admission.changed
		admission.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (lease *runnerAdmissionLease) release() {
	if lease == nil || lease.admission == nil {
		return
	}
	lease.once.Do(func() {
		admission := lease.admission
		admission.mu.Lock()
		admission.messages--
		admission.bytes -= lease.bytes
		admission.keyRefs -= lease.keyRefs
		close(admission.changed)
		admission.changed = make(chan struct{})
		admission.mu.Unlock()
	})
}
