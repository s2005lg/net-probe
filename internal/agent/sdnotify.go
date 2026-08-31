package agent

import (
	"net"
	"os"
	"runtime"
	"strings"
)

func NotifyReady() error    { return sdNotify("READY=1") }
func NotifyWatchdog() error { return sdNotify("WATCHDOG=1") }
func NotifyStopping() error { return sdNotify("STOPPING=1") }

func NotifyReadyConfirmed() (bool, error) { return sdNotifyConfirmed("READY=1") }

func sdNotify(message string) error {
	_, err := sdNotifyConfirmed(message)
	return err
}

func sdNotifyConfirmed(message string) (bool, error) {
	name := os.Getenv("NOTIFY_SOCKET")
	if name == "" {
		return false, nil
	}
	if runtime.GOOS == "linux" && strings.HasPrefix(name, "@") {
		name = "\x00" + strings.TrimPrefix(name, "@")
	}
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return false, err
	}
	defer connection.Close()
	_, err = connection.Write([]byte(message))
	return err == nil, err
}
