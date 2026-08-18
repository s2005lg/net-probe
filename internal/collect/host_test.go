package collect

import "testing"

func TestParseLoadavg(t *testing.T) {
	l1, l5, l15, err := ParseLoadavg("0.20 0.30 0.40 1/123 456\n")
	if err != nil {
		t.Fatal(err)
	}
	if l1 != 0.20 || l5 != 0.30 || l15 != 0.40 {
		t.Fatalf("load = %v %v %v", l1, l5, l15)
	}
}

func TestParseMeminfo(t *testing.T) {
	total, avail, err := ParseMeminfo("MemTotal:       1024 kB\nMemAvailable:    512 kB\n")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1024*1024 || avail != 512*1024 {
		t.Fatalf("total=%d avail=%d", total, avail)
	}
}

func TestParseDf(t *testing.T) {
	pct, err := ParseDf("Filesystem 1024-blocks Used Available Capacity Mounted on\noverlay 1000 600 400 60% /\n")
	if err != nil {
		t.Fatal(err)
	}
	if pct != 60.0 {
		t.Fatalf("pct = %v", pct)
	}
}

func TestParseOSRelease(t *testing.T) {
	name, version := ParseOSRelease("PRETTY_NAME=\"Debian GNU/Linux 11 (bullseye)\"\nVERSION_ID=\"11\"\n")
	if name != "Debian GNU/Linux 11 (bullseye)" || version != "11" {
		t.Fatalf("os = %q %q", name, version)
	}
}
