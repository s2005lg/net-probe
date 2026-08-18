package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var githubAPIBase = "https://api.github.com"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func FetchLatest(serviceType string) (string, error) {
	repos := map[string]string{
		"hysteria2": "apernet/hysteria",
		"xray":      "XTLS/Xray-core",
		"sing-box":  "SagerNet/sing-box",
		"v2ray":     "v2fly/v2ray-core",
	}
	repo, ok := repos[serviceType]
	if !ok {
		return "", fmt.Errorf("unsupported service %q", serviceType)
	}

	resp, err := httpClient.Get(githubAPIBase + "/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github returned %s", resp.Status)
	}

	var v struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.TagName == "" {
		return "", fmt.Errorf("empty tag_name")
	}
	return strings.TrimPrefix(strings.TrimPrefix(v.TagName, "app/"), "v"), nil
}
