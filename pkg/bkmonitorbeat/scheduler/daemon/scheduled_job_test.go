// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/test/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
)

// newScheduledTaskConfig 构造一个已通过校验的计划任务配置。
func newScheduledTaskConfig(t *testing.T, taskID int32, schedule configs.ActiveProfileScheduleConfig) *configs.ActiveProfileTaskConfig {
	t.Helper()
	conf := configs.NewActiveProfileTaskConfig()
	conf.BKTenantID = "system"
	conf.BizID = 11
	conf.TaskID = taskID
	conf.BKHostID = 456
	conf.Name = "native-cpu-demo"
	conf.OutputDir = t.TempDir()
	conf.Schedule = schedule
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{
		Type: configs.ActiveProfileSelectorPID,
		PIDs: "2341",
	}
	conf.EventDataID = 110001
	conf.ProfileDataID = 110002
	conf.ProfileAppName = "builtin_profile_app"
	conf.ProfileServiceName = "builtin_profile_app"
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	if err := conf.InitIdent(); err != nil {
		t.Fatal(err)
	}
	return conf
}

func futureOnceAt(t *testing.T, taskID int32) *configs.ActiveProfileTaskConfig {
	t.Helper()
	return newScheduledTaskConfig(t, taskID, configs.ActiveProfileScheduleConfig{
		Type:      configs.ActiveProfileScheduleOnceAt,
		ExecuteAt: time.Now().Add(time.Hour).UnixMilli(),
		Timezone:  "UTC",
	})
}

func newScheduledJob(conf *configs.ActiveProfileTaskConfig) *ScheduledJob {
	return &ScheduledJob{
		IntervalJob: &IntervalJob{task: active_profile.New(nil, conf)},
		schedule:    conf,
	}
}

func TestScheduledJobFutureOnceAtPersistsTerminalProgress(t *testing.T) {
	initScheduleStorage(t)
	conf := futureOnceAt(t, 1004)

	job := newScheduledJob(conf)
	job.Init()
	if job.IsFinished() {
		t.Fatal("future once_at should be runnable")
	}
	if job.expired || job.storageKey == "" {
		t.Fatalf("future once_at has unexpected state: expired=%v key=%q", job.expired, job.storageKey)
	}
	job.Next()
	if !job.IsFinished() {
		t.Fatal("once_at should finish after dispatch")
	}
	queue := NewLockQueue()
	if pushRunnableJob(queue, job) || queue.First() != nil {
		t.Fatal("finished schedule must not be put back into the runtime queue")
	}

	// 改上报目标只改变完整配置 ident，不能让已分发的 occurrence 重复执行。
	conf.ProfileServiceName = "rotated-service-name"
	if err := conf.InitIdent(); err != nil {
		t.Fatal(err)
	}
	reloaded := newScheduledJob(conf)
	reloaded.Init()
	if !reloaded.IsFinished() {
		t.Fatalf("dispatched execution repeated at %v", reloaded.checkTime)
	}
}

func TestScheduledJobCronAdvances(t *testing.T) {
	initScheduleStorage(t)
	conf := newScheduledTaskConfig(t, 1002, configs.ActiveProfileScheduleConfig{
		Type:           configs.ActiveProfileScheduleCron,
		CronExpression: "0 * * * *",
		Timezone:       "UTC",
	})

	job := newScheduledJob(conf)
	job.Init()
	first := job.checkTime
	job.Next()
	if !job.checkTime.Equal(first.Add(time.Hour)) {
		t.Fatalf("next cron time = %v, want %v", job.checkTime, first.Add(time.Hour))
	}
}

