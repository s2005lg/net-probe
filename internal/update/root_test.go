package update

import "testing"

func TestInstalledVersionFromTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		target  string
		want    string
		wantErr bool
	}{
		{name: "tagged version", target: "/opt/net-probe/versions/v1.2.3/net-probe", want: "v1.2.3"},
		{name: "plain version", target: "/opt/net-probe/versions/1.2.3/net-probe", want: "1.2.3"},
		{name: "outside store", target: "/tmp/v1.2.3/net-probe", wantErr: true},
		{name: "invalid version", target: "/opt/net-probe/versions/latest/net-probe", wantErr: true},
		{name: "extra nesting", target: "/opt/net-probe/versions/v1.2.3/bin/net-probe", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := installedVersionFromTarget(test.target)
			if test.wantErr {
				if err == nil {
					t.Fatalf("installedVersionFromTarget(%q) succeeded with %q", test.target, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("installedVersionFromTarget(%q): %v", test.target, err)
			}
			if got != test.want {
				t.Fatalf("installedVersionFromTarget(%q) = %q, want %q", test.target, got, test.want)
			}
		})
	}
}
