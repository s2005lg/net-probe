package detect

import "testing"

func TestCapabilitiesFor(t *testing.T) {
	tests := []struct {
		service, traffic, online, trafficReason, onlineReason string
	}{
		{"hysteria2", "supported", "supported", "", ""},
		{"xray", "supported", "supported", "", ""},
		{"sing-box", "supported", "unsupported", "", "native_api_unavailable"},
		{"anytls", "unsupported", "unsupported", "native_api_unavailable", "native_api_unavailable"},
		{"v2ray", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"shadowsocks", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"trojan", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"tuic", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"custom", "unknown", "unknown", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			got := CapabilitiesFor(tt.service)
			if string(got.Traffic.Support) != tt.traffic || string(got.OnlineClients.Support) != tt.online {
				t.Fatalf("capabilities = %+v", got)
			}
			if got.Traffic.ReasonCode != tt.trafficReason || got.OnlineClients.ReasonCode != tt.onlineReason {
				t.Fatalf("reasons = %+v", got)
			}
		})
	}
}
