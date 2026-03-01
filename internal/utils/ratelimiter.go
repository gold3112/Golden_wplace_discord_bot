package utils

import (
	"context"
	"sync"
	"time"
)

// RateLimiter ホスト別にレート制限を行う
type RateLimiter struct {
	rps        int
	interval   time.Duration
	hostLimits map[string]*hostLimiter
	mu         sync.Mutex
}

type hostLimiter struct {
	requests chan *request
	ticker   *time.Ticker
	done     chan struct{}
}

type request struct {
	fn       func() (interface{}, error)
	resultCh chan *result
	ctx      context.Context
}

type result struct {
	value interface{}
	err   error
}

// NewRateLimiter 新しいレートリミッターを作成
func NewRateLimiter(rps int) *RateLimiter {
	if rps <= 0 {
		rps = 2
	}
	return &RateLimiter{
		rps:        rps,
		interval:   time.Second / time.Duration(rps),
		hostLimits: make(map[string]*hostLimiter),
	}
}

// Do リクエストをキューに追加して実行（ホスト別にレート制限）
func (rl *RateLimiter) Do(ctx context.Context, host string, fn func() (interface{}, error)) (interface{}, error) {
	hl := rl.getOrCreateHostLimiter(host)

	req := &request{
		fn:       fn,
		resultCh: make(chan *result, 1),
		ctx:      ctx,
	}

	select {
	case hl.requests <- req:
		// queued
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case res := <-req.resultCh:
		return res.value, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (rl *RateLimiter) getOrCreateHostLimiter(host string) *hostLimiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if hl, ok := rl.hostLimits[host]; ok {
		return hl
	}

	hl := &hostLimiter{
		requests: make(chan *request, 100),
		ticker:   time.NewTicker(rl.interval),
		done:     make(chan struct{}),
	}
	rl.hostLimits[host] = hl
	go rl.worker(hl)
	return hl
}

func (rl *RateLimiter) worker(hl *hostLimiter) {
	for {
		select {
		case <-hl.ticker.C:
			select {
			case req := <-hl.requests:
				if req.ctx.Err() != nil {
					req.resultCh <- &result{err: req.ctx.Err()}
					continue
				}
				value, err := req.fn()
				req.resultCh <- &result{value: value, err: err}
			default:
			}
		case <-hl.done:
			hl.ticker.Stop()
			return
		}
	}
}

// Close すべてのワーカーを停止
func (rl *RateLimiter) Close() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for _, hl := range rl.hostLimits {
		close(hl.done)
	}
	rl.hostLimits = make(map[string]*hostLimiter)
}
