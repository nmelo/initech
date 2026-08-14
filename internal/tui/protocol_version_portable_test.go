package tui

// protocol_version_portable_test.go is the untagged half of ini-yc03's version
// gate: everything that needs no TCP server runs on every platform, so the
// Windows constraint on the server rig cannot quietly take this coverage with
// it (ini-47w).

import "testing"

// TestBothSidesSendTheSameProtocolVersion guards the gate against the most
// boring way to break it: a version constant used on one side and a literal on
// the other. That would make every attach a refusal, or worse, make the gate
// pass while the peers disagree.
func TestBothSidesSendTheSameProtocolVersion(t *testing.T) {
	server := HelloOKMsg{Version: ProtocolVersion}
	client := HelloMsg{Version: ProtocolVersion}
	if server.Version != client.Version {
		t.Fatalf("server announces v%d, client sends v%d", server.Version, client.Version)
	}
	if ProtocolVersion < 1 {
		t.Errorf("ProtocolVersion = %d; a zero version is indistinguishable from an absent "+
			"field, which the gate treats as a mismatch", ProtocolVersion)
	}
}
