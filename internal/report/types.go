package report

type Host struct {
	Hostname          string  `json:"hostname"`
	OS                string  `json:"os"`
	OSVersion         string  `json:"os_version"`
	Kernel            string  `json:"kernel"`
	Arch              string  `json:"arch"`
	IPv4              string  `json:"ipv4,omitempty"`
	IPv6              string  `json:"ipv6,omitempty"`
	UptimeSeconds     int64   `json:"uptime_seconds"`
	Load1             float64 `json:"load1"`
	Load5             float64 `json:"load5"`
	Load15            float64 `json:"load15"`
	MemTotalBytes     uint64  `json:"mem_total_bytes"`
	MemAvailableBytes uint64  `json:"mem_available_bytes"`
	MemUsedPct        float64 `json:"mem_used_pct"`
	DiskTotalBytes    uint64  `json:"disk_total_bytes,omitempty"`
	DiskUsedBytes     uint64  `json:"disk_used_bytes,omitempty"`
	DiskTotalHuman    string  `json:"disk_total_human,omitempty"`
	DiskUsedHuman     string  `json:"disk_used_human,omitempty"`
	DiskUsedPct       float64 `json:"disk_used_pct"`
	UpgradableCount   int     `json:"upgradable_count"`
}

type Listen struct {
	Proto string `json:"proto"`
	Addr  string `json:"addr"`
	Port  uint16 `json:"port"`
}

type Cert struct {
	NotAfter string `json:"not_after"`
	DaysLeft int    `json:"days_left"`
}

type Stats struct {
	Tx            uint64 `json:"tx"`
	Rx            uint64 `json:"rx"`
	OnlineClients uint64 `json:"online_clients"`
}

type CapabilitySupport string

const (
	CapabilitySupported   CapabilitySupport = "supported"
	CapabilityUnsupported CapabilitySupport = "unsupported"
	CapabilityUnknown     CapabilitySupport = "unknown"
)

type MetricCapability struct {
	Support    CapabilitySupport `json:"support"`
	Source     string            `json:"source,omitempty"`
	ReasonCode string            `json:"reason_code,omitempty"`
}

type ServiceCapabilities struct {
	Traffic       MetricCapability `json:"traffic"`
	OnlineClients MetricCapability `json:"online_clients"`
}

type ObservationState string

const (
	ObservationOK            ObservationState = "ok"
	ObservationNotConfigured ObservationState = "not_configured"
	ObservationDisabled      ObservationState = "disabled"
	ObservationError         ObservationState = "error"
)

type TrafficTelemetry struct {
	State     ObservationState `json:"state"`
	TxBytes   *uint64          `json:"tx_bytes,omitempty"`
	RxBytes   *uint64          `json:"rx_bytes,omitempty"`
	ErrorCode string           `json:"error_code,omitempty"`
}

type CountTelemetry struct {
	State     ObservationState `json:"state"`
	Value     *uint64          `json:"value,omitempty"`
	ErrorCode string           `json:"error_code,omitempty"`
}

type ServiceTelemetry struct {
	Traffic       *TrafficTelemetry `json:"traffic,omitempty"`
	OnlineClients *CountTelemetry   `json:"online_clients,omitempty"`
}

type ProtocolInfo struct {
	State  string   `json:"state"`
	Items  []string `json:"items"`
	Source string   `json:"source,omitempty"`
}

type Service struct {
	Type      string   `json:"type"`
	Runtime   string   `json:"runtime"`
	Unit      string   `json:"unit,omitempty"`
	Binary    string   `json:"binary,omitempty"`
	Version   string   `json:"version,omitempty"`
	Active    bool     `json:"active"`
	Enabled   bool     `json:"enabled"`
	MainPID   int      `json:"main_pid,omitempty"`
	NRestarts int      `json:"n_restarts,omitempty"`
	Listen    []Listen `json:"listen"`
	ListenOK  bool     `json:"listen_ok"`
	Cert      *Cert    `json:"cert,omitempty"`
	Stats     *Stats   `json:"stats,omitempty"`
	Protocols     *ProtocolInfo         `json:"protocols,omitempty"`
	Capabilities *ServiceCapabilities  `json:"capabilities,omitempty"`
	Telemetry    *ServiceTelemetry      `json:"telemetry,omitempty"`
	Status       string                 `json:"status"`
	Error        string                 `json:"error,omitempty"`
}

// PopulateLegacyStats projects successful telemetry observations into the
// legacy stats fields for consumers that do not understand the new contract.
func (s *Service) PopulateLegacyStats() {
	if s.Telemetry == nil {
		return
	}
	stats := &Stats{}
	succeeded := false
	if traffic := s.Telemetry.Traffic; traffic != nil && traffic.State == ObservationOK {
		if traffic.TxBytes != nil {
			stats.Tx = *traffic.TxBytes
			succeeded = true
		}
		if traffic.RxBytes != nil {
			stats.Rx = *traffic.RxBytes
			succeeded = true
		}
	}
	if clients := s.Telemetry.OnlineClients; clients != nil && clients.State == ObservationOK && clients.Value != nil {
		stats.OnlineClients = *clients.Value
		succeeded = true
	}
	if succeeded {
		s.Stats = stats
	}
}

type Report struct {
	SchemaVersion string    `json:"schema_version"`
	AgentVersion  string    `json:"agent_version"`
	NodeID        string    `json:"node_id"`
	CollectedAt   string    `json:"collected_at"`
	CollectMS     int64     `json:"collect_ms"`
	Host          Host      `json:"host"`
	Services      []Service `json:"services"`
}
