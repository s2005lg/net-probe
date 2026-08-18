package detect

import (
	"fmt"
	"regexp"
)

type Template struct {
	ID               string   `yaml:"id"`
	Name             string   `yaml:"name"`
	Units            []string `yaml:"units"`
	BinaryPatterns   []string `yaml:"binary_patterns"`
	VersionCmd       []string `yaml:"version_cmd"`
	Transport        []string `yaml:"transport"`
	CertPaths        []string `yaml:"cert_paths"`
	ListenPorts      []uint16 `yaml:"listen_ports"`
	StatsKind        string   `yaml:"stats_kind"`
	StatsConfigPaths []string `yaml:"stats_config_paths"`

	unitRe []*regexp.Regexp
	binRe  []*regexp.Regexp
}

func (t *Template) Compile() error {
	for _, p := range t.Units {
		re, err := regexp.Compile("^" + p + "$")
		if err != nil {
			return fmt.Errorf("unit pattern %q: %w", p, err)
		}
		t.unitRe = append(t.unitRe, re)
	}
	for _, p := range t.BinaryPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("binary pattern %q: %w", p, err)
		}
		t.binRe = append(t.binRe, re)
	}
	return nil
}

func (t *Template) MatchUnit(unit string) bool {
	for _, re := range t.unitRe {
		if re.MatchString(unit) {
			return true
		}
	}
	return false
}

func (t *Template) MatchBinary(path string) bool {
	for _, re := range t.binRe {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

type Registry struct {
	templates []Template
}

func NewRegistry(ts []Template) (*Registry, error) {
	r := &Registry{templates: ts}
	for i := range r.templates {
		if err := r.templates[i].Compile(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) FindUnit(unit string) (Template, bool) {
	for _, t := range r.templates {
		if t.MatchUnit(unit) {
			return t, true
		}
	}
	return Template{}, false
}

func (r *Registry) FindBinary(path string) (Template, bool) {
	for _, t := range r.templates {
		if t.MatchBinary(path) {
			return t, true
		}
	}
	return Template{}, false
}
