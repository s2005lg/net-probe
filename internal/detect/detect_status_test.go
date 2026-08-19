package detect

import (
	"testing"

	"github.com/s2005lg/net-probe/internal/report"
)

func TestClassifyServiceStatus(t *testing.T) {
	cases := []struct {
		name             string
		active           bool
		cert             *report.Cert
		hasDeclaredPorts bool
		listenOK         bool
		want             string
	}{
		{name: "active ok", active: true, cert: &report.Cert{DaysLeft: 90}, hasDeclaredPorts: false, listenOK: true, want: "ok"},
		{name: "inactive", active: false, cert: nil, want: "error"},
		{name: "expired cert", active: true, cert: &report.Cert{DaysLeft: -1}, want: "error"},
		{name: "expiring cert", active: true, cert: &report.Cert{DaysLeft: 20}, want: "warn"},
		{name: "missing declared ports", active: true, cert: &report.Cert{DaysLeft: 90}, hasDeclaredPorts: true, listenOK: false, want: "warn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := classifyServiceStatus(tc.active, tc.cert, tc.hasDeclaredPorts, tc.listenOK)
			if status != tc.want {
				t.Fatalf("status = %q, want %q", status, tc.want)
			}
		})
	}
}
