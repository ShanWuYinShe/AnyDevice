package keyboard

import (
	"testing"
	"time"
)

// TestKeyChar 校验字符型按键的美式布局映射（含 Shift 上档）。
func TestKeyChar(t *testing.T) {
	cases := []struct {
		key   string
		shift bool
		want  string
	}{
		{"KeyA", false, "a"}, {"KeyA", true, "A"}, {"KeyZ", true, "Z"},
		{"Digit1", false, "1"}, {"Digit1", true, "!"},
		{"Digit0", false, "0"}, {"Digit2", true, "@"},
		{"Minus", false, "-"}, {"Minus", true, "_"},
		{"Semicolon", true, ":"}, {"Slash", true, "?"},
	}
	for _, c := range cases {
		got, ok := keyChar(c.key, c.shift)
		if !ok || got != c.want {
			t.Errorf("keyChar(%s, shift=%v) = %q,%v; want %q", c.key, c.shift, got, ok, c.want)
		}
	}
	// 非字符键不产出字符（含输入法提交/导航键与方向键）
	for _, key := range []string{ArrowUp, Enter, Tab, Space} {
		if _, ok := keyChar(key, false); ok {
			t.Errorf("%s must not produce a character", key)
		}
	}
}

func TestFriendlyKeyName(t *testing.T) {
	if FriendlyKeyName(Enter) != "回车 (Enter)" {
		t.Fatalf("expected '回车 (Enter)', got '%s'", FriendlyKeyName(Enter))
	}
	if FriendlyKeyName(Space) != "空格 (Space)" {
		t.Fatalf("expected '空格 (Space)', got '%s'", FriendlyKeyName(Space))
	}
	if FriendlyKeyName(KeyA) != "A" {
		t.Fatalf("expected 'A', got '%s'", FriendlyKeyName(KeyA))
	}
	if FriendlyKeyName(Digit1) != "1" {
		t.Fatalf("expected '1', got '%s'", FriendlyKeyName(Digit1))
	}
	if FriendlyKeyName(Numpad0) != "小键盘 0" {
		t.Fatalf("expected '小键盘 0', got '%s'", FriendlyKeyName(Numpad0))
	}
	if FriendlyKeyName(NumpadEnter) != "小键盘 回车" {
		t.Fatalf("expected '小键盘 回车', got '%s'", FriendlyKeyName(NumpadEnter))
	}
	if FriendlyKeyName(NumLock) != "数字锁定 (NumLock)" {
		t.Fatalf("expected '数字锁定 (NumLock)', got '%s'", FriendlyKeyName(NumLock))
	}
}

func TestNumpadKeyChar(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{Numpad0, "0"}, {Numpad1, "1"}, {Numpad2, "2"}, {Numpad3, "3"}, {Numpad4, "4"},
		{Numpad5, "5"}, {Numpad6, "6"}, {Numpad7, "7"}, {Numpad8, "8"}, {Numpad9, "9"},
		{NumpadDecimal, "."}, {NumpadAdd, "+"}, {NumpadSubtract, "-"},
		{NumpadMultiply, "*"}, {NumpadDivide, "/"}, {NumpadEqual, "="},
	}
	for _, c := range cases {
		got, ok := keyChar(c.key, false)
		if !ok || got != c.want {
			t.Errorf("keyChar(%s, false) = %q, %v; want %q", c.key, got, ok, c.want)
		}
	}
}

func TestKeyboardEngineLifecycle(t *testing.T) {
	received := make(chan KeyEvent, 10)
	engine := NewEngine(func(evt KeyEvent) {
		received <- evt
	})

	engine.SetSubscribers(1)
	engine.Start()

	// Ensure engine started without panic
	time.Sleep(50 * time.Millisecond)

	// SimulateKey and ReleaseAll shouldn't panic
	SimulateKey(KeyA, true)
	SimulateKey(ShiftLeft, true)
	ReleaseAll()
	ReleaseAllModifiers()

	engine.Stop()
}
