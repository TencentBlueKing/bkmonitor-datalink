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
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
)

var (
	errComparatorAssignmentEnded       = errors.New("kafka comparator assignment: session ended")
	errComparatorSymbolicInitialOffset = errors.New("kafka comparator assignment: symbolic initial offset")
)

type comparatorMetadata interface {
	RefreshMetadata(topics ...string) error
	Partitions(topic string) ([]int32, error)
	GetOffset(topic string, partition int32, timestamp int64) (int64, error)
}

type comparatorGeneration struct {
	id          assignmentID
	epoch       string
	active      bool
	expected    map[assignmentClaim]comparator.StreamRole
	assignments map[assignmentClaim]comparator.PartitionAssignment
	ready       chan struct{}
	readyClosed bool
	run         *comparator.Run
	err         error
	inflight    int
	recordOwner bool
	lostEntries int
	lostInputs  int
	lossKnown   bool
}

type comparatorAssignmentHandle struct {
	generation *comparatorGeneration
}

type comparatorAssignmentCoordinator struct {
	mu sync.Mutex

	metadata        comparatorMetadata
	roleTopics      map[comparator.StreamRole]string
	maxEntries      int
	coverageTimeout time.Duration
	diagnostics     ComparatorDiagnostics
	nextGeneration  uint64
	current         *comparatorGeneration
}

func newComparatorAssignmentCoordinator(
	metadata comparatorMetadata,
	roleTopics map[comparator.StreamRole]string,
	maxEntries int,
	coverageTimeout time.Duration,
) (*comparatorAssignmentCoordinator, error) {
	if metadata == nil {
		return nil, errors.New("kafka comparator assignment: metadata is required")
	}
	if maxEntries <= 0 {
		return nil, errors.New("kafka comparator assignment: max entries must be positive")
	}
	if coverageTimeout <= 0 {
		return nil, errors.New("kafka comparator assignment: coverage timeout must be positive")
	}
	copyTopics, err := validateComparatorTopics(roleTopics)
	if err != nil {
		return nil, err
	}
	return &comparatorAssignmentCoordinator{
		metadata: metadata, roleTopics: copyTopics, maxEntries: maxEntries, coverageTimeout: coverageTimeout,
	}, nil
}

func (c *comparatorAssignmentCoordinator) Setup(
	session sarama.ConsumerGroupSession,
) (*comparatorAssignmentHandle, error) {
	if c == nil || c.metadata == nil || session == nil {
		return nil, errors.New("kafka comparator assignment: initialized coordinator and session are required")
	}
	sessionContext := session.Context()
	if sessionContext == nil {
		return nil, errors.New("kafka comparator assignment: session context is required")
	}
	if err := sessionContext.Err(); err != nil {
		return nil, err
	}
	id := sessionAssignmentID(session)
	if id.member == "" || id.generation < 0 {
		return nil, errors.New("kafka comparator assignment: session identity is invalid")
	}

	c.mu.Lock()
	if c.current != nil && c.current.active {
		c.mu.Unlock()
		return nil, errors.New("kafka comparator assignment: another session is active")
	}
	c.mu.Unlock()

	topics := c.orderedTopics()
	if err := c.metadata.RefreshMetadata(topics...); err != nil {
		return nil, fmt.Errorf("kafka comparator assignment: refresh metadata: %w", err)
	}
	expected := make(map[assignmentClaim]comparator.StreamRole)
	for _, role := range orderedComparatorRoles() {
		topic := c.roleTopics[role]
		partitions, err := c.metadata.Partitions(topic)
		if err != nil {
			return nil, fmt.Errorf("kafka comparator assignment: list partitions for %q: %w", topic, err)
		}
		if len(partitions) == 0 {
			return nil, fmt.Errorf("kafka comparator assignment: topic %q has no partitions", topic)
		}
		for _, partition := range partitions {
			if partition < 0 {
				return nil, fmt.Errorf("kafka comparator assignment: topic %q has an invalid partition", topic)
			}
			claim := assignmentClaim{topic: topic, partition: partition}
			if _, ok := expected[claim]; ok {
				return nil, fmt.Errorf("kafka comparator assignment: duplicate metadata partition %s/%d", topic, partition)
			}
			expected[claim] = role
		}
	}
	if err := requireExactComparatorClaims(session.Claims(), expected); err != nil {
		return nil, err
	}
	if err := sessionContext.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != nil && c.current.active {
		return nil, errors.New("kafka comparator assignment: another session is active")
	}
	c.nextGeneration++
	if c.nextGeneration == 0 {
		return nil, errors.New("kafka comparator assignment: generation sequence overflow")
	}
	generation := &comparatorGeneration{
		id:          id,
		epoch:       fmt.Sprintf("comparator-assignment-v1:%d:%d:%s", c.nextGeneration, id.generation, id.member),
		active:      true,
		expected:    expected,
		assignments: make(map[assignmentClaim]comparator.PartitionAssignment, len(expected)),
		ready:       make(chan struct{}),
	}
	c.current = generation
	if done := sessionContext.Done(); done != nil {
		go func(current *comparatorGeneration) {
			<-done
			c.endGeneration(current, sessionContext.Err())
		}(generation)
	}
	return &comparatorAssignmentHandle{generation: generation}, nil
}

