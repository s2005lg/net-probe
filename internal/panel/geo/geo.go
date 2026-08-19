package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Location struct {
	Country    string
	RegionName string
	City       string
}

type apiResponse struct {
	Status     string `json:"status"`
	Country    string `json:"country"`
	RegionName string `json:"regionName"`
	City       string `json:"city"`
}

var httpClient = &http.Client{Timeout: 4 * time.Second}

func Lookup(ctx context.Context, ip string) (Location, error) {
	if parsed := net.ParseIP(ip); parsed != nil && parsed.IsPrivate() {
		return Location{}, fmt.Errorf("private IP")
	}
	url := "http://ip-api.com/json/" + ip + "?lang=zh-CN&fields=status,country,regionName,city"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Location{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Location{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Location{}, fmt.Errorf("geo provider returned %d", resp.StatusCode)
	}
	var out apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Location{}, err
	}
	if out.Status != "success" {
		return Location{}, fmt.Errorf("geo lookup failed")
	}
	return Location{Country: out.Country, RegionName: out.RegionName, City: out.City}, nil
}

func Format(loc Location) string {
	parts := make([]string, 0, 2)
	if loc.Country != "" {
		parts = append(parts, loc.Country)
	}
	if loc.City != "" {
		parts = append(parts, loc.City)
	} else if loc.RegionName != "" {
		parts = append(parts, loc.RegionName)
	}
	return strings.Join(parts, "-")
}

func IsPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsPrivate()
}
