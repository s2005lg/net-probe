package detect

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

func Builtin() ([]Template, error) {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := builtinFS.ReadFile("builtin/" + e.Name())
		if err != nil {
			return nil, err
		}
		var t Template
		if err := yaml.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, t)
	}
	return out, nil
}

func LoadCustom(dir string) ([]Template, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var t Template
		if err := yaml.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
