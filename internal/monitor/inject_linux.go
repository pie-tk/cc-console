//go:build linux

package monitor

import "fmt"

func init() {
	Injector = &linuxStubInjector{}
}

type linuxStubInjector struct{}

func (s *linuxStubInjector) SendClear(pid int) error {
	return fmt.Errorf("Linux 控制台注入尚未实现")
}

func (s *linuxStubInjector) SendRewind(pid int) error {
	return fmt.Errorf("Linux 控制台注入尚未实现")
}

func (s *linuxStubInjector) SendPrompt(pid int, text string) error {
	return fmt.Errorf("Linux 控制台注入尚未实现")
}

func (s *linuxStubInjector) ShowWindow(pid int) error {
	return fmt.Errorf("Linux 窗口置前尚未实现")
}
