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

	// SimulateKey call shouldn't panic
	SimulateKey(KeyA, true)
	SimulateKey(KeyA, false)

	engine.Stop()
}
