package task

import (
	"errors"
	"math"
	"time"
)

// Kind 区分任务来源，统一展示在任务中心。
type Kind string

const (
	KindLANSend      Kind = "lan_send"
	KindLANReceive   Kind = "lan_receive"
	KindCloudUpload  Kind = "cloud_upload"
	KindCloudDownload Kind = "cloud_download"
	// 预留：后续里程碑扩展
	KindSync  Kind = "sync"
	KindParse Kind = "parse"
)

type Status string

const (
	// 原有状态（局域网传输使用）
	StatusPending   Status = "pending"     // 等待用户确认（接收方）
	StatusAccepted  Status = "accepted"    // 已接受，准备传输
	StatusRunning   Status = "running"     // 传输中
	StatusCompleted Status = "completed"   // 完成
	StatusRejected  Status = "rejected"    // 用户拒绝
	StatusFailed    Status = "failed"      // 失败

	// 扩展状态（统一任务模型）
	StatusQueued         Status = "queued"          // 排队等待
	StatusPaused         Status = "paused"          // 用户暂停
	StatusWaitingNetwork Status = "waiting_network" // 等待网络恢复
	StatusCancelled      Status = "cancelled"       // 用户取消
)

type Direction string

const (
	DirectionSend    Direction = "send"
	DirectionReceive Direction = "receive"
)

var (
	ErrTaskNotFound      = errors.New("task not found")
	ErrImmutableID       = errors.New("task ID is immutable")
	ErrInvalidTransition = errors.New("invalid task status transition")
	ErrInvalidProgress   = errors.New("invalid task progress")
	ErrInvalidTask       = errors.New("invalid task")
	ErrImmutableField    = errors.New("task field is immutable")
)

type Task struct {
	ID               string    `json:"id"`
	Kind             Kind      `json:"kind"`
	FileName         string    `json:"fileName"`
	LocalPath        string    `json:"localPath,omitempty"`
	Direction        Direction `json:"direction"`
	Peer             string    `json:"peer"`
	BatchID          string    `json:"batchId,omitempty"`
	TotalBytes       int64     `json:"totalBytes"`
	TransferredBytes int64     `json:"transferredBytes"`
	Speed            float64   `json:"speed"`
	Status           Status    `json:"status"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func validTransition(current, next Status) bool {
	// 终态不可再变更（failed 允许重试→queued）
	if terminal(current) {
		return current == StatusFailed && next == StatusQueued
	}
	if current == next {
		return true
	}
	switch current {
	case StatusPending:
		return next == StatusAccepted || next == StatusRejected || next == StatusFailed || next == StatusCancelled
	case StatusAccepted:
		return next == StatusRunning || next == StatusFailed || next == StatusCancelled
	case StatusQueued:
		return next == StatusRunning || next == StatusFailed || next == StatusCancelled || next == StatusWaitingNetwork
	case StatusRunning:
		return next == StatusCompleted || next == StatusFailed || next == StatusPaused || next == StatusWaitingNetwork || next == StatusCancelled
	case StatusPaused:
		return next == StatusRunning || next == StatusQueued || next == StatusCancelled || next == StatusFailed
	case StatusWaitingNetwork:
		return next == StatusRunning || next == StatusQueued || next == StatusFailed || next == StatusCancelled
	default:
		return false
	}
}

func validStatus(value Status) bool {
	switch value {
	case StatusPending, StatusAccepted, StatusRunning, StatusCompleted,
		StatusRejected, StatusFailed, StatusQueued, StatusPaused,
		StatusWaitingNetwork, StatusCancelled:
		return true
	default:
		return false
	}
}

func validKind(value Kind) bool {
	switch value {
	case KindLANSend, KindLANReceive, KindCloudUpload, KindCloudDownload,
		KindSync, KindParse, "":
		return true
	default:
		return false
	}
}

func terminal(value Status) bool {
	return value == StatusCompleted || value == StatusRejected || value == StatusFailed || value == StatusCancelled
}

func validDirection(value Direction) bool {
	return value == "" || value == DirectionSend || value == DirectionReceive
}

func validProgress(value Task) bool {
	if value.TotalBytes < 0 || value.TransferredBytes < 0 || value.TransferredBytes > value.TotalBytes {
		return false
	}
	if math.IsNaN(value.Speed) || math.IsInf(value.Speed, 0) || value.Speed < 0 {
		return false
	}
	return value.Status != StatusCompleted || value.TransferredBytes == value.TotalBytes
}
