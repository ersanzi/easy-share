package task

import (
	"errors"
	"math"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusAccepted  Status = "accepted"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusRejected  Status = "rejected"
	StatusFailed    Status = "failed"
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
	FileName         string    `json:"fileName"`
	LocalPath        string    `json:"localPath,omitempty"`
	Direction        Direction `json:"direction"`
	Peer             string    `json:"peer"`
	TotalBytes       int64     `json:"totalBytes"`
	TransferredBytes int64     `json:"transferredBytes"`
	Speed            float64   `json:"speed"`
	Status           Status    `json:"status"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func validTransition(current, next Status) bool {
	if terminal(current) {
		return false
	}
	if current == next {
		return true
	}
	switch current {
	case StatusPending:
		return next == StatusAccepted || next == StatusRejected || next == StatusFailed
	case StatusAccepted:
		return next == StatusRunning || next == StatusFailed
	case StatusRunning:
		return next == StatusCompleted || next == StatusFailed
	default:
		return false
	}
}

func validStatus(value Status) bool {
	switch value {
	case StatusPending, StatusAccepted, StatusRunning, StatusCompleted, StatusRejected, StatusFailed:
		return true
	default:
		return false
	}
}

func terminal(value Status) bool {
	return value == StatusCompleted || value == StatusRejected || value == StatusFailed
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
