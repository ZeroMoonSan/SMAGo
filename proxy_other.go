//go:build !windows

package main

func detectWindowsProxy() (string, bool) { return "", false }
