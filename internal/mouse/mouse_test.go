package mouse

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMouseEventJSON(t *testing.T) {
	evt := MouseEvent{
		Type:      "mouse",
		Action:    "down",
		Button:    "right",
		X:         0.5,
		Y:         0.5,
		RightDown: true,
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed MouseEvent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed.Action != "down" || parsed.Button != "right" || !parsed.RightDown {
		t.Fatalf("unexpected parsed event: %+v", parsed)
	}
}

// TestFillMouseEventGeometry 回归：钩子回调只填 RawX/RawY，所有发出的
// 事件（move/down/up/wheel）都必须补全归一化坐标与屏幕尺寸——缺了会被
// 接收端当成落点 (0,0)，表现为点击跑到屏幕左上角。
func TestFillMouseEventGeometry(t *testing.T) {
	evt := MouseEvent{Action: "down", RawX: 1920, RawY: 540}
	fillMouseEventGeometry(&evt, 3840, 1080)
	if evt.ScreenWidth != 3840 || evt.ScreenHeight != 1080 {
		t.Fatalf("屏幕尺寸未补全: %+v", evt)
	}
	if evt.X != 0.5 || evt.Y != 0.5 {
		t.Fatalf("归一化坐标错误: X=%v Y=%v, want 0.5/0.5", evt.X, evt.Y)
	}

	// 屏幕尺寸非法（GetSystemMetrics 失败）时不得除零，保持零值
	evt2 := MouseEvent{Action: "up", RawX: 100, RawY: 100}
	fillMouseEventGeometry(&evt2, 0, 0)
	if evt2.X != 0 || evt2.Y != 0 || evt2.ScreenWidth != 0 {
		t.Fatalf("非法屏幕尺寸应保持零值: %+v", evt2)
	}
	evt3 := MouseEvent{Action: "wheel", RawX: 10, RawY: 10}
	fillMouseEventGeometry(&evt3, -1, 100)
	if evt3.X != 0 || evt3.ScreenWidth != 0 {
		t.Fatalf("负屏幕尺寸应保持零值: %+v", evt3)
	}
}

func TestMouseWheelEventJSON(t *testing.T) {
	evt := MouseEvent{
		Type:       "mouse",
		Action:     "wheel",
		WheelDelta: 120,
		Timestamp:  time.Now().UnixMilli(),
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed MouseEvent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed.Action != "wheel" || parsed.WheelDelta != 120 {
		t.Fatalf("unexpected parsed wheel event: %+v", parsed)
	}
}

func TestMouseEngineLifecycle(t *testing.T) {
	received := make(chan MouseEvent, 10)
	engine := NewEngine(func(evt MouseEvent) {
		received <- evt
	})

	engine.SetSubscribers(1)
	engine.Start()

	time.Sleep(50 * time.Millisecond)

	// ReleaseAllButtons and Simulate calls shouldn't panic
	SimulateButton("left", true)
	SimulateButton("left", false)
	SimulateScroll(1)
	ReleaseAllButtons()

	engine.Stop()
}
