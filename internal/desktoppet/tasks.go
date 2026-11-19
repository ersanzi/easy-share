package desktoppet

import (
	"fmt"
	"strings"

	"easyshare/internal/task"
)

// TaskSummary 只由 Core 的 task.Task 投影，不生成进度，不把断线解释成任务完成。
type TaskSummary struct {
	Label     string `json:"label"`
	Detail    string `json:"detail,omitempty"`
	Active    int    `json:"active"`
	Attention int    `json:"attention"`
}

// SummarizeTasks 的状态分组与 frontend/src/utils/tasks.ts 一致。
func SummarizeTasks(tasks []task.Task, available bool) TaskSummary {
	if !available {
		return TaskSummary{Label: "任务状态暂不可用", Detail: "请在主窗口检查 Core 连接；上传任务不会因面板隐藏而取消"}
	}
	result := TaskSummary{Label: "暂无任务"}
	var top *task.Task
	priority := func(status task.Status) int {
		switch status {
		case task.StatusAccepted, task.StatusRunning, task.StatusQueued:
			return 0
		case task.StatusPending, task.StatusPaused, task.StatusWaitingNetwork, task.StatusFailed:
			return 1
		default:
			return 2
		}
	}
	for i := range tasks {
		current := &tasks[i]
		switch priority(current.Status) {
		case 0:
			result.Active++
		case 1:
			result.Attention++
		}
		if top == nil || priority(current.Status) < priority(top.Status) ||
			(priority(current.Status) == priority(top.Status) && current.UpdatedAt.After(top.UpdatedAt)) {
			top = current
		}
	}
	if result.Active > 0 || result.Attention > 0 {
		result.Label = fmt.Sprintf("%d 项进行中 · %d 项需处理", result.Active, result.Attention)
	} else if top != nil {
		result.Label = "最近：" + taskStatusText(top.Status)
	}
	if top != nil {
		parts := []string{top.FileName, taskStatusText(top.Status)}
		if top.Kind == task.KindCloudUpload && strings.HasSuffix(top.FileName, "/") {
			parts = append(parts, fmt.Sprintf("%d / %d 个文件", top.TransferredBytes, top.TotalBytes))
		} else if top.TotalBytes > 0 {
			parts = append(parts, fmt.Sprintf("%d / %d B", top.TransferredBytes, top.TotalBytes))
		}
		if top.Error != "" {
			parts = append(parts, top.Error)
		}
		result.Detail = strings.Join(parts, " · ")
	}
	return result
}

func taskStatusText(status task.Status) string {
	switch status {
	case task.StatusPending:
		return "等待确认"
	case task.StatusAccepted:
		return "准备中"
	case task.StatusRunning:
		return "进行中"
	case task.StatusQueued:
		return "排队中"
	case task.StatusPaused:
		return "已暂停"
	case task.StatusWaitingNetwork:
		return "等待网络"
	case task.StatusCompleted:
		return "已完成"
	case task.StatusRejected:
		return "已拒绝"
	case task.StatusFailed:
		return "失败"
	case task.StatusCancelled:
		return "已取消"
	default:
		return string(status)
	}
}
