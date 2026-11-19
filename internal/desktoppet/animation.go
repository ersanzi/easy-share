package desktoppet

import "math"

const (
	// ExpandDurationMS / CollapseDurationMS 是完整行程的默认时长。
	ExpandDurationMS   uint64 = 350
	CollapseDurationMS uint64 = 350
)

// Animation 是不依赖窗口的 elapsed-time 缓动状态，仅由 UI 线程推进。
// 零值为收起；Value 为 [0,1] 展开比例，不包含位置、裁剪或后台任务生命周期。
type Animation struct {
	value      float64
	from       float64
	target     float64
	startedMS  uint64
	durationMS uint64
	running    bool
}

// SetExpanded 先采样当前位置再改目标，因此进出反向不会跳回端点。
// 反向剩余行程按距离缩短时长；重复同一目标不会重启动画。减少动画立即到端点。
func (a *Animation) SetExpanded(expanded bool, nowMS uint64, reducedMotion bool) float64 {
	current := a.Tick(nowMS)
	target := float64(0)
	duration := CollapseDurationMS
	if expanded {
		target, duration = 1, ExpandDurationMS
	}
	if reducedMotion {
		a.value, a.from, a.target = target, target, target
		a.running = false
		a.durationMS = 0
		return target
	}
	if target == a.target && a.running {
		return current
	}
	if target == current {
		a.target = target
		a.running = false
		return current
	}
	a.from, a.target, a.startedMS = current, target, nowMS
	a.durationMS = max(1, uint64(math.Round(float64(duration)*math.Abs(target-current))))
	a.running = true
	return current
}

// Tick 按实际经过时间计算，而非按 timer 次数累加；迟到的帧直接采样正确位置。
// 使用 cubic-bezier(.22,1,.36,1) 解x取y；反向位置连续。时钟回退保持位置。
func (a *Animation) Tick(nowMS uint64) float64 {
	if !a.running || nowMS < a.startedMS {
		return a.value
	}
	elapsed := nowMS - a.startedMS
	if elapsed >= a.durationMS {
		a.value = a.target
		a.running = false
		return a.value
	}
	t := float64(elapsed) / float64(a.durationMS)
	eased := RevealEase(t)
	value := a.from + (a.target-a.from)*eased
	// 防止同一段里的异常倒序采样让动画倒退。
	if a.target > a.from {
		a.value = max(a.value, min(a.target, value))
	} else {
		a.value = min(a.value, max(a.target, value))
	}
	return a.value
}

// Running 为 false 时宿主可取消动画 WM_TIMER，无需 sleep 或后台动画循环。
func (a *Animation) Running() bool { return a.running }

// Expanded 返回目标态，不等同于动画是否已经到达展开端点。
func (a *Animation) Expanded() bool { return a.target == 1 }
