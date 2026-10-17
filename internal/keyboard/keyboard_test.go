package keyboard

import (
	"testing"
	"time"
)

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
