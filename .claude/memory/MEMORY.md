# Memory Index

- [Claude Code 实例检测](claude-code-instance-detection.md) — 本机如何探测运行中的 Claude Code 实例及其 busy/idle 状态（注意 claude.exe 同名双产品陷阱）
- [GitHub SSH 走 443](github-ssh-over-443.md) — 本机 SSH 22 端口被代理 fake-IP 拦截，GitHub 必须用 ssh.github.com:443
- [发布流程](claude-monitor-release-flow.md) — 发新版本前必须先改 `service/monitor_service.go` 的 Version 常量，再 tag + release
- [编译需同时产出两个文件](always-build-both.md) — 每次代码修改后必须同时构建便携版 exe 和安装包，漏掉安装包会导致 bug fix 未在安装包中体现
