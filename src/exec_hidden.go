package main

import (
	"context"
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW is the Win32 flag that tells CreateProcess not to
// spawn a console window for the child. Combined with HideWindow it
// covers both cmd.exe-style and GUI-style children.
const createNoWindow = 0x08000000

// hideWindow configures c to launch without a console window on Windows.
// On other OSes this is a no-op.
func hideWindow(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.HideWindow = true
	c.SysProcAttr.CreationFlags |= createNoWindow
}

// hiddenCmd builds an *exec.Cmd with the window hidden (Windows only).
func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	hideWindow(c)
	return c
}

// hiddenCmdContext is the Context-aware variant.
func hiddenCmdContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	hideWindow(c)
	return c
}
