package discovery

import (
	"testing"
)

func TestDiscoveryService(t *testing.T) {
	s1 := NewService(8080, "192.168.1.100", DeviceCapabilities{
		Audio:   true,
		Mouse:   true,
		Control: true,
	})
	s1.SetName("Test-Host-1")

	if s1.ID() == "" {
		t.Fatal("expected non-empty node ID")
	}
	if s1.Name() != "Test-Host-1" {
		t.Fatalf("expected name Test-Host-1, got %s", s1.Name())
	}

	// Test Manual Peer addition
	p := s1.AddManualPeer("192.168.1.105:8080", "Office-PC")
	if p.Target != "192.168.1.105:8080" {
		t.Fatalf("expected target 192.168.1.105:8080, got %s", p.Target)
	}

	peers := s1.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}

	// Test Inbound registration
	c := s1.RegisterInboundClient("client-99", "192.168.1.200:54321", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X)")
	if c.OS != "iOS" {
		t.Fatalf("expected OS iOS, got %s", c.OS)
	}

	peers = s1.GetPeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	// Test UpdateProvides
	s1.UpdateProvides(false, true, false, "USB Mic")
	caps := s1.GetProvides()
	if caps.Audio || !caps.Mouse || caps.Control {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestDefaultNodeName(t *testing.T) {
	name := DefaultNodeName()
	if name == "" {
		t.Fatal("expected non-empty default node name")
	}
}
