package cache

import "sync"

// WaitGroupValue manages a list of channels to notify when a value is ready.
type WaitGroupValue struct {
	mu       sync.Mutex
	channels []chan struct{}
	once     sync.Once
}

func (wgv *WaitGroupValue) addChannel(ch chan struct{}) {
	wgv.mu.Lock()
	defer wgv.mu.Unlock()
	wgv.channels = append(wgv.channels, ch)
}

func (wgv *WaitGroupValue) relatedChannels() []chan struct{} {
	wgv.mu.Lock()
	defer wgv.mu.Unlock()

	channels := make([]chan struct{}, len(wgv.channels))
	copy(channels, wgv.channels)
	wgv.channels = nil
	return channels
}
