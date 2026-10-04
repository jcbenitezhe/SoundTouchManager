package main

import "sync"

// Bus fans one message out to every subscriber. A subscriber that falls
// behind loses messages instead of stalling the device.
type Bus struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newBus() *Bus { return &Bus{subs: map[chan []byte]struct{}{}} }

func (b *Bus) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Bus) Unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (b *Bus) Publish(msg []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Count is the number of subscribers.
func (b *Bus) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
