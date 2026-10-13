package library

import (
	"context"
	"time"

	"electric-eel-session/bluez"
)

func (t *Tesla) observe(event Observation) {
	if t.emit != nil {
		t.emit(event)
	}
}

func (t *Tesla) startWatch(ctx context.Context) error {
	if t.watcher != nil {
		return nil
	}
	if t.bus == nil {
		bus, err := bluez.Open()
		if err != nil {
			return err
		}
		t.bus = bus
	}
	watcher, err := t.bus.Watch(ctx, t.adapter, t.vin)
	if err != nil {
		return err
	}
	t.watcher = watcher
	root := t.root
	if root == nil {
		root = context.Background()
	}
	watchCtx, cancel := context.WithCancel(root)
	t.watchCancel, t.watchDone = cancel, make(chan struct{})
	done := t.watchDone
	if t.linkActive.Load() {
		watcher.Pause(ctx)
	}
	go func() {
		defer close(done)
		defer t.backgroundGuard()
		for watchCtx.Err() == nil {
			t.watchControl.Lock()
			connected := t.linkActive.Load()
			if !connected {
				watcher.Resume()
			}
			t.watchControl.Unlock()
			if connected {
				select {
				case <-watchCtx.Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
				continue
			}
			pollCtx, pollCancel := context.WithTimeout(watchCtx, 500*time.Millisecond)
			result, err := watcher.Wait(pollCtx)
			pollCancel()
			if watchCtx.Err() != nil {
				return
			}
			if err != nil {
				failure := classify(err)
				t.observe(Observation{Kind: 5, Status: failure.Status, Error: failure.Error})
				return // Rust owns retry and watcher replacement.
			}
			if result != nil && result.HasRSSI && !t.linkActive.Load() {
				t.targetMu.Lock()
				t.target++
				t.lastTarget, t.targetAt = result, time.Now()
				token := t.target
				if t.targets == nil {
					t.targets = make(map[uint64]discoveryTarget)
				}
				for token, record := range t.targets {
					if time.Since(record.at) > 5*time.Second || token+64 < t.target {
						delete(t.targets, token)
					}
				}
				t.targets[token] = discoveryTarget{result: result, at: t.targetAt}
				t.targetMu.Unlock()
				t.observe(Observation{Kind: 1, Target: token, RSSI: int32(result.RSSI)})
			}
		}
	}()
	return nil
}

func (t *Tesla) stopWatch() {
	if t.watchCancel == nil {
		return
	}
	t.watchCancel()
	<-t.watchDone
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.watcher.Stop(ctx)
	cancel()
	t.watchCancel, t.watcher = nil, nil
	t.targetMu.Lock()
	t.lastTarget = nil
	t.target++
	clear(t.targets)
	t.targetMu.Unlock()
}
