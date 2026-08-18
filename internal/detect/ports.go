package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/s2005lg/net-probe/internal/report"
)

type Socket struct {
	Proto string
	State string
	Addr  string
	Port  uint16
	Inode uint64
}

func ParseProcSockets(tcp4, tcp6, udp4, udp6 string) ([]Socket, error) {
	var out []Socket
	add := func(proto, data string) error {
		for _, line := range strings.Split(data, "\n") {
			f := strings.Fields(line)
			if len(f) < 10 || f[0] == "sl" {
				continue
			}
			addr, port, err := splitAddrPort(f[1])
			if err != nil {
				continue
			}
			inode, _ := strconv.ParseUint(f[9], 10, 64)
			out = append(out, Socket{
				Proto: proto,
				State: f[3],
				Addr:  addr,
				Port:  port,
				Inode: inode,
			})
		}
		return nil
	}
	for _, p := range []struct {
		proto, data string
	}{{"tcp", tcp4}, {"tcp", tcp6}, {"udp", udp4}, {"udp", udp6}} {
		if err := add(p.proto, p.data); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func splitAddrPort(s string) (string, uint16, error) {
	ipHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, fmt.Errorf("bad address %q", s)
	}
	n, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return "", 0, err
	}
	return hexIP(ipHex), uint16(n), nil
}

func hexIP(hex string) string {
	switch len(hex) {
	case 8:
		parts := make([]string, 0, 4)
		for i := 0; i < 8; i += 2 {
			b, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
			parts = append(parts, strconv.FormatUint(b, 10))
		}
		return strings.Join(parts, ".")
	case 32:
		if isAllZero(hex) {
			return "::"
		}
	}
	return hex
}

func isAllZero(s string) bool {
	for _, r := range s {
		if r != '0' {
			return false
		}
	}
	return true
}

func ListenForPID(procRoot string, pid int, socks []Socket) []report.Listen {
	inodes := pidInodes(procRoot, pid)
	var out []report.Listen
	for _, s := range socks {
		if !inodes[s.Inode] {
			continue
		}
		if s.Proto == "tcp" && s.State != "0A" {
			continue
		}
		out = append(out, report.Listen{Proto: s.Proto, Addr: s.Addr, Port: s.Port})
	}
	return out
}

func pidInodes(procRoot string, pid int) map[uint64]bool {
	out := map[uint64]bool{}
	fdDir := filepath.Join(procRoot, strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, e.Name()))
		if err != nil {
			continue
		}
		const prefix = "socket:["
		if strings.HasPrefix(target, prefix) && strings.HasSuffix(target, "]") {
			if inode, err := strconv.ParseUint(target[len(prefix):len(target)-1], 10, 64); err == nil {
				out[inode] = true
			}
		}
	}
	return out
}
