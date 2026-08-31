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
	"path/filepath"
	"strings"

	"github.com/s2005lg/net-probe/internal/agent"
	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/detect"
)

var version = "dev"

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, agent.Enroll))
}

type enrollFunc func(context.Context, *http.Client, agent.EnrollmentOptions) (*agent.Identity, error)

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer, enroll enrollFunc) int {
	if len(args) > 0 && args[0] == "enroll" {
		return runEnroll(args[1:], stdin, stdout, stderr, enroll)
	}
	flags := flag.NewFlagSet("net-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cfgPath := flags.String("config", "", "config file path")
	check := flags.Bool("check", false, "validate config and print report preview")
	once := flags.Bool("once", false, "run once and exit (default behavior)")
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

	ctx := context.Background()
	runner := detect.ExecRunner{}

	if *check {
		rep, err := agent.Build(ctx, cfg, version, runner)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}

	return agent.Run(ctx, cfg, version, runner)
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
