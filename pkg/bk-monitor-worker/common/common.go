// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package common
package common

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"strings"
	"sync"
	"time"

	pb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/proto"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/protobuf/proto"
)

// QueueKeyPrefix returns a prefix for all keys in the given queue.
func QueueKeyPrefix(qname string) string {
	return fmt.Sprintf("{%s}:{%s}:", config.StorageRedisKeyPrefix, qname)
}

// TaskKeyPrefix returns a prefix for task key.
func TaskKeyPrefix(qname string) string {
	return fmt.Sprintf("%st:", QueueKeyPrefix(qname))
}

// TaskKey returns a redis key for the given task message.
func TaskKey(qname, id string) string {
	return fmt.Sprintf("%s%s", TaskKeyPrefix(qname), id)
}

// PendingKey returns a redis key for the given queue name.
func PendingKey(qname string) string {
	return fmt.Sprintf("%spending", QueueKeyPrefix(qname))
}

// ActiveKey returns a redis key for the active tasks.
func ActiveKey(qname string) string {
	return fmt.Sprintf("%sactive", QueueKeyPrefix(qname))
}

// ScheduledKey returns a redis key for the scheduled tasks.
func ScheduledKey(qname string) string {
	return fmt.Sprintf("%sscheduled", QueueKeyPrefix(qname))
}

// RetryKey returns a redis key for the retry tasks.
func RetryKey(qname string) string {
	return fmt.Sprintf("%sretry", QueueKeyPrefix(qname))
}

// ArchivedKey returns a redis key for the archived tasks.
func ArchivedKey(qname string) string {
	return fmt.Sprintf("%sarchived", QueueKeyPrefix(qname))
}

// LeaseKey returns a redis key for the lease.
func LeaseKey(qname string) string {
	return fmt.Sprintf("%slease", QueueKeyPrefix(qname))
}

// CompletedKey returns a redis key for the completed
func CompletedKey(qname string) string {
	return fmt.Sprintf("%scompleted", QueueKeyPrefix(qname))
}

// PausedKey returns a redis key to indicate that the given queue is paused.
func PausedKey(qname string) string {
	return fmt.Sprintf("%spaused", QueueKeyPrefix(qname))
}

// ProcessedTotalKey returns a redis key for total processed count for the given queue.
func ProcessedTotalKey(qname string) string {
	return fmt.Sprintf("%sprocessed", QueueKeyPrefix(qname))
}

// FailedTotalKey returns a redis key for total failure count for the given queue.
func FailedTotalKey(qname string) string {
	return fmt.Sprintf("%sfailed", QueueKeyPrefix(qname))
}

// ProcessedKey returns a redis key for processed count for the given day for the queue.
func ProcessedKey(qname string, t time.Time) string {
	return fmt.Sprintf("%sprocessed:%s", QueueKeyPrefix(qname), t.UTC().Format("2006-01-02"))
}

// FailedKey returns a redis key for failure count for the given day for the queue.
func FailedKey(qname string, t time.Time) string {
	return fmt.Sprintf("%sfailed:%s", QueueKeyPrefix(qname), t.UTC().Format("2006-01-02"))
}

// ServerInfoKey returns a redis key for process info.
func ServerInfoKey(hostname string, pid int, serverID string) string {
	return fmt.Sprintf("bmw:servers:{%s:%d:%s}", hostname, pid, serverID)
}

// WorkersKey returns a redis key for the workers given hostname, pid, and server ID.
// Deprecated: use WorkerKey
func WorkersKey(hostname string, pid int, serverID string) string {
	return fmt.Sprintf("bmw:workers:{%s:%d:%s}", hostname, pid, serverID)
}

// WorkerKey 一个worker可以监听多个队列 以队列名称作为前缀便于快速定位某个队列下的worker
func WorkerKey(queue, workerIdentification string) string {
	return fmt.Sprintf("%s:%s", WorkerKeyQueuePrefix(queue), workerIdentification)
}

