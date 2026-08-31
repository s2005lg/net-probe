package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/s2005lg/net-probe/internal/agent"
	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/detect"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

var version = "dev"
var releasePublicKeyHex string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "internal-update-helper" {
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "internal-update-helper accepts no arguments")
			os.Exit(2)
		}
		os.Exit(runUpdateHelper(os.Stderr))
	}
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, agent.Enroll))
}

func runUpdateHelper(stderr io.Writer) int {
	if releasePublicKeyHex == "" {
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
		PublicKey: identity.ReleaseKey, CurrentVersion: version, ControlVersion: controlproto.Version,
		AgentID: identity.AgentID, AgentUID: uid, AgentGID: gid,
	}); err != nil {
		fmt.Fprintln(stderr, "update helper failed")
		return 1
	}
	return 0
}

type enrollFunc func(context.Context, *http.Client, agent.EnrollmentOptions) (*agent.Identity, error)

type residentRunner interface {
	Run(context.Context) error
}

type reloadableResident interface {
	Reload() error
}

type cliDependencies struct {
	runOnce     func(context.Context, *config.Config, string, detect.Runner) int
	newResident func(string, *config.Config, string, detect.Runner) (residentRunner, error)
}

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer, enroll enrollFunc) int {
	return runCLIWithDependencies(args, stdin, stdout, stderr, enroll, cliDependencies{
		runOnce: agent.Run,
		newResident: func(path string, cfg *config.Config, version string, runner detect.Runner) (residentRunner, error) {
			return agent.NewRuntime(path, cfg, version, runner)
		},
	})
}

func runCLIWithDependencies(args []string, stdin io.Reader, stdout, stderr io.Writer, enroll enrollFunc, deps cliDependencies) int {
	if len(args) > 0 && args[0] == "enroll" {
		return runEnroll(args[1:], stdin, stdout, stderr, enroll)
	}
	flags := flag.NewFlagSet("net-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cfgPath := flags.String("config", "", "config file path")
	check := flags.Bool("check", false, "validate config and print report preview")
	once := flags.Bool("once", false, "collect and deliver one report, then exit")
	ver := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	_ = once

	if *ver {
		fmt.Fprintln(stdout, version)
		return 0
	}

	path := *cfgPath
	if path == "" {
		path = agent.ConfigDir() + "/config.toml"
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if releasePublicKeyHex != "" {
		if err := agent.VerifyReleaseKeyPin(cfg.Panel.ReleaseKeyFile, releasePublicKeyHex); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}

	runner := detect.ExecRunner{}

	if *check {
		rep, err := agent.Build(context.Background(), cfg, version, runner)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}

	if *once {
		return deps.runOnce(context.Background(), cfg, version, runner)
	}
	runtime, err := deps.newResident(path, cfg, version, runner)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				if reloadable, ok := runtime.(reloadableResident); ok {
					if err := reloadable.Reload(); err != nil {
						fmt.Fprintln(stderr, "reload:", err)
					}
				}
			}
		}
	}()
	if err := runtime.Run(ctx); err != nil {
		stop()
		<-done
		fmt.Fprintln(stderr, err)
		return 1
	}
	stop()
	<-done
	return 0
}

func runEnroll(args []string, stdin io.Reader, stdout, stderr io.Writer, enroll enrollFunc) int {
	flags := flag.NewFlagSet("net-probe enroll", flag.ContinueOnError)
	flags.SetOutput(stderr)
	panelURL := flags.String("panel-url", "", "Panel HTTPS URL")
	fingerprint := flags.String("ca-fingerprint", "", "Panel CA SHA-256 fingerprint")
	codeStdin := flags.Bool("code-stdin", false, "read the one-time enrollment code from stdin")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	if !*codeStdin || *panelURL == "" || *fingerprint == "" {
		fmt.Fprintln(stderr, "enroll requires --panel-url, --ca-fingerprint, and --code-stdin")
		return 2
	}
	reader := bufio.NewReader(io.LimitReader(stdin, 4097))
	code, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		fmt.Fprintln(stderr, "read enrollment code")
		return 2
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 4096 {
		fmt.Fprintln(stderr, "invalid enrollment code")
		return 2
	}
	enrollmentConfig := config.Default()
	configPath := filepath.Join(agent.ConfigDir(), "config.toml")
	if loaded, err := config.Load(configPath); err == nil {
		enrollmentConfig = loaded
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, err)
		return 2
	}
	enrollmentConfig.Panel.URL = *panelURL
	if err := enrollmentConfig.Validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	identity, err := enroll(context.Background(), nil, agent.EnrollmentOptions{
		PanelURL: *panelURL, CAFingerprint: *fingerprint, Code: code,
		PKIDir: filepath.Dir(enrollmentConfig.Panel.CAFile), NodeID: agent.NodeID(enrollmentConfig), Version: version,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "enrolled agent %s\n", identity.AgentID)
	return 0
}
