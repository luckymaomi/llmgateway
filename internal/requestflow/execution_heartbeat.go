package requestflow

import (
	"context"
	"sync"
	"time"

	"github.com/luckymaomi/llmgateway/internal/execution"
)

func (s *Service) executionContext(parent context.Context, claim execution.Claim) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	var once sync.Once
	stop := func() { once.Do(cancel) }
	go func() {
		ticker := time.NewTicker(s.config.ExecutionHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.repository.HeartbeatExecution(ctx, claim); err != nil {
					stop()
					return
				}
			}
		}
	}()
	return ctx, stop
}