// WorkerKeyPrefix prefix of worker status
func WorkerKeyPrefix() string {
	return "bmw:workers:"
}

// WorkerKeyQueuePrefix
func WorkerKeyQueuePrefix(queue string) string {
	return fmt.Sprintf("%s%s", WorkerKeyPrefix(), queue)
}

// SchedulerEntriesKey returns a redis key for the scheduler entries given scheduler ID.
func SchedulerEntriesKey(schedulerID string) string {
	return fmt.Sprintf("bmw:schedulers:{%s}", schedulerID)
}

// SchedulerHistoryKey returns a redis key for the scheduler's history for the given entry.
func SchedulerHistoryKey(entryID string) string {
	return fmt.Sprintf("bmw:scheduler_history:%s", entryID)
}

// UniqueKey returns a redis key with the given type, payload, and queue name.
func UniqueKey(qname, tasktype string, payload []byte) string {
	if payload == nil {
		return fmt.Sprintf("%sunique:%s:", QueueKeyPrefix(qname), tasktype)
	}
	checksum := md5.Sum(payload)
	return fmt.Sprintf("%sunique:%s:%s", QueueKeyPrefix(qname), tasktype, hex.EncodeToString(checksum[:]))
}

// DaemonTaskKey list of daemon task definitions
func DaemonTaskKey() string {
	return "bmw:daemonTasks:tasks"
}

// DaemonBindingTask binding relationship between task to worker
func DaemonBindingTask() string {
	return "bmw:daemonTasks:binding:taskBinding"
}

// DaemonBindingWorker binding relationship between worker to tasks
func DaemonBindingWorker(workerId string) string {
	return fmt.Sprintf("bmw:daemonTasks:binding:workerBinding:%s", workerId)
}

// ValidateQueueName validate queue name
func ValidateQueueName(queueName string) error {
	if len(strings.TrimSpace(queueName)) == 0 {
		return fmt.Errorf("queue name is null")
	}
	return nil
}

// ServerInfo holds information about a running server.
type ServerInfo struct {
	Host              string
	PID               int
	ServerID          string
	Concurrency       int
	Queues            map[string]int
	StrictPriority    bool
	Status            string
	Started           time.Time
	ActiveWorkerCount int
}

// EncodeServerInfo marshals the given ServerInfo and returns the encoded bytes.
func EncodeServerInfo(info *ServerInfo) ([]byte, error) {
	if info == nil {
		return nil, fmt.Errorf("cannot encode nil server info")
	}
	queues := make(map[string]int32)
	for q, p := range info.Queues {
		queues[q] = int32(p)
	}
	started, err := ptypes.TimestampProto(info.Started)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(&pb.ServerInfo{
		Host:              info.Host,
		Pid:               int32(info.PID),
		ServerId:          info.ServerID,
		Concurrency:       int32(info.Concurrency),
		Queues:            queues,
		StrictPriority:    info.StrictPriority,
		Status:            info.Status,
		StartTime:         started,
		ActiveWorkerCount: int32(info.ActiveWorkerCount),
	})
}

// DecodeServerInfo decodes the given bytes into ServerInfo.
func DecodeServerInfo(b []byte) (*ServerInfo, error) {
	var pbmsg pb.ServerInfo
	if err := proto.Unmarshal(b, &pbmsg); err != nil {
		return nil, err
	}
	queues := make(map[string]int)
	for q, p := range pbmsg.GetQueues() {
		queues[q] = int(p)
	}
	startTime, err := ptypes.Timestamp(pbmsg.GetStartTime())
	if err != nil {
		return nil, err
	}
	return &ServerInfo{
		Host:              pbmsg.GetHost(),
		PID:               int(pbmsg.GetPid()),
		ServerID:          pbmsg.GetServerId(),
		Concurrency:       int(pbmsg.GetConcurrency()),
		Queues:            queues,
		StrictPriority:    pbmsg.GetStrictPriority(),
		Status:            pbmsg.GetStatus(),
		Started:           startTime,
		ActiveWorkerCount: int(pbmsg.GetActiveWorkerCount()),
	}, nil
}

