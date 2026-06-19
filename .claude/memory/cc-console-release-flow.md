---
name: cc-console-release-flow
description: cc-console 发新版本时必须同步改的版本号位置 + 发布命令
metadata: 
  node_type: memory
  type: project
  originSessionId: 9ecb6dd0-7730-434f-ba58-ec39d648412b
---

发布 cc-console 新版本（如 v1.2.0）时，**必须先改代码里的版本号**，再打 tag + 创建 release。

**版本号唯一定义点**：`service/monitor_service.go` 的 `const Version`（约 121 行）。仓库里其他 version 字样（`frontend/package.json` 的 `0.0.0`、`Taskfile.yml` 的 `version: '3'`）都与应用版本无关，不要动。

**完整发布流程**（GitHub: `pie-tk/cc-console`，SSH 走 [[github-ssh-over-443]] 的 443）：
1. 改 `service/monitor_service.go` 的 `Version` 常量为新版本号
2. `go build -ldflags="-H windowsgui -s -w" -o cc-console.exe .`（前端先 `cd frontend && npm run build`）
3. git commit + push
4. `git tag v1.2.0 && git push origin v1.2.0`
5. `gh release create v1.2.0 cc-console.exe --title v1.2.0 --notes "..."`
6. **验证**：启动 build 出的 exe，确认「关于」页版本号 == 目标版本。Version 是编译期常量烤进 exe，源码改了不重 build 不会变；且应用关窗只是隐藏到托盘 + 单实例，旧进程会一直显示旧版本号——必须从托盘菜单「退出」彻底杀掉旧实例再启动新的才生效。

**覆盖/重传已有 release**（发布后发现 exe 版本号错了，不想改 tag/代码）：
`gh release upload v1.1.0 cc-console.exe --clobber`  # --clobber 覆盖同名 asset，不动 tag 与 git

**Why:** Version 是 Go 编译期常量，烤进 exe，改源码不重 build 不变。2026-06-11 v1.1.0 release 发布时 build 用了旧常量，exe 内部仍是 1.0.0，事后用 `gh release upload --clobber` 重传正确 exe 修复——靠记容易漏，所以固化此流程。
**How to apply:** 用户说「发新版本 / release / 上传」时，按上面 6 步走，第 1 步（改常量）和第 6 步（验证实际版本）最容易漏，优先确认。
