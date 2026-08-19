package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/s2005lg/net-probe/internal/agent"
	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/detect"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "", "config file path")
	check := flag.Bool("check", false, "validate config and print report preview")
	once := flag.Bool("once", false, "run once and exit (default behavior)")
	ver := flag.Bool("version", false, "print version")
	flag.Parse()
	_ = once

	if *ver {
		fmt.Println(version)
		return
	}

	path := *cfgPath
	if path == "" {
		path = agent.ConfigDir() + "/config.toml"
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx := context.Background()
	runner := detect.ExecRunner{}

	if *check {
		rep, err := agent.Build(ctx, cfg, version, runner)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		return
	}

	os.Exit(agent.Run(ctx, cfg, version, runner))
}
