package detect

import (
	"context"
	"testing"

	"github.com/s2005lg/net-probe/internal/config"
)

func TestDetectKnownUnit(t *testing.T) {
	reg, _ := NewRegistry([]Template{{
		ID: "hysteria2", Name: "Hysteria2",
		Units: []string{"hysteria-server"}, BinaryPatterns: []string{"hysteria"},
		VersionCmd: []string{"version"},
	}})
	r := fakeRunner{out: map[string]string{
		"systemctl list-unit-files --type=service --no-legend --no-pager":                                          "hysteria-server.service enabled\n",
		"systemctl show hysteria-server --property=ActiveState,SubState,UnitFileState,NRestarts,MainPID,ExecStart": "ActiveState=active\nUnitFileState=enabled\nMainPID=10\nExecStart={ path=/usr/local/bin/hysteria }",
	}}
	svcs, err := Detect(context.Background(), reg, config.DetectConfig{}, Deps{Runner: r, ProcRoot: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 || svcs[0].Type != "hysteria2" || !svcs[0].Active {
		t.Fatalf("svcs = %+v", svcs)
	}
}
