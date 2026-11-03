package mouse

import "testing"

// TestWheelDeltaSamePlatform 同平台换算不得改变原有手感：
// Windows 注入端 notch 单位原值直传（含精度触控板 1/40 细分值），
// macOS 注入端 line 单位原值直传。
func TestWheelDeltaSamePlatform(t *testing.T) {
	cases := []struct {
		name string
		unit string
		raw  int
		os   string
	}{
		{"Windows 标准滚轮一格", WheelUnitNotch, 120, "windows"},
		{"Windows 标准滚轮三格", WheelUnitNotch, 360, "windows"},
		{"Windows 精度触控板 1/40", WheelUnitNotch, 1, "windows"},
		{"Windows 精度触控板 40", WheelUnitNotch, 40, "windows"},
		{"Windows 向上/向下符号", WheelUnitNotch, -120, "windows"},
		{"macOS 滚轮一行", WheelUnitLine, 1, "darwin"},
		{"macOS 滚轮五行", WheelUnitLine, 5, "darwin"},
		{"macOS 向上/向下符号", WheelUnitLine, -3, "darwin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			accum := 0
			if got := WheelDeltaForLocal(c.unit, c.raw, c.os, "", &accum); got != c.raw {
				t.Fatalf("WheelDeltaForLocal(%q,%d,%q)=%d，期望原值 %d", c.unit, c.raw, c.os, got, c.raw)
			}
			if accum != 0 {
				t.Fatalf("同平台换算不应产生余量，实际 %d", accum)
			}
		})
	}
}

// TestWheelDeltaCrossPlatform 跨平台换算：
// Windows notch（120/格）→ macOS 行（1/格）；macOS 行 → Windows notch（×120）。
func TestWheelDeltaCrossPlatform(t *testing.T) {
	// Windows → macOS：标准滚轮一格 = 1 行
	accum := 0
	if got := WheelDeltaForLocal(WheelUnitNotch, 120, "darwin", "windows", &accum); got != 1 {
		t.Fatalf("Win 一格应换算成 1 行，实际 %d", got)
	}
	if got := WheelDeltaForLocal(WheelUnitNotch, -240, "darwin", "windows", &accum); got != -2 {
		t.Fatalf("Win 两格向下应换算成 -2 行，实际 %d", got)
	}
	// Windows 精度触控板：120 个 1/40 增量累积成 1 行
	accum = 0
	lines := 0
	for i := 0; i < 120; i++ {
		lines += WheelDeltaForLocal(WheelUnitNotch, 1, "darwin", "windows", &accum)
	}
	if lines != 1 {
		t.Fatalf("120 个 1/40 增量应累积成 1 行，实际 %d 行（余量 %d）", lines, accum)
	}
	// macOS → Windows：一行 = 120 单位
	if got := WheelDeltaForLocal(WheelUnitLine, 1, "windows", "darwin", &accum); got != 120 {
		t.Fatalf("macOS 一行应换算成 120 单位，实际 %d", got)
	}
	if got := WheelDeltaForLocal(WheelUnitLine, -3, "windows", "darwin", &accum); got != -360 {
		t.Fatalf("macOS 三行向下应换算成 -360 单位，实际 %d", got)
	}
	// 恶意/异常超大行数被限幅
	if got := WheelDeltaForLocal(WheelUnitLine, 100000, "windows", "darwin", &accum); got != 500*wheelDelta {
		t.Fatalf("超大行数应限幅到 500 行（%d 单位），实际 %d", 500*wheelDelta, got)
	}
}

// TestWheelDeltaLegacyPeerFallback 旧版对端没有 wheel_unit 时按对端 OS 推断：
// Windows 旧端是 notch 单位（含精度触控板细分值），macOS 旧端是行单位。
func TestWheelDeltaLegacyPeerFallback(t *testing.T) {
	accum := 0
	// 旧 Windows 端 → 本机 macOS：一格 120 应换算成 1 行
	if got := WheelDeltaForLocal("", 120, "darwin", "windows", &accum); got != 1 {
		t.Fatalf("旧 Win 一格应换算成 1 行，实际 %d", got)
	}
	// 旧 Windows 精度触控板 1/40 → 本机 Windows：原值直传，不得 ×120
	if got := WheelDeltaForLocal("", 1, "windows", "windows", &accum); got != 1 {
		t.Fatalf("旧 Win 精度触控板 1 应原值直传，实际 %d", got)
	}
	// 旧 macOS 端 → 本机 Windows：一行 1 应换算成 120 单位
	if got := WheelDeltaForLocal("", 1, "windows", "darwin", &accum); got != 120 {
		t.Fatalf("旧 macOS 一行应换算成 120 单位，实际 %d", got)
	}
	// 旧 macOS 端 → 本机 macOS：原值
	if got := WheelDeltaForLocal("", 2, "darwin", "darwin", &accum); got != 2 {
		t.Fatalf("旧 macOS 两行应原值直传，实际 %d", got)
	}
}

// TestWheelDeltaUnknownFallback 未知单位/旧对端且 OS 未知时按原值直传，
// 与升级前行为一致，避免错误换算（保守策略）。
func TestWheelDeltaUnknownFallback(t *testing.T) {
	accum := 0
	if got := WheelDeltaForLocal("future-unit", 7, "windows", "windows", &accum); got != 7 {
		t.Fatalf("未知单位应原值直传，实际 %d", got)
	}
	if got := WheelDeltaForLocal("", 1, "windows", "", &accum); got != 1 {
		t.Fatalf("旧对端且 OS 未知应原值直传，实际 %d", got)
	}
	if got := WheelDeltaForLocal("", 120, "darwin", "", &accum); got != 120 {
		t.Fatalf("旧对端且 OS 未知在 macOS 也应原值直传，实际 %d", got)
	}
}

// TestWheelDeltaZero 零增量不触发任何滚动（避免无意义注入）。
func TestWheelDeltaZero(t *testing.T) {
	accum := 0
	if got := WheelDeltaForLocal(WheelUnitNotch, 0, "darwin", "windows", &accum); got != 0 {
		t.Fatalf("零增量应返回 0，实际 %d", got)
	}
}
