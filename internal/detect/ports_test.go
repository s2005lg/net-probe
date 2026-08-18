package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProcSockets(t *testing.T) {
	tcp := "  sl  local_address rem_address   st\n" +
		"   0: 00000000:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 123456 1 0000000000000000 100 0 0 10 0\n"
	udp := "  sl  local_address rem_address   st\n" +
		"   0: 00000000:20FB 00000000:0000 07 00000000:00000000 00:00000000 00000000 1000 0 123456 1 0000000000000000 100 0 0 10 0\n"
	socks, err := ParseProcSockets(tcp, "", udp, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 2 {
		t.Fatalf("socks = %d", len(socks))
	}
	if socks[0].Port != 8443 || socks[0].Proto != "tcp" {
		t.Fatalf("sock = %+v", socks[0])
	}
	if socks[1].Port != 8443 || socks[1].Proto != "udp" {
		t.Fatalf("sock = %+v", socks[1])
	}
}

func TestListenForPID(t *testing.T) {
	procRoot := t.TempDir()
	fdDir := filepath.Join(procRoot, "42", "fd")
	if err := os.MkdirAll(fdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[123456]", filepath.Join(fdDir, "3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/not/a/socket", filepath.Join(fdDir, "4")); err != nil {
		t.Fatal(err)
	}

	socks := []Socket{
		{Proto: "tcp", State: "0A", Addr: "0.0.0.0", Port: 8443, Inode: 123456},
		{Proto: "tcp", State: "01", Addr: "127.0.0.1", Port: 9999, Inode: 123456},
		{Proto: "udp", State: "07", Addr: "0.0.0.0", Port: 8443, Inode: 123456},
		{Proto: "tcp", State: "0A", Addr: "0.0.0.0", Port: 8080, Inode: 999999},
	}
	got := ListenForPID(procRoot, 42, socks)
	if len(got) != 2 {
		t.Fatalf("ListenForPID = %+v", got)
	}
	if got[0].Proto != "tcp" || got[0].Port != 8443 {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got[1].Proto != "udp" || got[1].Port != 8443 {
		t.Fatalf("got[1] = %+v", got[1])
	}
}

func TestHexIP(t *testing.T) {
	if got := hexIP("00000000"); got != "0.0.0.0" {
		t.Fatalf("ipv4 = %q", got)
	}
	if got := hexIP("00000000000000000000000000000000"); got != "::" {
		t.Fatalf("ipv6 wildcard = %q", got)
	}
}
