//go:build !windows

package server

import "syscall"

// restartSysProcAttr detaches the handed-over daemon from this process's
// session so it survives the terminal that started the old one.
func restartSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
