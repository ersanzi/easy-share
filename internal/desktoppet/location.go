package desktoppet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"easyshare/internal/winui"
)

const (
	WidthDIP  = 144
	HeightDIP = 96
	RadiusDIP = 14
)

// Display 为当前显示器快照；工作区与窗口坐标均为物理像素，允许负坐标。
type Display struct {
	Name    string
	Work    winui.Rect
	DPI     int32
	Primary bool
}

// Placement 单独记位置，不保存账号/令牌、面板固定态或任何任务状态。
// 同一显示器 DPI/任务栏变化时恢复 DIP 偏移；拔屏时按原矩形找最近工作区。
type Placement struct {
	Version    int        `json:"version"`
	Monitor    string     `json:"monitor"`
	OffsetXDIP int32      `json:"offsetXDip"`
	OffsetYDIP int32      `json:"offsetYDip"`
	Window     winui.Rect `json:"window"`
}

func Scale(v, dpi int32) int32 {
	if dpi <= 0 {
		dpi = 96
	}
	return int32((int64(v)*int64(dpi) + 48) / 96)
}

func CapturePlacement(rect winui.Rect, display Display) Placement {
	dpi := max(96, display.DPI)
	return Placement{Version: 1, Monitor: display.Name, Window: rect,
		OffsetXDIP: int32(int64(rect.Left-display.Work.Left) * 96 / int64(dpi)),
		OffsetYDIP: int32(int64(rect.Top-display.Work.Top) * 96 / int64(dpi))}
}

// RestorePlacement 将整只宠物拉回可用工作区；固定展开不改变或锁定宠物位置。
func RestorePlacement(saved Placement, displays []Display) (winui.Rect, Display) {
	valid := make([]Display, 0, len(displays))
	for _, d := range displays {
		if d.Work.Width() > 0 && d.Work.Height() > 0 {
			if d.DPI <= 0 {
				d.DPI = 96
			}
			valid = append(valid, d)
		}
	}
	if len(valid) == 0 {
		return winui.Rect{}, Display{}
	}
	chosen := 0
	found := false
	if saved.Version == 1 {
		for i, d := range valid {
			if saved.Monitor != "" && strings.EqualFold(d.Name, saved.Monitor) {
				chosen, found = i, true
				break
			}
		}
		if !found {
			best := int64(^uint64(0) >> 1)
			cx := int64(saved.Window.Left) + int64(saved.Window.Width())/2
			cy := int64(saved.Window.Top) + int64(saved.Window.Height())/2
			for i, d := range valid {
				x := int64(clamp(int32(cx), d.Work.Left, d.Work.Right))
				y := int64(clamp(int32(cy), d.Work.Top, d.Work.Bottom))
				distance := (cx-x)*(cx-x) + (cy-y)*(cy-y)
				if distance < best {
					chosen, best = i, distance
				}
			}
		}
	} else {
		for i, d := range valid {
			if d.Primary {
				chosen = i
				break
			}
		}
	}
	d := valid[chosen]
	w, h := Scale(WidthDIP, d.DPI), Scale(HeightDIP, d.DPI)
	x, y := d.Work.Right-w-Scale(16, d.DPI), d.Work.Bottom-h-Scale(16, d.DPI)
	if saved.Version == 1 {
		if found {
			x, y = d.Work.Left+Scale(saved.OffsetXDIP, d.DPI), d.Work.Top+Scale(saved.OffsetYDIP, d.DPI)
		} else {
			x, y = saved.Window.Left, saved.Window.Top
		}
	}
	x, y = clamp(x, d.Work.Left, d.Work.Right-w), clamp(y, d.Work.Top, d.Work.Bottom-h)
	return winui.Rect{Left: x, Top: y, Right: x + w, Bottom: y + h}, d
}

// PanelRect 默认位于宠物下方；底部不足时翻到上方，极小工作区再钳制边缘。
// 只计算面板，不修改宠物位置或 saved placement；小屏压缩高度由原页面滚动。
func PanelRect(pet, work winui.Rect, width, height, gap int32) winui.Rect {
	if width <= 0 || height <= 0 || work.Width() <= 0 || work.Height() <= 0 {
		return winui.Rect{}
	}
	w, h := min(width, work.Width()), min(height, work.Height())
	gap = max(0, gap)
	x, y := pet.Left+(pet.Width()-w)/2, pet.Bottom+gap
	// height 为360 DIP的物理尺寸；下方至少240 DIP时压缩而非翻转。
	minimum := int32(int64(height) * 240 / 360)
	below := max(0, work.Bottom-y)
	if below >= minimum {
		h = min(h, below)
	} else {
		h = min(h, max(1, pet.Top-gap-work.Top))
		y = pet.Top - gap - h
	}
	x, y = clamp(x, work.Left, work.Right-w), clamp(y, work.Top, work.Bottom-h)
	return winui.Rect{Left: x, Top: y, Right: x + w, Bottom: y + h}
}

// PanelFrame 返回固定宿主与向下展开的实际可见区域。
// 控制器的完整高度偏移由 RevealBounds 计算，不能再移动宿主或缩放网页。
func PanelFrame(final winui.Rect, progress float64, travel int32) (window, clip winui.Rect) {
	progress = max(0, min(1, progress))
	if final.Width() <= 0 || final.Height() <= 0 || progress <= 0 {
		return final, winui.Rect{}
	}
	// travel 参数保留兼容；行程始终是完整高度，宿主绝不随帧移动。
	_ = travel
	revealed := max(1, int32(float64(final.Height())*progress+.5))
	return final, winui.Rect{Right: final.Width(), Bottom: revealed}
}

func ReadPlacement(path string) (Placement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Placement{}, err
	}
	if len(data) > 4096 {
		return Placement{}, fmt.Errorf("pet placement 超过大小限制")
	}
	var saved Placement
	if err := json.Unmarshal(data, &saved); err != nil {
		return Placement{}, fmt.Errorf("pet placement: %w", err)
	}
	if saved.Version != 1 || saved.Window.Width() <= 0 || saved.Window.Height() <= 0 {
		return Placement{}, fmt.Errorf("pet placement 格式无效")
	}
	return saved, nil
}

// WritePlacement 仅在拖动结束/退出写入，小文件原子替换，避免截断位置记忆。
func WritePlacement(path string, saved Placement) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pet-position-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

func clamp(v, lo, hi int32) int32 {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}
