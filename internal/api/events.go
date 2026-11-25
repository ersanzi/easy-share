package api

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"easyshare/internal/task"
	"github.com/coder/websocket"
)

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

const taskEventCapacity = 64
const taskEventInterval = time.Second

type pendingTaskEvent struct {
	event    Event
	sequence uint64
}

// 云进度按任务覆盖最新值并限制容量；设备事件独立，不等待控制面。
type eventSubscriber struct {
	devices  chan Event
	mutex    sync.Mutex
	tasks    map[string]pendingTaskEvent
	sequence uint64
}

func newEventSubscriber() *eventSubscriber {
	return &eventSubscriber{devices: make(chan Event, 32), tasks: make(map[string]pendingTaskEvent)}
}

func (subscriber *eventSubscriber) publish(event Event) {
	if event.Type == "transfer.updated" {
		value, ok := event.Data.(task.Task)
		if !ok || value.Hidden {
			return
		}
		if !value.VisibleTo("") {
			if value.OwnerID == "" {
				return
			}
			subscriber.mutex.Lock()
			defer subscriber.mutex.Unlock()
			if _, exists := subscriber.tasks[value.ID]; !exists && len(subscriber.tasks) >= taskEventCapacity {
				// 订阅是实时视图，超限淘汰最久未更新项；完整列表由快照恢复。
				oldestID := ""
				oldest := ^uint64(0)
				for id, pending := range subscriber.tasks {
					if pending.sequence < oldest {
						oldestID, oldest = id, pending.sequence
					}
				}
				delete(subscriber.tasks, oldestID)
			}
			subscriber.sequence++
			subscriber.tasks[value.ID] = pendingTaskEvent{event: event, sequence: subscriber.sequence}
			return
		}
	}
	select {
	case subscriber.devices <- event:
	default:
		// 慢消费者同样保留新设备状态，不排队重放过期进度。
		select {
		case <-subscriber.devices:
		default:
		}
		select {
		case subscriber.devices <- event:
		default:
		}
	}
}

func (subscriber *eventSubscriber) hasTasks() bool {
	subscriber.mutex.Lock()
	defer subscriber.mutex.Unlock()
	return len(subscriber.tasks) > 0
}

func (subscriber *eventSubscriber) takeTasks(owner string) []Event {
	subscriber.mutex.Lock()
	defer subscriber.mutex.Unlock()
	events := make([]Event, 0, len(subscriber.tasks))
	for _, pending := range subscriber.tasks {
		if pending.event.Data.(task.Task).VisibleTo(owner) {
			events = append(events, pending.event)
		}
	}
	clear(subscriber.tasks)
	return events
}

// 每批只验权一次，无跨批授权缓存；网络等待不占用设备消息交付协程。
func (subscriber *eventSubscriber) authorizedTasks(ctx context.Context, interval time.Duration, authorize func() (string, error)) <-chan []Event {
	ready := make(chan []Event)
	go func() {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		defer close(ready)
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if !subscriber.hasTasks() {
				timer.Reset(interval)
				continue
			}
			owner, err := authorize()
			if err != nil {
				subscriber.takeTasks("")
				timer.Reset(interval)
				continue
			}
			// 验权期间产生的进度也只取最终值，不重放旧队列。
			batch := subscriber.takeTasks(owner)
			if len(batch) == 0 {
				timer.Reset(interval)
				continue
			}
			select {
			case <-ctx.Done():
				return
			case ready <- batch:
			}
			timer.Reset(interval)
		}
	}()
	return ready
}

type eventHub struct {
	mutex       sync.RWMutex
	subscribers map[*eventSubscriber]struct{}
}

func newEventHub() *eventHub { return &eventHub{subscribers: make(map[*eventSubscriber]struct{})} }

func (hub *eventHub) publish(event Event) {
	hub.mutex.RLock()
	defer hub.mutex.RUnlock()
	for subscriber := range hub.subscribers {
		subscriber.publish(event)
	}
}

func (hub *eventHub) serve(ctx context.Context, connection *websocket.Conn, authorize func() (string, error)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	subscriber := newEventSubscriber()
	hub.mutex.Lock()
	hub.subscribers[subscriber] = struct{}{}
	hub.mutex.Unlock()
	defer func() { hub.mutex.Lock(); delete(hub.subscribers, subscriber); hub.mutex.Unlock() }()
	ready := subscriber.authorizedTasks(ctx, taskEventInterval, authorize)
	write := func(event Event) error {
		data, err := json.Marshal(event)
		if err != nil {
			return nil
		}
		sendCtx, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		return connection.Write(sendCtx, websocket.MessageText, data)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-subscriber.devices:
			if write(event) != nil {
				return
			}
		case batch, ok := <-ready:
			if !ok {
				return
			}
			for _, event := range batch {
				if write(event) != nil {
					return
				}
			}
		}
	}
}
