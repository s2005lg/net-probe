package detect

import "github.com/s2005lg/net-probe/internal/report"

var serviceCapabilities = map[string]report.ServiceCapabilities{
	"hysteria2": {
		Traffic:       supported(),
		OnlineClients: supported(),
	},
	"xray": {
		Traffic:       supported(),
		OnlineClients: supported(),
	},
	"sing-box": {
		Traffic:       supported(),
		OnlineClients: unsupported("native_api_unavailable"),
	},
	"anytls": {
		Traffic:       unsupported("native_api_unavailable"),
		OnlineClients: unsupported("native_api_unavailable"),
	},
	"v2ray": {
		Traffic:       unsupported("collector_not_implemented"),
		OnlineClients: unsupported("collector_not_implemented"),
	},
	"shadowsocks": {
		Traffic:       unsupported("collector_not_implemented"),
		OnlineClients: unsupported("collector_not_implemented"),
	},
	"trojan": {
		Traffic:       unsupported("collector_not_implemented"),
		OnlineClients: unsupported("collector_not_implemented"),
	},
	"tuic": {
		Traffic:       unsupported("collector_not_implemented"),
		OnlineClients: unsupported("collector_not_implemented"),
	},
}

func supported() report.MetricCapability {
	return report.MetricCapability{Support: report.CapabilitySupported, Source: "native_api"}
}

func unsupported(reason string) report.MetricCapability {
	return report.MetricCapability{Support: report.CapabilityUnsupported, ReasonCode: reason}
}

func CapabilitiesFor(serviceType string) report.ServiceCapabilities {
	if capabilities, ok := serviceCapabilities[serviceType]; ok {
		return capabilities
	}
	return report.ServiceCapabilities{
		Traffic:       report.MetricCapability{Support: report.CapabilityUnknown},
		OnlineClients: report.MetricCapability{Support: report.CapabilityUnknown},
	}
}