func (c *comparatorAssignmentCoordinator) RegisterClaim(
	handle *comparatorAssignmentHandle,
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	if c == nil || handle == nil || handle.generation == nil || session == nil || claim == nil {
		return errors.New("kafka comparator assignment: coordinator, handle, session and claim are required")
	}
	id := sessionAssignmentID(session)
	claimID := assignmentClaim{topic: claim.Topic(), partition: claim.Partition()}
	c.mu.Lock()
	generation, err := c.currentGenerationLocked(handle, id)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if !generation.active {
		c.mu.Unlock()
		return generationError(generation)
	}
	role, ok := generation.expected[claimID]
	if !ok {
		c.mu.Unlock()
		return errors.New("kafka comparator assignment: claim is outside the frozen topology")
	}
	if _, ok := generation.assignments[claimID]; ok {
		c.failGenerationLocked(generation, errors.New("kafka comparator assignment: duplicate claim registration"))
		c.mu.Unlock()
		return generation.err
	}
	c.mu.Unlock()

	nextOffset, err := c.resolveInitialOffset(claim)
	if err != nil {
		c.mu.Lock()
		if c.current == generation && generation.active {
			c.failGenerationLocked(generation, err)
		}
		c.mu.Unlock()
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.currentGenerationLocked(handle, id)
	if err != nil || current != generation {
		if err != nil {
			return err
		}
		return errors.New("kafka comparator assignment: session changed while registering claim")
	}
	if !generation.active {
		return generationError(generation)
	}
	if _, ok := generation.assignments[claimID]; ok {
		c.failGenerationLocked(generation, errors.New("kafka comparator assignment: duplicate claim registration"))
		return generation.err
	}
	generation.assignments[claimID] = comparator.PartitionAssignment{
		Role: role, Topic: claimID.topic, Partition: claimID.partition, NextOffset: nextOffset,
	}
	if len(generation.assignments) != len(generation.expected) {
		return nil
	}
	assignments := make([]comparator.PartitionAssignment, 0, len(generation.assignments))
	for _, assignment := range generation.assignments {
		assignments = append(assignments, assignment)
	}
	sort.Slice(assignments, func(left, right int) bool {
		if assignments[left].Role != assignments[right].Role {
			return assignments[left].Role < assignments[right].Role
		}
		if assignments[left].Topic != assignments[right].Topic {
			return assignments[left].Topic < assignments[right].Topic
		}
		return assignments[left].Partition < assignments[right].Partition
	})
	run, err := comparator.NewRun(
		generation.epoch,
		c.maxEntries,
		assignments,
		comparator.WithCoverageTimeout(c.coverageTimeout),
	)
	if err != nil {
		c.failGenerationLocked(generation, fmt.Errorf("kafka comparator assignment: create Run: %w", err))
		return generation.err
	}
	generation.run = run
	c.closeReadyLocked(generation)
	return nil
}

func (c *comparatorAssignmentCoordinator) WaitReady(
	ctx context.Context,
	handle *comparatorAssignmentHandle,
	session sarama.ConsumerGroupSession,
) (*comparator.Run, string, error) {
	if c == nil || ctx == nil || handle == nil || handle.generation == nil || session == nil {
		return nil, "", errors.New("kafka comparator assignment: coordinator, context, handle and session are required")
	}
	id := sessionAssignmentID(session)
	c.mu.Lock()
	generation, err := c.currentGenerationLocked(handle, id)
	if err != nil {
		c.mu.Unlock()
		return nil, "", err
	}
	ready := generation.ready
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case <-ready:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != generation || generation.id != id {
		return nil, "", errors.New("kafka comparator assignment: session changed while waiting for Run")
	}
	if generation.err != nil {
		return nil, "", generation.err
	}
	if !generation.active || generation.run == nil {
		return nil, "", errComparatorAssignmentEnded
	}
	return generation.run, generation.epoch, nil
}

func (c *comparatorAssignmentCoordinator) Cleanup(
	handle *comparatorAssignmentHandle,
	session sarama.ConsumerGroupSession,
) error {
	if c == nil || handle == nil || handle.generation == nil || session == nil {
		return errors.New("kafka comparator assignment: coordinator, handle and session are required")
	}
	id := sessionAssignmentID(session)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != handle.generation || c.current.id != id {
		return nil
	}
	c.endGenerationLocked(c.current, errComparatorAssignmentEnded)
	return nil
}

func (c *comparatorAssignmentCoordinator) resolveInitialOffset(claim sarama.ConsumerGroupClaim) (int64, error) {
	initial := claim.InitialOffset()
	if initial < 0 {
		return 0, fmt.Errorf(
			"%w %d; offset repair must commit a numeric offset before Consume",
			errComparatorSymbolicInitialOffset,
			initial,
		)
	}
	next := initial
	highWater, err := c.metadata.GetOffset(claim.Topic(), claim.Partition(), sarama.OffsetNewest)
	if err != nil {
		return 0, fmt.Errorf("kafka comparator assignment: resolve newest offset: %w", err)
	}
	if next < 0 || next == math.MaxInt64 || highWater < 0 || highWater == math.MaxInt64 || next > highWater {
		return 0, errors.New("kafka comparator assignment: initial offset or high water is out of range")
	}
	return next, nil
}

func (c *comparatorAssignmentCoordinator) currentGenerationLocked(
	handle *comparatorAssignmentHandle,
	id assignmentID,
) (*comparatorGeneration, error) {
	if handle == nil || handle.generation == nil || c.current != handle.generation || c.current.id != id {
		return nil, errComparatorAssignmentEnded
	}
	return c.current, nil
}

func generationError(generation *comparatorGeneration) error {
	if generation.err != nil {
		return generation.err
	}
	return errComparatorAssignmentEnded
}

func (c *comparatorAssignmentCoordinator) failGenerationLocked(generation *comparatorGeneration, err error) {
	if err == nil {
		err = errComparatorAssignmentEnded
	}
	generation.active = false
	generation.err = err
	c.invalidateGenerationIfIdleLocked(generation)
	c.closeReadyLocked(generation)
}

func (c *comparatorAssignmentCoordinator) endGeneration(current *comparatorGeneration, cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != current || !current.active {
		return
	}
	c.endGenerationLocked(current, cause)
}

func (c *comparatorAssignmentCoordinator) endGenerationLocked(generation *comparatorGeneration, cause error) {
	if cause == nil {
		cause = errComparatorAssignmentEnded
	}
	generation.active = false
	if generation.err == nil {
		generation.err = cause
	}
	c.invalidateGenerationIfIdleLocked(generation)
	c.closeReadyLocked(generation)
}

func (c *comparatorAssignmentCoordinator) invalidateGenerationIfIdleLocked(generation *comparatorGeneration) {
	if generation.inflight != 0 {
		return
	}
	if generation.run != nil && generation.run.Valid() {
		generation.lostEntries, generation.lostInputs, _ = generation.run.CoverageCounts(generation.epoch)
		generation.lossKnown = true
		_ = generation.run.Invalidate(generation.epoch, generationError(generation))
	}
}

func (c *comparatorAssignmentCoordinator) epochRollover(
	handle *comparatorAssignmentHandle,
) (ComparatorEpochRollover, bool) {
	if c == nil || handle == nil || handle.generation == nil {
		return ComparatorEpochRollover{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	generation := handle.generation
	if !generation.lossKnown {
		return ComparatorEpochRollover{}, false
	}
	return ComparatorEpochRollover{
		Entries: generation.lostEntries, Authoritative: generation.lostInputs,
	}, true
}

func (c *comparatorAssignmentCoordinator) closeReadyLocked(generation *comparatorGeneration) {
	if !generation.readyClosed {
		generation.readyClosed = true
		close(generation.ready)
	}
}

func (c *comparatorAssignmentCoordinator) orderedTopics() []string {
	topics := make([]string, 0, 3)
	for _, role := range orderedComparatorRoles() {
		topics = append(topics, c.roleTopics[role])
	}
	return topics
}

func validateComparatorTopics(roleTopics map[comparator.StreamRole]string) (map[comparator.StreamRole]string, error) {
	if len(roleTopics) != 3 {
		return nil, errors.New("kafka comparator assignment: exactly three role topics are required")
	}
	copyTopics := make(map[comparator.StreamRole]string, 3)
	seen := make(map[string]struct{}, 3)
	for _, role := range orderedComparatorRoles() {
		topic, ok := roleTopics[role]
		if !ok || topic == "" || strings.TrimSpace(topic) != topic {
			return nil, errors.New("kafka comparator assignment: every role topic must be non-empty canonical text")
		}
		if _, ok := seen[topic]; ok {
			return nil, errors.New("kafka comparator assignment: role topics must be unique")
		}
		seen[topic] = struct{}{}
		copyTopics[role] = topic
	}
	return copyTopics, nil
}

func requireExactComparatorClaims(
	claims map[string][]int32,
	expected map[assignmentClaim]comparator.StreamRole,
) error {
	actual := make(map[assignmentClaim]struct{})
	for topic, partitions := range claims {
		for _, partition := range partitions {
			claim := assignmentClaim{topic: topic, partition: partition}
			if partition < 0 {
				return errors.New("kafka comparator assignment: session has an invalid partition")
			}
			if _, ok := actual[claim]; ok {
				return errors.New("kafka comparator assignment: session has a duplicate claim")
			}
			actual[claim] = struct{}{}
		}
	}
	if len(actual) != len(expected) {
		return errors.New("kafka comparator assignment: one member must own every partition of all three topics")
	}
	for claim := range actual {
		if _, ok := expected[claim]; !ok {
			return errors.New("kafka comparator assignment: one member must own every partition of all three topics")
		}
	}
	return nil
}

func orderedComparatorRoles() []comparator.StreamRole {
	return []comparator.StreamRole{comparator.StreamInput, comparator.StreamGo, comparator.StreamPython}
}
