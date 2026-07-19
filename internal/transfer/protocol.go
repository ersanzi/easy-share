package transfer

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

const maxMetadata = 64 * 1024

type Metadata struct {
	TaskID     string `json:"taskId"`
	FileName   string `json:"fileName"`
	FileSize   int64  `json:"fileSize"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
}
type response struct {
	Allowed bool   `json:"allowed"`
	Error   string `json:"error,omitempty"`
}

func writeMessage(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxMetadata {
		return errors.New("metadata too large")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err = writer.Write(header[:]); err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}
func readMessage(reader io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxMetadata {
		return errors.New("invalid metadata size")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