// TestScheduledJobCronStopsAfterEffectiveEnd 覆盖 Cron 生效窗口：越过结束时间后
// 该任务不再产生 occurrence，也不会被放回运行队列。
func TestScheduledJobCronStopsAfterEffectiveEnd(t *testing.T) {
	initScheduleStorage(t)
	end := time.Now().Add(30 * time.Minute)
	conf := newScheduledTaskConfig(t, 1006, configs.ActiveProfileScheduleConfig{
		Type:           configs.ActiveProfileScheduleCron,
		CronExpression: "0 * * * *",
		Timezone:       "UTC",
		EffectiveEndAt: end.UnixMilli(),
	})

	job := newScheduledJob(conf)
	job.Init()
	if !job.IsFinished() && job.checkTime.After(end) {
		t.Fatalf("cron must not schedule beyond its effective end: %v", job.checkTime)
	}
	job.Next()
	if !job.IsFinished() {
		t.Fatalf("cron should finish after its effective end, next = %v", job.checkTime)
	}
}

func TestScheduledJobExpiredOnceAtReportsOnlyOnce(t *testing.T) {
	initScheduleStorage(t)
	conf := newScheduledTaskConfig(t, 1005, configs.ActiveProfileScheduleConfig{
		Type:      configs.ActiveProfileScheduleOnceAt,
		ExecuteAt: time.Now().Add(-time.Second).UnixMilli(),
		Timezone:  "UTC",
	})

	job := newScheduledJob(conf)
	job.Init()
	if job.IsFinished() || !job.expired {
		t.Fatalf("expired once_at should dispatch one terminal event: finished=%v expired=%v", job.finished, job.expired)
	}
	job.Next()
	if !job.IsFinished() {
		t.Fatal("expired once_at should finish after its terminal event is dispatched")
	}

	reloaded := newScheduledJob(conf)
	reloaded.Init()
	if !reloaded.IsFinished() {
		t.Fatal("expired terminal event must not repeat after reload")
	}
}

func TestRetireWaitsForReservedRun(t *testing.T) {
	initScheduleStorage(t)
	ctx, cancel := context.WithCancel(context.Background())
	job := &IntervalJob{
		ctx:    ctx,
		cancel: cancel,
		task:   active_profile.New(nil, futureOnceAt(t, 1007)),
	}
	if !isGracefulReloadJob(job) {
		t.Fatal("active profiling jobs must opt in to graceful reload")
	}

	job.StartRun()
	retired := make(chan struct{})
	go func() {
		job.Retire()
		close(retired)
	}()

	select {
	case <-retired:
		t.Fatal("retire returned before the reserved run finished")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-ctx.Done():
		t.Fatal("retire canceled the run context before it finished")
	default:
	}

	job.FinishRun()
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("retire did not finish after the run completed")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("retire should release the old job context after the run completed")
	}
}

