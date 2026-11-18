// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"sync"
	"time"
)

const idleGrace = 8 * time.Second

func autoShutdown(ctx context.Context, next http.Handler) (context.Context, http.Handler, func()) {
	ctx, stop := context.WithCancel(ctx)
	var mu sync.Mutex
	active := 0
	last := time.Now()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		active++
		mu.Unlock()
		defer func() {
			mu.Lock()
			active--
			last = time.Now()
			mu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				mu.Lock()
				if active == 0 && time.Since(last) >= idleGrace {
					stop()
				}
				mu.Unlock()
			}
		}
	}()
	return ctx, handler, stop
}
