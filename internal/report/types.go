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
	Tx uint64 `json:"tx"`
	Rx uint64 `json:"rx"`
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
	Status    string   `json:"status"`
	Error     string   `json:"error,omitempty"`
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
