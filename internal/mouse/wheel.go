package mouse

// 滚轮增量的单位与跨平台换算。
//
// 协议里 wheel 事件的 WheelDelta 保持捕获端原生值，WheelUnit 标明单位：
//   - Windows（WH_MOUSE_LL / WM_MOUSEWHEEL 的 mouseData）：120 = 一格；
//     高精度滚轮/精度触控板会发送 1/40 等细分增量（单位相同，累计到 120 才是一格）。
//   - macOS（kCGScrollWheelEventDeltaAxis1）：单位为「行」，滚轮通常 ±1/步。
//
// 旧版对端没有 wheel_unit 字段，按对端 OS 推断（windows→notch，其余→line）。
// 本机注入端再按自己的单位换算，避免跨平台一格滚 120 行 / 1/120 格的失真。
const (
	WheelUnitNotch = "notch"
	WheelUnitLine  = "line"
	wheelDelta     = 120
)

// WheelDeltaForLocal 把对端滚轮增量换算成本机注入端期望的单位：
//   - Windows 注入端要 120/格：line 单位 ×120；notch 单位原值（保留高精度细分值）；
//   - macOS 注入端要行单位：line 原值；notch 按 120/格累积换算（accum 跨事件保存余量，
//     精度触控板的 1/40 增量会累积到一格再滚，不会每事件都滚一整格）。
//
// localOS 传 runtime.GOOS；peerOS 仅在 unit 为空（旧版对端）时用于推断单位。
func WheelDeltaForLocal(unit string, raw int, localOS, peerOS string, accum *int) int {
	if raw == 0 {
		return 0
	}
	if unit == "" {
		switch peerOS {
		case "windows":
			unit = WheelUnitNotch
		case "darwin", "linux":
			unit = WheelUnitLine
		default:
			// 旧版对端且 OS 未知（如刚手动添加、异步探测未回填）：
			// 按原值直传，与升级前行为一致，避免错误换算放大滚动。
			return raw
		}
	}
	if unit != WheelUnitNotch && unit != WheelUnitLine {
		// 未知单位：保守按原值直传
		return raw
	}
	if unit == WheelUnitNotch {
		if localOS == "darwin" {
			if accum == nil {
				return 0
			}
			*accum += raw
			lines := *accum / wheelDelta
			*accum -= lines * wheelDelta
			return lines
		}
		// Windows 注入端本身就是 notch 单位（含 1/40 等高精度细分值）
		return raw
	}
	// line 单位
	if localOS == "windows" {
		// 防异常/恶意对端：单事件最多 500 行（正常滚动远小于此）
		const maxLines = 500
		if raw > maxLines {
			raw = maxLines
		} else if raw < -maxLines {
			raw = -maxLines
		}
		return raw * wheelDelta
	}
	return raw
}
