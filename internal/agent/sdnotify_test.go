package agent

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSDNotifySendsExactLifecycleDatagrams(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "np-sdnotify-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "notify.sock")
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", path)

	for _, test := range []struct {
		name string
		call func() error
		want string
	}{
		{name: "ready", call: NotifyReady, want: "READY=1"},
		{name: "watchdog", call: NotifyWatchdog, want: "WATCHDOG=1"},
		{name: "stopping", call: NotifyStopping, want: "STOPPING=1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err != nil {
				t.Fatal(err)
			}
			if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 128)
			n, _, err := listener.ReadFromUnix(buf)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(buf[:n]); got != test.want {
				t.Fatalf("datagram=%q want=%q", got, test.want)
			}
		})
	}
}

func TestSDNotifyIsNoopWithoutSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := NotifyReady(); err != nil {
		t.Fatal(err)
	}
	confirmed, err := NotifyReadyConfirmed()
	if err != nil || confirmed {
		t.Fatalf("confirmed=%v err=%v", confirmed, err)
	}
}
