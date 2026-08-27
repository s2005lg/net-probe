package detect

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/s2005lg/net-probe/internal/report"
	"gopkg.in/yaml.v3"
)

type protocolConfig struct {
	Inbounds []map[string]any `yaml:"inbounds"`
}

func DiscoverProtocols(serviceType string, execArgs, fallbackPaths []string) (*report.ProtocolInfo, error) {
	key, flags, ok := protocolDiscoverySpec(serviceType)
	if !ok {
		return &report.ProtocolInfo{State: "unknown", Items: []string{}}, nil
	}

	paths := configPathsFromArgs(execArgs, flags)
	usesExplicitPaths := len(paths) > 0
	if !usesExplicitPaths {
		paths = fallbackPaths
	}

	protocols := map[string]struct{}{}
	readConfig := false
	for _, path := range uniquePaths(paths) {
		files, err := protocolConfigFiles(path)
		if err != nil {
			if !usesExplicitPaths && isNotExist(err) {
				continue
			}
			return protocolError(err)
		}
		for _, file := range files {
			contents, err := os.ReadFile(file)
			if err != nil {
				return protocolError(err)
			}
			var config protocolConfig
			if err := yaml.Unmarshal(contents, &config); err != nil {
				return protocolError(err)
			}
			readConfig = true
			for _, inbound := range config.Inbounds {
				if value, ok := inbound[key].(string); ok && strings.ToLower(value) == "vless" {
					protocols["vless"] = struct{}{}
				}
			}
		}
	}
	if !readConfig {
		return &report.ProtocolInfo{State: "unknown", Items: []string{}}, nil
	}

	items := make([]string, 0, len(protocols))
	for protocol := range protocols {
		items = append(items, protocol)
	}
	sort.Strings(items)
	return &report.ProtocolInfo{State: "ok", Items: items, Source: "config"}, nil
}

func protocolDiscoverySpec(serviceType string) (string, []string, bool) {
	switch serviceType {
	case "xray":
		return "protocol", []string{"-config", "-c", "-confdir"}, true
	case "sing-box":
		return "type", []string{"-c", "--config", "-C", "--config-directory"}, true
	default:
		return "", nil, false
	}
}

func configPathsFromArgs(args, flags []string) []string {
	var paths []string
	for i, arg := range args {
		for _, flag := range flags {
			if arg == flag && i+1 < len(args) {
				paths = append(paths, args[i+1])
				break
			}
			if value, ok := strings.CutPrefix(arg, flag+"="); ok && value != "" {
				paths = append(paths, value)
				break
			}
		}
	}
	return paths
}

func protocolConfigFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().IsRegular() {
		return []string{path}, nil
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("protocol configuration is not a regular file or directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isConfigExtension(entry.Name()) {
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if entryInfo.Mode().IsRegular() {
			files = append(files, filepath.Join(path, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func isConfigExtension(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func isNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || strings.Contains(err.Error(), fs.ErrNotExist.Error()))
}

func protocolError(err error) (*report.ProtocolInfo, error) {
	return &report.ProtocolInfo{State: "error", Items: []string{}}, err
}
