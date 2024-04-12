package notifier

import "k8s.io/client-go/util/flowcontrol"

type tokenBucketRateLimiter struct {
	unlimited bool
	rejected  bool
	limiter   flowcontrol.RateLimiter
}

// Stop 实现 RateLimiter Stop 方法
func (rl *tokenBucketRateLimiter) Stop() {
	if rl.rejected || rl.unlimited {
		return
	}
	rl.limiter.Stop()
}

// TryAccept 实现 RateLimiter TryAccept 方法
func (rl *tokenBucketRateLimiter) TryAccept() bool {
	if rl.unlimited {
		return true
	}
	if rl.rejected {
		return false
	}
	return rl.limiter.TryAccept()
}
