package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/s2005lg/net-probe/internal/agent"
	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/controlproto"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

var releasePublicKeyHex string

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "net-probe-update-helper accepts no arguments")
		os.Exit(2)
	}
	os.Exit(run(os.Stderr))
}

func run(stderr *os.File) int {
	publicKey, err := hex.DecodeString(releasePublicKeyHex)
	if err != nil || len(publicKey) != 32 {
		fmt.Fprintln(stderr, "release verification key is not embedded")
		return 2
	}
	configPath := filepath.Join(agent.ConfigDir(), "config.toml")
	cfg, err := config.Load(configPath)
	if err != nil || cfg.Validate() != nil {
		fmt.Fprintln(stderr, "load Agent configuration")
		return 2
	}
	if err := agent.VerifyReleaseKeyPin(cfg.Panel.ReleaseKeyFile, releasePublicKeyHex); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	identity, err := agent.LoadIdentity(cfg.Panel.URL, filepath.Dir(cfg.Panel.CAFile))
	if err != nil {
		fmt.Fprintln(stderr, "load Agent identity")
		return 2
	}
	account, err := user.Lookup("net-probe")
	if err != nil {
		fmt.Fprintln(stderr, "resolve net-probe account")
		return 2
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 {
		fmt.Fprintln(stderr, "invalid net-probe account")
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "update helper requires Linux")
		return 2
	}
	if err := npupdate.RunHelper(context.Background(), npupdate.RootHelperOptions{
		PublicKey: identity.ReleaseKey, ControlVersion: controlproto.Version,
		AgentID: identity.AgentID, AgentUID: uid, AgentGID: gid,
	}); err != nil {
		fmt.Fprintln(stderr, "update helper failed")
		return 1
	}
	return 0
}
