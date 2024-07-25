package actor

import (
	"context"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/asynkron/protoactor-go/actor"
)

type HelloActor struct {
	Name string
}

type Service struct {
	system *actor.ActorSystem
	actors []*actor.PID
}

func (state *HelloActor) Receive(context actor.Context) {
	switch context.Message().(type) {
	case string:
		fmt.Printf("Hello\n")
	}
}

// Type
func (s *Service) Type() string {
	return "actor"
}

// Start
func (s *Service) Start(ctx context.Context) {
	// 创建 Actor System
	s.system = actor.NewActorSystem()

	// 创建 Actors 并注册到 Actor System
	for i := 0; i < MaxActorNum; i++ {
		props := actor.PropsFromProducer(func() actor.Actor { return &HelloActor{} })
		pid, err := s.system.Root.SpawnNamed(props, fmt.Sprintf("helloactor%d", i))
		if err != nil {
			log.Errorf(ctx, "Error creating actor: %v", err)
			return
		}
		s.actors = append(s.actors, pid)
	}

	// 将自己的信息注册到 Redis
	err := s.registerToRedis(ctx)
	if err != nil {
		log.Errorf(ctx, "Error registering to Redis: %v", err)
		return
	}
	log.Infof(ctx, "actor service reloaded or start success")
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	s.Close()
	s.Wait()
	s.Start(ctx)
}

// Wait
func (s *Service) Wait() {
}

// Close
func (s *Service) Close() {
	// 清除 Redis 中的注册信息
	ctx := context.TODO()
	if s == nil || s.system == nil {
		log.Warnf(ctx, "Service or ActorSystem not initialized")
		return
	}

	err := s.unregisterFromRedis(ctx)
	if err != nil {
		log.Errorf(ctx, "Error unregistering from Redis: %v", err)
		return
	}

	// 关闭 Actor System
	s.system.Shutdown()
}

// registerToRedis
func (s *Service) registerToRedis(ctx context.Context) error {
	// 注册 ActorSystem
	systemKey := "ActorSystem." + s.system.ID
	_, err := redis.HSet(ctx, systemKey, "system", "running")
	if err != nil {
		return err
	}

	// 注册所有 actors 的 PID
	for _, pid := range s.actors {
		actorKey := fmt.Sprintf("Actor.%s", pid.Id)
		_, err := redis.HSet(ctx, systemKey, actorKey, "active")
		if err != nil {
			return err
		}
	}

	return nil
}

// unregisterFromRedis
func (s *Service) unregisterFromRedis(ctx context.Context) error {
	// 获取 ActorSystem 的键
	systemKey := "ActorSystem." + s.system.ID

	// 清除 ActorSystem
	err := redis.Del(ctx, systemKey)
	if err != nil {
		return err
	}
	return nil
}
