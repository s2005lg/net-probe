package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverProtocolsXrayVLESS(t *testing.T) {
	path := writeProtocolConfig(t, `{"inbounds":[{"protocol":"vless","settings":{"clients":[{"id":"secret-uuid"}]}},{"protocol":"vless"}]}`)
	got, err := DiscoverProtocols("xray", []string{"xray", "run", "-config", path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "ok" || got.Source != "config" || !reflect.DeepEqual(got.Items, []string{"vless"}) {
		t.Fatalf("protocols = %+v", got)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-uuid") {
		t.Fatalf("secret leaked: %s", b)
	}
}

func TestDiscoverProtocolsSingBoxVLESS(t *testing.T) {
	path := writeProtocolConfig(t, `{"inbounds":[{"type":"vless","users":[{"uuid":"private"}]}]}`)
	got, err := DiscoverProtocols("sing-box", []string{"sing-box", "run", "-c", path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "ok" || !reflect.DeepEqual(got.Items, []string{"vless"}) {
		t.Fatalf("protocols = %+v", got)
	}
}

func TestDiscoverProtocolsConfigResults(t *testing.T) {
	t.Run("valid config without VLESS is empty", func(t *testing.T) {
		path := writeProtocolConfig(t, "inbounds:\n  - protocol: trojan\n")
		got, err := DiscoverProtocols("xray", []string{"xray", "-c=" + path}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "ok" || len(got.Items) != 0 {
			t.Fatalf("protocols = %+v", got)
		}
	})

	t.Run("directory entries are deduplicated", func(t *testing.T) {
		dir := t.TempDir()
		writeProtocolConfigAt(t, filepath.Join(dir, "a.yaml"), "inbounds:\n  - type: vless\n")
		writeProtocolConfigAt(t, filepath.Join(dir, "b.json"), `{"inbounds":[{"type":"vless"}]}`)
		writeProtocolConfigAt(t, filepath.Join(dir, "ignored.txt"), `{"inbounds":[{"type":"vless"}]}`)
		got, err := DiscoverProtocols("sing-box", []string{"sing-box", "-C", dir}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "ok" || !reflect.DeepEqual(got.Items, []string{"vless"}) {
			t.Fatalf("protocols = %+v", got)
		}
	})

	t.Run("fallback path is used without an explicit config", func(t *testing.T) {
		path := writeProtocolConfig(t, `{"inbounds":[{"protocol":"vless"}]}`)
		got, err := DiscoverProtocols("xray", []string{"xray", "run"}, []string{path})
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "ok" || !reflect.DeepEqual(got.Items, []string{"vless"}) {
			t.Fatalf("protocols = %+v", got)
		}
	})
}

func TestDiscoverProtocolsRecognizesConfigFlags(t *testing.T) {
	file := writeProtocolConfig(t, `{"inbounds":[{"protocol":"vless","type":"vless"}]}`)
	dir := t.TempDir()
	writeProtocolConfigAt(t, filepath.Join(dir, "config.json"), `{"inbounds":[{"protocol":"vless","type":"vless"}]}`)
	tests := []struct {
		name        string
		serviceType string
		args        []string
	}{
		{"xray config", "xray", []string{"xray", "-config", file}},
		{"xray config equals", "xray", []string{"xray", "-config=" + file}},
		{"xray c", "xray", []string{"xray", "-c", file}},
		{"xray c equals", "xray", []string{"xray", "-c=" + file}},
		{"xray confdir", "xray", []string{"xray", "-confdir", dir}},
		{"xray confdir equals", "xray", []string{"xray", "-confdir=" + dir}},
		{"sing-box c", "sing-box", []string{"sing-box", "-c", file}},
		{"sing-box c equals", "sing-box", []string{"sing-box", "-c=" + file}},
		{"sing-box config", "sing-box", []string{"sing-box", "--config", file}},
		{"sing-box config equals", "sing-box", []string{"sing-box", "--config=" + file}},
		{"sing-box C", "sing-box", []string{"sing-box", "-C", dir}},
		{"sing-box C equals", "sing-box", []string{"sing-box", "-C=" + dir}},
		{"sing-box config directory", "sing-box", []string{"sing-box", "--config-directory", dir}},
		{"sing-box config directory equals", "sing-box", []string{"sing-box", "--config-directory=" + dir}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DiscoverProtocols(tt.serviceType, tt.args, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != "ok" || !reflect.DeepEqual(got.Items, []string{"vless"}) {
				t.Fatalf("protocols = %+v", got)
			}
		})
	}
}

func TestDiscoverProtocolsFailureStateIsSafe(t *testing.T) {
	t.Run("explicit unreadable path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.json")
		got, err := DiscoverProtocols("xray", []string{"xray", "-config", path}, nil)
		if err == nil {
			t.Fatal("DiscoverProtocols error = nil, want an error")
		}
		if got.State != "error" || len(got.Items) != 0 || got.Source != "" {
			t.Fatalf("protocols = %+v", got)
		}
	})

	t.Run("unimplemented service", func(t *testing.T) {
		got, err := DiscoverProtocols("hysteria2", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "unknown" || len(got.Items) != 0 || got.Source != "" {
			t.Fatalf("protocols = %+v", got)
		}
	})
}

func writeProtocolConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	writeProtocolConfigAt(t, path, contents)
	return path
}

func writeProtocolConfigAt(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
