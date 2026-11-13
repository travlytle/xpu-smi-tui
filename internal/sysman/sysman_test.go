package sysman

import "testing"

// The UUID is a label on the exporter's series; its spelling must stay exactly
// what xpu-smi printed, which these two are recorded from.
func TestFormatUUIDMatchesXpuSmi(t *testing.T) {
	b70 := [16]byte{0x86, 0x80, 0x23, 0xe2, 0, 0, 0, 0, 0x19, 0, 0, 0, 0, 0, 0, 0}
	if got, want := formatUUID(b70), "00000000-0000-0019-0000-0000e2238086"; got != want {
		t.Errorf("B70 uuid = %s, want %s", got, want)
	}
	a40 := [16]byte{0x86, 0x80, 0xb1, 0x56, 0x05, 0, 0, 0, 0xb5, 0, 0, 0, 0, 0, 0, 0}
	if got, want := formatUUID(a40), "00000000-0000-00b5-0000-000556b18086"; got != want {
		t.Errorf("A40 uuid = %s, want %s", got, want)
	}
}

func TestEccName(t *testing.T) {
	for state, want := range map[int]string{1: "Enabled", 2: "Disabled", 0: "N/A", -1: "N/A"} {
		if got := eccName(state); got != want {
			t.Errorf("eccName(%d) = %s, want %s", state, got, want)
		}
	}
}
