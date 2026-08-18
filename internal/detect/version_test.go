package detect

import (
	"context"
	"testing"
)

func TestVersion(t *testing.T) {
	r := fakeRunner{out: map[string]string{
		"/usr/local/bin/hysteria version": "Version: v2.9.0\n",
	}}
	v, err := Version(context.Background(), r, "/usr/local/bin/hysteria", []string{"version"})
	if err != nil {
		t.Fatal(err)
	}
	if v != "Version: v2.9.0" {
		t.Fatalf("version = %q", v)
	}
}
