package transfer

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"easyshare/internal/task"
)

type SendRequest struct {
	Address    string
	FilePath   string
	DeviceID   string
	DeviceName string
	PeerName   string
	BatchID    string
	Tasks      *task.Store
	OnUpdate   func(task.Task)
}

func Send(ctx context.Context, request SendRequest) error {
	// 判断是否为文件夹传输：打包为 zip 后走同一管线
	sendPath := request.FilePath
	kind := KindFile
	displayName := filepath.Base(request.FilePath)
	info, err := os.Stat(request.FilePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		kind = KindFolder
		tempPath, _, zipErr := zipFolder(request.FilePath)
		if zipErr != nil {
			return zipErr
		}
		defer os.Remove(tempPath)
		sendPath = tempPath
	}
	file, err := os.Open(sendPath)
	if err != nil {
		return err
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	created, err := request.Tasks.Create(task.Task{FileName: displayName, LocalPath: request.FilePath, Kind: task.KindLANSend, Direction: task.DirectionSend, Peer: request.PeerName, BatchID: request.BatchID, TotalBytes: fileInfo.Size(), Status: task.StatusPending})
	if err != nil {
		return err
	}
	emit := func(value task.Task) {
		if request.OnUpdate != nil {
			request.OnUpdate(value)
		}
	}
	emit(created)
	fail := func(cause error) error {
		updated, _ := request.Tasks.Update(created.ID, func(value *task.Task) error {
			value.Status = task.StatusFailed
			value.Error = cause.Error()
			return nil
		})
		emit(updated)
		return cause
	}
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", request.Address)
	if err != nil {
		return fail(err)
	}
	defer connection.Close()
	if err := writeMessage(connection, Metadata{TaskID: created.ID, FileName: displayName, FileSize: fileInfo.Size(), DeviceID: request.DeviceID, DeviceName: request.DeviceName, Kind: kind}); err != nil {
		return fail(err)
	}
	var reply response
	if err := readMessage(connection, &reply); err != nil {
		return fail(err)
	}
	if !reply.Allowed {
		updated, _ := request.Tasks.Update(created.ID, func(value *task.Task) error { value.Status = task.StatusRejected; return nil })
		emit(updated)
		return nil
	}
	updated, _ := request.Tasks.Update(created.ID, func(value *task.Task) error { value.Status = task.StatusAccepted; return nil })
	emit(updated)
	updated, _ = request.Tasks.Update(created.ID, func(value *task.Task) error { value.Status = task.StatusRunning; return nil })
	emit(updated)
	buffer := make([]byte, 256*1024)
	var sent int64
	last := time.Now()
	var lastBytes int64
	for {
		count, readErr := file.Read(buffer)
		if count > 0 {
			written, writeErr := connection.Write(buffer[:count])
			sent += int64(written)
			if writeErr != nil {
				return fail(writeErr)
			}
			if time.Since(last) > 100*time.Millisecond || sent == fileInfo.Size() {
				now := time.Now()
				elapsed := now.Sub(last).Seconds()
				speed := 0.0
				if elapsed > 0 {
					speed = float64(sent-lastBytes) / elapsed
				}
				updated, _ = request.Tasks.Update(created.ID, func(value *task.Task) error {
					value.TransferredBytes = sent
					value.Speed = speed
					return nil
				})
				emit(updated)
				lastBytes = sent
				last = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fail(readErr)
		}
	}
	updated, _ = request.Tasks.Update(created.ID, func(value *task.Task) error {
		value.TransferredBytes = value.TotalBytes
		value.Status = task.StatusCompleted
		return nil
	})
	emit(updated)
	return nil
}