func TestReloadRetiresChangedActiveProfileJobGracefully(t *testing.T) {
	initScheduleStorage(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	oldConf := futureOnceAt(t, 1003)

	state := newDaemonState()
	state.ctx = ctx
	state.Status = define.SchedulerRunning
	daemon := &Daemon{daemonState: state}
	oldJob := NewJob(active_profile.New(nil, oldConf), daemon)
	oldJob.Init()
	state.jobs.Push(oldJob)

	oldJob.StartRun()
	newConf := *oldConf
	newConf.ProfileServiceName = "rotated-service-name"
	if err := newConf.InitIdent(); err != nil {
		t.Fatal(err)
	}
	if newConf.GetIdent() == oldConf.GetIdent() {
		t.Fatal("test requires a changed full config ident")
	}
	newTask := active_profile.New(nil, &newConf)
	if err := daemon.Reload(ctx, nil, []define.Task{newTask}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-oldJob.(*ScheduledJob).ctx.Done():
		t.Fatal("reload canceled the changed active profiling job while its run was reserved")
	case <-time.After(20 * time.Millisecond):
	}

	oldJob.FinishRun()
	daemon.waitgroup.Wait()
	select {
	case <-oldJob.(*ScheduledJob).ctx.Done():
	default:
		t.Fatal("old active profiling job was not released after its run finished")
	}
}

// blockingTask 停在 Run 里直到 release 关闭，用来观察调度器退出时是否等待它。
type blockingTask struct {
	define.Task
	graceful bool
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newBlockingTask(graceful bool) *blockingTask {
	task := mock.NewTask()
	task.GetConfig().(*mock.TaskConfig).Period = time.Hour
	return &blockingTask{
		Task:     task,
		graceful: graceful,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (t *blockingTask) GracefulReload() bool {
	return t.graceful
}

func (t *blockingTask) Run(_ context.Context, _ chan<- define.Event) {
	t.once.Do(func() { close(t.entered) })
	<-t.release
}

// startDaemonWithDueJob 把一个已到期的 job 放进队列并启动调度循环，等它被派发后返回。
func startDaemonWithDueJob(t *testing.T, ctx context.Context, task *blockingTask) *Daemon {
	t.Helper()
	state := newDaemonState()
	state.ctx = ctx
	state.EventChan = make(chan define.Event, 1)
	state.ticker = time.NewTicker(5 * time.Millisecond)
	daemon := &Daemon{daemonState: state}
	jobCtx, jobCancel := context.WithCancel(ctx)
	state.jobs.Push(&IntervalJob{ctx: jobCtx, cancel: jobCancel, task: task, checkTime: time.Now().Add(-time.Second)})
	daemon.waitgroup.Add(1)
	go daemon.run(ctx)

	select {
	case <-task.entered:
	case <-time.After(time.Second):
		t.Fatal("the due job was not dispatched")
	}
	return daemon
}

func waitAsync(daemon *Daemon) <-chan struct{} {
	waited := make(chan struct{})
	go func() {
		daemon.Wait()
		close(waited)
	}()
	return waited
}

// 普通任务不响应 ctx、仍停在 Run 里时，调度器退出不能等它，与改造前一致。
func TestWaitDoesNotTrackOrdinaryRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := newBlockingTask(false)
	defer close(task.release)

	daemon := startDaemonWithDueJob(t, ctx, task)
	cancel()
	waited := waitAsync(daemon)
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait blocked on an ordinary task run")
	}
}

// 主动采集的执行必须等到结束，才能在事件通道关闭前送出终态。
func TestWaitTracksGracefulRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := newBlockingTask(true)

	daemon := startDaemonWithDueJob(t, ctx, task)
	cancel()
	waited := waitAsync(daemon)
	select {
	case <-waited:
		t.Fatal("Wait returned before the graceful run finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(task.release)
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after the graceful run finished")
	}
}

// TestScheduledJobInitAfterLargeClockJumpIsFastAndAccurate 覆盖时钟大幅前跳与回拨：
// 即使持久化记录远早于当前时间（如停机多年或 NTP 校时前跳），Init 也必须在 O(1) 内
// 定位到当前之后的下一次触发；时钟回拨（last > now）时则从 last 推算，避免重复执行。
func TestScheduledJobInitAfterLargeClockJumpIsFastAndAccurate(t *testing.T) {
	initScheduleStorage(t)
	conf := newScheduledTaskConfig(t, 1008, configs.ActiveProfileScheduleConfig{
		Type:           configs.ActiveProfileScheduleCron,
		CronExpression: "* * * * *",
		Timezone:       "UTC",
	})

	job := newScheduledJob(conf)
	job.Init()
	storageKey := job.storageKey

	// 模拟 30 年前的持久化记录（每分钟一次对应超过 1500 万个周期）
	ancient := time.Now().AddDate(-30, 0, 0).UTC().Truncate(time.Minute)
	if err := storage.Set(storageKey, ancient.Format(timeTemplate), time.Hour); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	jumped := newScheduledJob(conf)
	jumped.Init()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Init() after 30-year clock jump took %s, want O(1)", elapsed)
	}
	if jumped.IsFinished() {
		t.Fatal("cron job without effective_end_at should stay runnable")
	}
	wantMin := started.Truncate(time.Minute).Add(time.Minute)
	if jumped.checkTime.Before(wantMin) || jumped.checkTime.After(wantMin.Add(time.Minute)) {
		t.Fatalf("checkTime = %v, want near %v", jumped.checkTime, wantMin)
	}

	// 模拟时钟回拨 10 分钟：last 晚于 now，必须继续从 last 推算而不是立即重跑
	futureLast := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Minute)
	if err := storage.Set(storageKey, futureLast.Format(timeTemplate), time.Hour); err != nil {
		t.Fatal(err)
	}
	rolledBack := newScheduledJob(conf)
	rolledBack.Init()
	if !rolledBack.checkTime.Equal(futureLast.Add(time.Minute)) {
		t.Fatalf("checkTime after clock rollback = %v, want %v", rolledBack.checkTime, futureLast.Add(time.Minute))
	}
}

