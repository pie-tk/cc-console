# Memory Index

- [Claude Code 实例检测](claude-code-instance-detection.md) — 本机如何探测运行中的 Claude Code 实例及其 busy/idle 状态（注意 claude.exe 同名双产品陷阱）
- [GitHub SSH 走 443](github-ssh-over-443.md) — 本机 SSH 22 端口被代理 fake-IP 拦截，GitHub 必须用 ssh.github.com:443
- [发布流程](claude-monitor-release-flow.md) — 发新版本前必须先改 `service/monitor_service.go` 的 Version 常量，再 tag + release
