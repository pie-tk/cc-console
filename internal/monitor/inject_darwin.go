//go:build darwin

package monitor

import "fmt"

func init() {
	Injector = &stubInjector{}
}

type stubInjector struct{}

func (s *stubInjector) SendClear(pid int) error {
	return fmt.Errorf("macOS 控制台注入尚未实现")
}

func (s *stubInjector) SendRewind(pid int) error {
	return fmt.Errorf("macOS 控制台注入尚未实现")
}

func (s *stubInjector) SendPrompt(pid int, text string) error {
	return fmt.Errorf("macOS 控制台注入尚未实现")
}

func (s *stubInjector) SendAskAnswer(pid int, actions string) error {
	return fmt.Errorf("macOS 控制台注入尚未实现")
}

func (s *stubInjector) ShowWindow(pid int) error {
	return fmt.Errorf("macOS 窗口置前尚未实现")
}

func (s *stubInjector) CloseInstance(pid int) (string, error) {
	return "", fmt.Errorf("macOS 关闭实例尚未实现")
}