// WorkerInfo holds information about a running worker.
type WorkerInfo struct {
	Host     string
	PID      int
	ServerID string
	ID       string
	Kind     string
	Payload  []byte
	Queue    string
	Started  time.Time
	Deadline time.Time
}

// EncodeWorkerInfo marshals the given WorkerInfo and returns the encoded bytes.
func EncodeWorkerInfo(info *WorkerInfo) ([]byte, error) {
	if info == nil {
		return nil, fmt.Errorf("cannot encode nil worker info")
	}
	startTime, err := ptypes.TimestampProto(info.Started)
	if err != nil {
		return nil, err
	}
	deadline, err := ptypes.TimestampProto(info.Deadline)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(&pb.WorkerInfo{
		Host:        info.Host,
		Pid:         int32(info.PID),
		ServerId:    info.ServerID,
		TaskId:      info.ID,
		TaskKind:    info.Kind,
		TaskPayload: info.Payload,
		Queue:       info.Queue,
		StartTime:   startTime,
		Deadline:    deadline,
	})
}

// DecodeWorkerInfo decodes the given bytes into WorkerInfo.
func DecodeWorkerInfo(b []byte) (*WorkerInfo, error) {
	var pbmsg pb.WorkerInfo
	if err := proto.Unmarshal(b, &pbmsg); err != nil {
		return nil, err
	}
	startTime, err := ptypes.Timestamp(pbmsg.GetStartTime())
	if err != nil {
		return nil, err
	}
	deadline, err := ptypes.Timestamp(pbmsg.GetDeadline())
	if err != nil {
		return nil, err
	}
	return &WorkerInfo{
		Host:     pbmsg.GetHost(),
		PID:      int(pbmsg.GetPid()),
		ServerID: pbmsg.GetServerId(),
		ID:       pbmsg.GetTaskId(),
		Kind:     pbmsg.GetTaskKind(),
		Payload:  pbmsg.GetTaskPayload(),
		Queue:    pbmsg.GetQueue(),
		Started:  startTime,
		Deadline: deadline,
	}, nil
}

// SchedulerEntry holds information about a periodic task registered with a scheduler.
type SchedulerEntry struct {
	ID      string
	Spec    string
	Kind    string
	Payload []byte
	Opts    []string
	Next    time.Time
	Prev    time.Time
}

// EncodeSchedulerEntry marshals the given entry and returns an encoded bytes.
func EncodeSchedulerEntry(entry *SchedulerEntry) ([]byte, error) {
	if entry == nil {
		return nil, fmt.Errorf("cannot encode nil scheduler entry")
	}
	next, err := ptypes.TimestampProto(entry.Next)
	if err != nil {
		return nil, err
	}
	prev, err := ptypes.TimestampProto(entry.Prev)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(&pb.SchedulerEntry{
		Id:              entry.ID,
		Spec:            entry.Spec,
		TaskKind:        entry.Kind,
		TaskPayload:     entry.Payload,
		EnqueueOptions:  entry.Opts,
		NextEnqueueTime: next,
		PrevEnqueueTime: prev,
	})
}

// DecodeSchedulerEntry unmarshals the given bytes and returns a decoded SchedulerEntry.
func DecodeSchedulerEntry(b []byte) (*SchedulerEntry, error) {
	var pbmsg pb.SchedulerEntry
	if err := proto.Unmarshal(b, &pbmsg); err != nil {
		return nil, err
	}
	next, err := ptypes.Timestamp(pbmsg.GetNextEnqueueTime())
	if err != nil {
		return nil, err
	}
	prev, err := ptypes.Timestamp(pbmsg.GetPrevEnqueueTime())
	if err != nil {
		return nil, err
	}
	return &SchedulerEntry{
		ID:      pbmsg.GetId(),
		Spec:    pbmsg.GetSpec(),
		Kind:    pbmsg.GetTaskKind(),
		Payload: pbmsg.GetTaskPayload(),
		Opts:    pbmsg.GetEnqueueOptions(),
		Next:    next,
		Prev:    prev,
	}, nil
}

