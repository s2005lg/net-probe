package detect

import "testing"

func TestBuiltinRegistryMatch(t *testing.T) {
	ts, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(ts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.FindUnit("hysteria-server"); !ok {
		t.Fatal("hysteria-server not matched")
	}
	if got, ok := reg.FindBinary("/usr/local/bin/xray"); !ok || got.ID != "xray" {
		t.Fatalf("xray binary match = %v", got)
	}
}
