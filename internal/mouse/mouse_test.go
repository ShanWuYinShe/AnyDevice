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