// SchedulerEnqueueEvent holds information about an enqueue event by a scheduler.
type SchedulerEnqueueEvent struct {
	// ID of the task that was enqueued.
	TaskID string

	// Time the task was enqueued.
	EnqueuedAt time.Time
}

// EncodeSchedulerEnqueueEvent marshals the given event
// and returns an encoded bytes.
func EncodeSchedulerEnqueueEvent(event *SchedulerEnqueueEvent) ([]byte, error) {
	if event == nil {
		return nil, fmt.Errorf("cannot encode nil enqueue event")
	}
	enqueuedAt, err := ptypes.TimestampProto(event.EnqueuedAt)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(&pb.SchedulerEnqueueEvent{
		TaskId:      event.TaskID,
		EnqueueTime: enqueuedAt,
	})
}

// DecodeSchedulerEnqueueEvent unmarshals the given bytes
// and returns a decoded SchedulerEnqueueEvent.
func DecodeSchedulerEnqueueEvent(b []byte) (*SchedulerEnqueueEvent, error) {
	var pbmsg pb.SchedulerEnqueueEvent
	if err := proto.Unmarshal(b, &pbmsg); err != nil {
		return nil, err
	}
	enqueuedAt, err := ptypes.Timestamp(pbmsg.GetEnqueueTime())
	if err != nil {
		return nil, err
	}
	return &SchedulerEnqueueEvent{
		TaskID:     pbmsg.GetTaskId(),
		EnqueuedAt: enqueuedAt,
	}, nil
}

// Cancelations is a collection that holds cancel functions for all active tasks.
//
// Cancelations are safe for concurrent use by multiple goroutines.
type Cancelations struct {
	mu          sync.Mutex
	cancelFuncs map[string]context.CancelFunc
}

// NewCancelations returns a Cancelations instance.
func NewCancelations() *Cancelations {
	return &Cancelations{
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

// Add adds a new cancel func to the collection.
func (c *Cancelations) Add(id string, fn context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelFuncs[id] = fn
}

// Delete deletes a cancel func from the collection given an id.
func (c *Cancelations) Delete(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cancelFuncs, id)
}

// Get returns a cancel func given an id.
func (c *Cancelations) Get(id string) (fn context.CancelFunc, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn, ok = c.cancelFuncs[id]
	return fn, ok
}

// Lease is a time bound lease for worker to process task.
type Lease struct {
	once sync.Once
	ch   chan struct{}

	Clock timex.Clock

	mu       sync.Mutex
	expireAt time.Time // guarded by mu
}

// NewLease create a new lease
func NewLease(expirationTime time.Time) *Lease {
	return &Lease{
		ch:       make(chan struct{}),
		expireAt: expirationTime,
		Clock:    timex.NewTimeClock(),
	}
}

// Reset changes the lease to expire at the given time.
// It returns true if the lease is still valid and reset operation was successful, false if the lease had been expired.
func (l *Lease) Reset(expirationTime time.Time) bool {
	if !l.IsValid() {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireAt = expirationTime
	return true
}

// NotifyExpiration Sends a notification to lessee about expired lease
// Returns true if notification was sent, returns false if the lease is still valid and notification was not sent.
func (l *Lease) NotifyExpiration() bool {
	if l.IsValid() {
		return false
	}
	l.once.Do(l.closeCh)
	return true
}

func (l *Lease) closeCh() {
	close(l.ch)
}

// Done returns a communication channel from which the lessee can read to get notified
// when lessor notifies about lease expiration.
func (l *Lease) Done() <-chan struct{} {
	return l.ch
}

// Deadline returns the expiration time of the lease.
func (l *Lease) Deadline() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expireAt
}

// IsValid returns true if the lease's expiration time is in the future or equals to the current time,
// returns false otherwise.
func (l *Lease) IsValid() bool {
	now := l.Clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expireAt.After(now) || l.expireAt.Equal(now)
}
