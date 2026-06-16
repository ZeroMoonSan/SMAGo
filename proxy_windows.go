//go:build windows

package main

import (
	"golang.org/x/sys/windows/registry"
	"os"
	"strings"
)

// detectWindowsProxy reads the user's "Internet Options" proxy settings
// from the registry and exports them as standard env vars so the stdlib's
// http.ProxyFromEnvironment picks them up.
//
// Windows stores two relevant values under HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings:
//   - ProxyEnable (DWORD, 1 = on)
//   - ProxyServer (REG_SZ) — e.g. "127.0.0.1:8888" or "http=127.0.0.1:8888;https=127.0.0.1:8888"
//   - ProxyOverride (REG_SZ) — comma-separated hosts to bypass; expanded to NO_PROXY
func detectWindowsProxy() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()

	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable == 0 {
		return "", false
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return "", false
	}

	override, _, _ := k.GetStringValue("ProxyOverride")
	return exportProxy(server, override)
}

func exportProxy(server, override string) (string, bool) {
	scheme := "http://"
	host := server
	if !strings.Contains(server, "=") {
		// Plain "host:port" — apply to both http and https
	} else {
		// Form "http=h:1;https=h:2" — pick the https one (or http fallback)
		parts := strings.Split(server, ";")
		var httpP, httpsP string
		for _, p := range parts {
			kv := strings.SplitN(p, "=", 2)
			if len(kv) != 2 {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(kv[0])) {
			case "http":
				httpP = strings.TrimSpace(kv[1])
			case "https":
				httpsP = strings.TrimSpace(kv[1])
			}
		}
		if httpsP != "" {
			host = httpsP
			scheme = "https://"
		} else if httpP != "" {
			host = httpP
		} else {
			return "", false
		}
	}

	url := scheme + host
	if err := os.Setenv("HTTP_PROXY", url); err != nil {
		return "", false
	}
	if err := os.Setenv("HTTPS_PROXY", url); err != nil {
		return "", false
	}
	if override != "" {
		override = strings.ReplaceAll(override, ";", ",")
		_ = os.Setenv("NO_PROXY", override)
		_ = os.Setenv("no_proxy", override)
	}
	return url, true
}