// TestPushRunnableJobReleasesFinishedJobContext 验证已完成任务不被入队时会立即释放其 context，
// 防止频繁 Reload 导致 scheduler.ctx 上持续累积已完成的子 context。
func TestPushRunnableJobReleasesFinishedJobContext(t *testing.T) {
	initScheduleStorage(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf := futureOnceAt(t, 1009)
	state := newDaemonState()
	state.ctx = ctx
	daemon := &Daemon{daemonState: state}

	first := NewJob(active_profile.New(nil, conf), daemon)
	first.Init()
	first.Next() // 持久化为已派发

	reloaded := NewJob(active_profile.New(nil, conf), daemon)
	reloaded.Init()
	if !reloaded.IsFinished() {
		t.Fatal("reloaded completed once_at should be finished")
	}
	if pushRunnableJob(state.jobs, reloaded) {
		t.Fatal("finished job must not be pushed into queue")
	}
	select {
	case <-reloaded.(*ScheduledJob).ctx.Done():
	default:
		t.Fatal("finished job context was not released when skipped by pushRunnableJob")
	}
}

type blockingScheduledTask struct {
	*blockingTask
	schedule *configs.ActiveProfileTaskConfig
}

func (t *blockingScheduledTask) GetConfig() define.TaskConfig {
	return t.schedule
}

// TestRunRetiresFinishedGracefulJob 验证 once_at 派发完最后一次后不会中断在途执行，
// 并在本次执行结束后自动取消释放该 job 的 context。
func TestRunRetiresFinishedGracefulJob(t *testing.T) {
	initScheduleStorage(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf := newScheduledTaskConfig(t, 1010, configs.ActiveProfileScheduleConfig{
		Type:      configs.ActiveProfileScheduleOnceAt,
		ExecuteAt: time.Now().Add(-time.Second).UnixMilli(),
		Timezone:  "UTC",
	})
	task := &blockingScheduledTask{
		blockingTask: newBlockingTask(true),
		schedule:     conf,
	}

	state := newDaemonState()
	state.ctx = ctx
	state.EventChan = make(chan define.Event, 1)
	state.ticker = time.NewTicker(5 * time.Millisecond)
	daemon := &Daemon{daemonState: state}

	job := NewJob(task, daemon).(*ScheduledJob)
	job.Init()
	state.jobs.Push(job)

	daemon.waitgroup.Add(1)
	go daemon.run(ctx)

	select {
	case <-task.entered:
	case <-time.After(time.Second):
		t.Fatal("once_at job was not dispatched")
	}

	// 运行期间不能提前取消 job.ctx
	select {
	case <-job.ctx.Done():
		t.Fatal("finished once_at canceled its in-flight run context prematurely")
	case <-time.After(30 * time.Millisecond):
	}

	// 运行完成后 retireJob 必须释放 job.ctx
	close(task.release)
	select {
	case <-job.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("finished once_at did not release its context after the run completed")
	}

	cancel()
	daemon.Wait()
}

func initScheduleStorage(t *testing.T) {
	t.Helper()
	if err := storage.Init(filepath.Join(t.TempDir(), "schedule.db"), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(storage.Close)
}
