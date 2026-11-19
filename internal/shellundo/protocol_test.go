package shellundo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestProtocolRoundTripAndStrictSchema(t *testing.T) {
	request := deleteRequest{Version: protocolVersion, Root: `C:\云盘`, Session: "random", Paths: []string{`C:\云盘\a.txt`}, Deadline: 42}
	var wire bytes.Buffer
	if err := writeFrame(&wire, request); err != nil {
		t.Fatal(err)
	}
	var got deleteRequest
	if err := readFrame(&wire, &got); err != nil || got.Version != request.Version || got.Root != request.Root || len(got.Paths) != 1 || got.Paths[0] != request.Paths[0] {
		t.Fatalf("round trip: %+v/%v", got, err)
	}
	for _, body := range []string{`{"version":1,"unknown":true}`, `{"version":1} {"version":2}`, `null trailing`} {
		var message bytes.Buffer
		_ = binary.Write(&message, binary.LittleEndian, uint32(len(body)))
		message.WriteString(body)
		if err := readFrame(&message, &got); err == nil {
			t.Fatalf("协议应拒绝: %s", body)
		}
	}
}

func TestProtocolLimitsAndTruncation(t *testing.T) {
	for _, size := range []uint32{0, maxFrameBytes + 1} {
		var wire bytes.Buffer
		_ = binary.Write(&wire, binary.LittleEndian, size)
		var got deleteRequest
		if err := readFrame(&wire, &got); err == nil {
			t.Fatalf("未拒绝 size=%d", size)
		}
	}
	var wire bytes.Buffer
	_ = binary.Write(&wire, binary.LittleEndian, uint32(100))
	wire.WriteString("{}")
	var got deleteRequest
	if err := readFrame(&wire, &got); err == nil {
		t.Fatal("截断帧未拒绝")
	}
	if err := writeFrame(&bytes.Buffer{}, strings.Repeat("x", maxFrameBytes)); err == nil {
		t.Fatal("超大消息未拒绝")
	}
}

func TestProtocolResponseAckRoundTripAndExactSession(t *testing.T) {
	for _, result := range []string{"", "hydrate failure"} {
		var wire bytes.Buffer
		if err := writeFrame(&wire, deleteResponse{Version: protocolVersion, Error: result}); err != nil {
			t.Fatal(err)
		}
		var response deleteResponse
		if err := readFrame(&wire, &response); err != nil || response.Error != result {
			t.Fatalf("完整响应读取: %+v / %v", response, err)
		}
		if err := writeFrame(&wire, deleteAck{Version: protocolVersion, Session: "current-session"}); err != nil {
			t.Fatal(err)
		}
		if err := awaitResponseAck(&wire, "current-session"); err != nil {
			t.Fatalf("成功/失败响应均应确认: %v", err)
		}
	}
	for _, ack := range []deleteAck{{Version: protocolVersion + 1, Session: "current-session"}, {Version: protocolVersion, Session: "stale"}, {Version: protocolVersion}} {
		var wire bytes.Buffer
		if err := writeFrame(&wire, ack); err != nil {
			t.Fatal(err)
		}
		if err := awaitResponseAck(&wire, "current-session"); err == nil {
			t.Fatalf("版本/会话错误应拒绝: %+v", ack)
		}
	}
}

func TestProtocolResponseAckPreservesDisconnectAndStrictSchema(t *testing.T) {
	if err := awaitResponseAck(&bytes.Buffer{}, "current-session"); !errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "不能重复删除") {
		t.Fatalf("不能吞掉未确认响应的 EOF: %v", err)
	}
	for _, body := range []string{`{"version":1,"session":"current-session","retry":true}`, `{"version":1,"session":"current-session"} {}`, `null`} {
		var wire bytes.Buffer
		_ = binary.Write(&wire, binary.LittleEndian, uint32(len(body)))
		wire.WriteString(body)
		if err := awaitResponseAck(&wire, "current-session"); err == nil {
			t.Fatalf("ACK 应拒绝额外/空数据: %s", body)
		}
	}
}
