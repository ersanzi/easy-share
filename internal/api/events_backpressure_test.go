package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"easyshare/internal/task"
	"github.com/coder/websocket"
)

func reviewProgress(id string, progress int64) Event {
	return Event{Type: "transfer.updated", Data: task.Task{ID: id, OwnerID: "alice", Kind: task.KindCloudDownload, TransferredBytes: progress}}
}

func TestTaskEventMailboxIsBoundedAndKeepsLatestProgress(t *testing.T) {
	subscriber := newEventSubscriber()
	for i := 0; i < 10000; i++ {
		subscriber.publish(reviewProgress("same", int64(i)))
	}
	if len(subscriber.tasks) != 1 {
		t.Fatal("同一任务积压了进度")
	}
	if got := subscriber.takeTasks("alice"); len(got) != 1 || got[0].Data.(task.Task).TransferredBytes != 9999 {
		t.Fatal(got)
	}
	for i := 0; i < 10000; i++ {
		subscriber.publish(reviewProgress(fmt.Sprint(i), int64(i)))
	}
	if len(subscriber.tasks) != taskEventCapacity {
		t.Fatalf("邮箱容量=%d", len(subscriber.tasks))
	}
	if _, ok := subscriber.tasks["9999"]; !ok {
		t.Fatal("丢弃最新事件")
	}
	if got := subscriber.takeTasks("bob"); len(got) != 0 {
		t.Fatal("跨账号交付")
	}
}

func TestSlowCloudAuthorizationDoesNotBlockLANAndCoalescesProgress(t *testing.T) {
	hub := newEventHub()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	authority := func() (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "alice", nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		hub.serve(ctx, conn, authority)
	}))
	defer server.Close()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	deadline := time.Now().Add(2 * time.Second)
	for {
		hub.mutex.RLock()
		count := len(hub.subscribers)
		hub.mutex.RUnlock()
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("未建立订阅")
		}
		time.Sleep(time.Millisecond)
	}
	hub.publish(reviewProgress("one", 1))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("未开始验权")
	}
	for i := 0; i < 10000; i++ {
		hub.publish(reviewProgress("one", int64(i)))
	}
	hub.publish(Event{Type: "transfer.updated", Data: task.Task{ID: "lan", Kind: task.KindLANReceive}})
	read := func() task.Task {
		t.Helper()
		readCtx, done := context.WithTimeout(ctx, time.Second)
		defer done()
		_, raw, err := connection.Read(readCtx)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Data task.Task `json:"data"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		return event.Data
	}
	if got := read(); got.ID != "lan" {
		t.Fatalf("慢验权阻塞或污染LAN: %+v", got)
	}
	close(release)
	if got := read(); got.ID != "one" || got.TransferredBytes != 9999 {
		t.Fatalf("未保留最新进度: %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("单批重复验权=%d", calls.Load())
	}
	cancel()
}

func TestTaskEventBatchRevalidatesWithoutPositiveCache(t *testing.T) {
	subscriber := newEventSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	denied := make(chan struct{})
	authorize := func() (string, error) {
		if calls.Add(1) == 1 {
			return "alice", nil
		}
		close(denied)
		return "", fmt.Errorf("revoked")
	}
	ready := subscriber.authorizedTasks(ctx, 10*time.Millisecond, authorize)
	subscriber.publish(reviewProgress("one", 1))
	select {
	case batch := <-ready:
		if len(batch) != 1 {
			t.Fatal(batch)
		}
	case <-time.After(time.Second):
		t.Fatal("首批未交付")
	}
	subscriber.publish(reviewProgress("one", 2))
	select {
	case <-denied:
	case <-time.After(time.Second):
		t.Fatal("第二批复用了旧授权")
	}
	select {
	case batch := <-ready:
		t.Fatalf("撤权后继续交付: %+v", batch)
	case <-time.After(30 * time.Millisecond):
	}
	if calls.Load() != 2 {
		t.Fatal("空闲仍重复访问控制面")
	}
}
