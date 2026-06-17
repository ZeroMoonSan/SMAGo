package main

import (
	"bytes"
	"net/http"
	"net/url"
	"time"
)

// defaultHTTP is a shared http.Client used by tools that don't need
// per-request configuration. Created with the system proxy in mind.
var defaultHTTP = &http.Client{Timeout: 300 * time.Second}

// setGlobalProxy replaces the shared client with one that uses the given
// proxy URL (typically from the registry or env).
func setGlobalProxy(rawURL string) {
	if rawURL == "" {
		defaultHTTP = &http.Client{Timeout: 300 * time.Second}
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	defaultHTTP = &http.Client{
		Timeout: 300 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
}

// newJSONPost builds an http.Request with Content-Type: application/json
// and Bearer auth if key is non-empty.
func newJSONPost(url string, body []byte, key string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}
