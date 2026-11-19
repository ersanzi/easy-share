package shellundo

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	protocolVersion = 1
	maxFrameBytes   = 1024 * 1024
	// 菜单转发不得无限常驻；超时后操作可能仍由已运行宿主完成，不应盲目重试。
	DefaultForwardTimeout = 30 * time.Second
)

var ErrTimeout = errors.New("Shell 请求超时；若已提交，可能仍在已运行的 EasyShare 中处理，请勿重复删除")

type deleteRequest struct {
	Version  int      `json:"version"`
	Root     string   `json:"root"`
	Session  string   `json:"session"`
	Paths    []string `json:"paths"`
	Deadline int64    `json:"deadline"`
}

type deleteResponse struct {
	Version int    `json:"version"`
	Error   string `json:"error,omitempty"`
}

// ACK 只确认响应已完整读取，不表示再次执行删除或文件操作成功。
type deleteAck struct {
	Version int    `json:"version"`
	Session string `json:"session"`
}

func awaitResponseAck(reader io.Reader, session string) error {
	var ack deleteAck
	if err := readFrame(reader, &ack); err != nil {
		return fmt.Errorf("Shell 响应已发送但未收到确认，不能重复删除: %w", err)
	}
	if ack.Version != protocolVersion || ack.Session != session {
		return fmt.Errorf("Shell 响应确认版本或会话不匹配，不能重复删除")
	}
	return nil
}

func writeFrame(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码 Shell 请求: %w", err)
	}
	if len(data) == 0 || len(data) > maxFrameBytes {
		return fmt.Errorf("Shell 消息过大")
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, data)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readFrame(reader io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 || size > maxFrameBytes {
		return fmt.Errorf("Shell 消息长度无效: %d", size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("解码 Shell 消息: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("Shell 消息有额外数据")
	}
	return nil
}
