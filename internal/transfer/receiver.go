package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"easyshare/internal/task"
)

type ReceiverOptions struct {
	Host       string
	Port       int
	ReceiveDir string
	Tasks      *task.Store
	OnUpdate   func(task.Task)
	OnReady    func()
}
type pending struct {
	connection net.Conn
	metadata   Metadata
	taskID     string
}
type Receiver struct {
	options     ReceiverOptions
	mutex       sync.Mutex
	pending     map[string]pending
	listener    net.Listener
	connections chan struct{}
}

func NewReceiver(options ReceiverOptions) *Receiver {
	return &Receiver{options: options, pending: make(map[string]pending), connections: make(chan struct{}, 32)}
}
func (receiver *Receiver) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(receiver.options.Host, strconv.Itoa(receiver.options.Port)))
	if err != nil {
		return err
	}
	receiver.listener = listener
	if receiver.options.OnReady != nil {
		receiver.options.OnReady()
	}
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case receiver.connections <- struct{}{}:
			go func() { defer func() { <-receiver.connections }(); receiver.prepare(connection) }()
		default:
			connection.Close()
		}
	}
}
func (receiver *Receiver) prepare(connection net.Conn) {
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	var metadata Metadata
	if readMessage(connection, &metadata) != nil || metadata.TaskID == "" || metadata.FileSize < 0 {
		connection.Close()
		return
	}
	name, err := safeName(metadata.FileName)
	if err != nil {
		connection.Close()
		return
	}
	metadata.FileName = name
	created, err := receiver.options.Tasks.Create(task.Task{FileName: name, Direction: task.DirectionReceive, Peer: metadata.DeviceName, TotalBytes: metadata.FileSize, Status: task.StatusPending})
	if err != nil {
		connection.Close()
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	receiver.mutex.Lock()
	if len(receiver.pending) >= 100 {
		receiver.mutex.Unlock()
		connection.Close()
		return
	}
	receiver.pending[created.ID] = pending{connection: connection, metadata: metadata, taskID: created.ID}
	receiver.mutex.Unlock()
	receiver.emit(created)
	time.AfterFunc(60*time.Second, func() {
		receiver.mutex.Lock()
		value, ok := receiver.pending[created.ID]
		if ok {
			delete(receiver.pending, created.ID)
		}
		receiver.mutex.Unlock()
		if ok {
			value.connection.Close()
			updated, _ := receiver.options.Tasks.Update(created.ID, func(value *task.Task) error {
				value.Status = task.StatusFailed
				value.Error = "confirmation timed out"
				return nil
			})
			receiver.emit(updated)
		}
	})
}
func (receiver *Receiver) Accept(id string) error {
	return receiver.acceptTo(id, "")
}

// AcceptTo accepts a transfer and saves it to a custom directory instead of the
// default ReceiveDir. If saveDir is empty, the default directory is used.
func (receiver *Receiver) AcceptTo(id, saveDir string) error {
	return receiver.acceptTo(id, saveDir)
}

func (receiver *Receiver) acceptTo(id, saveDir string) error {
	receiver.mutex.Lock()
	value, ok := receiver.pending[id]
	if ok {
		delete(receiver.pending, id)
	}
	receiver.mutex.Unlock()
	if !ok {
		return task.ErrTaskNotFound
	}
	updated, err := receiver.options.Tasks.Update(id, func(value *task.Task) error { value.Status = task.StatusAccepted; return nil })
	if err != nil {
		return err
	}
	receiver.emit(updated)
	if err := writeMessage(value.connection, response{Allowed: true}); err != nil {
		return err
	}
	go receiver.receive(value, saveDir)
	return nil
}

// SetReceiveDir updates the default receive directory at runtime.
func (receiver *Receiver) SetReceiveDir(dir string) {
	receiver.mutex.Lock()
	receiver.options.ReceiveDir = dir
	receiver.mutex.Unlock()
}
func (receiver *Receiver) Reject(id string) error {
	receiver.mutex.Lock()
	value, ok := receiver.pending[id]
	if ok {
		delete(receiver.pending, id)
	}
	receiver.mutex.Unlock()
	if !ok {
		return task.ErrTaskNotFound
	}
	_ = writeMessage(value.connection, response{Allowed: false, Error: "rejected"})
	value.connection.Close()
	updated, err := receiver.options.Tasks.Update(id, func(value *task.Task) error { value.Status = task.StatusRejected; return nil })
	receiver.emit(updated)
	return err
}
func (receiver *Receiver) receive(value pending, saveDir string) {
	defer value.connection.Close()
	targetDir := receiver.options.ReceiveDir
	if saveDir != "" {
		targetDir = saveDir
	}
	_ = os.MkdirAll(targetDir, 0o755)
	// 文件夹传输：FileName 是目录名，实际接收的是 zip 流
	isFolder := value.metadata.Kind == KindFolder
	wireName := value.metadata.FileName
	if isFolder {
		wireName = value.metadata.FileName + ".zip"
	}
	destination, err := availablePath(targetDir, wireName)
	if err != nil {
		receiver.fail(value.taskID, err)
		return
	}
	file, err := os.CreateTemp(targetDir, ".easyshare-*.part")
	if err != nil {
		receiver.fail(value.taskID, err)
		return
	}
	temporary := file.Name()
	updated, _ := receiver.options.Tasks.Update(value.taskID, func(value *task.Task) error { value.Status = task.StatusRunning; return nil })
	receiver.emit(updated)
	writer := &progressWriter{writer: file, total: value.metadata.FileSize, update: func(count int64) {
		current, _ := receiver.options.Tasks.Update(value.taskID, func(value *task.Task) error { value.TransferredBytes = count; return nil })
		receiver.emit(current)
	}}
	_, copyErr := io.CopyN(writer, value.connection, value.metadata.FileSize)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		os.Remove(temporary)
		receiver.fail(value.taskID, errors.Join(copyErr, syncErr, closeErr))
		return
	}
	if err := os.Rename(temporary, destination); err != nil {
		receiver.fail(value.taskID, err)
		return
	}
	// 文件夹传输：解压 zip 到同名目录，删除 zip 文件
	if isFolder {
		folderName := value.metadata.FileName
		folderDest, dirErr := availablePath(targetDir, folderName)
		if dirErr != nil {
			os.Remove(destination)
			receiver.fail(value.taskID, dirErr)
			return
		}
		if err := os.MkdirAll(folderDest, 0o755); err != nil {
			os.Remove(destination)
			receiver.fail(value.taskID, err)
			return
		}
		if err := unzipTo(destination, folderDest); err != nil {
			os.Remove(destination)
			os.RemoveAll(folderDest)
			receiver.fail(value.taskID, err)
			return
		}
		os.Remove(destination)
	}
	updated, _ = receiver.options.Tasks.Update(value.taskID, func(value *task.Task) error {
		value.TransferredBytes = value.TotalBytes
		value.Status = task.StatusCompleted
		return nil
	})
	receiver.emit(updated)
}
func (receiver *Receiver) fail(id string, err error) {
	updated, _ := receiver.options.Tasks.Update(id, func(value *task.Task) error { value.Status = task.StatusFailed; value.Error = err.Error(); return nil })
	receiver.emit(updated)
}
func (receiver *Receiver) emit(value task.Task) {
	if receiver.options.OnUpdate != nil {
		receiver.options.OnUpdate(value)
	}
}

type progressWriter struct {
	writer       io.Writer
	count, total int64
	update       func(int64)
}

func (writer *progressWriter) Write(data []byte) (int, error) {
	count, err := writer.writer.Write(data)
	writer.count += int64(count)
	writer.update(writer.count)
	return count, err
}

var _ = fmt.Sprintf
