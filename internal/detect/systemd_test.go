package detect

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct{ out map[string]string }

func (f fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	return f.out[key], nil
}

func TestShowUnit(t *testing.T) {
	r := fakeRunner{out: map[string]string{
		"systemctl show hysteria-server --property=ActiveState,SubState,UnitFileState,NRestarts,MainPID,ExecStart": "ActiveState=active\nSubState=running\nUnitFileState=enabled\nNRestarts=2\nMainPID=123\nExecStart={ path=/usr/local/bin/hysteria ; argv[]=/usr/local/bin/hysteria server -c /etc/hysteria/config.yaml ; ignore_errors=no }",
	}}
	u, err := ShowUnit(context.Background(), r, "hysteria-server")
	if err != nil {
		t.Fatal(err)
	}
	if !u.Active || !u.Enabled || u.MainPID != 123 || u.NRestarts != 2 {
		t.Fatalf("unexpected unit: %+v", u)
	}
	if u.ExecStart != "/usr/local/bin/hysteria" {
		t.Fatalf("ExecStart = %q", u.ExecStart)
	}
}

func TestListUnitNames(t *testing.T) {
	r := fakeRunner{out: map[string]string{
		"systemctl list-unit-files --type=service --no-legend --no-pager": "hysteria-server.service enabled\nxray.service disabled\n\n",
	}}
	got, err := ListUnitNames(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"hysteria-server.service", "xray.service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListUnitNames() = %#v, want %#v", got, want)
	}
}
