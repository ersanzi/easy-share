package api

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/coder/websocket"
)

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type eventHub struct {
	mutex       sync.RWMutex
	subscribers map[chan Event]struct{}
}

func newEventHub() *eventHub { return &eventHub{subscribers: make(map[chan Event]struct{})} }

func (hub *eventHub) publish(event Event) {
	hub.mutex.RLock()
	defer hub.mutex.RUnlock()
	for subscriber := range hub.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (hub *eventHub) serve(ctx context.Context, connection *websocket.Conn) {
	channel := make(chan Event, 32)
	hub.mutex.Lock()
	hub.subscribers[channel] = struct{}{}
	hub.mutex.Unlock()
	defer func() { hub.mutex.Lock(); delete(hub.subscribers, channel); hub.mutex.Unlock() }()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-channel:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if err := connection.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}
