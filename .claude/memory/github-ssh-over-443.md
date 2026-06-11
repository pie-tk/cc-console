---
name: github-ssh-over-443
description: GitHub SSH must use ssh.github.com:443 on this machine (port 22 blocked by proxy fake-IP)
metadata: 
  node_type: memory
  type: reference
  originSessionId: 31997c1a-341a-4ead-b8d0-fef4e6ee481d
---

On this Windows machine, **GitHub SSH over default port 22 is blocked**: `github.com` resolves to a proxy fake-IP (`198.18.0.37`, the `198.18.0.0/15` range a local proxy/VPN uses for DNS-based routing) and the connection is closed immediately ("Connection closed by 198.18.0.37 port 22"). HTTP/HTTPS to github.com works fine.

**Fix (already encoded in `~/.ssh/config`)** — route GitHub over SSH-on-443:
```
Host github.com
    HostName ssh.github.com
    Port 443
    User git
    IdentityFile ~/.ssh/github_ed25519
    IdentitiesOnly yes
```
- Key: `~/.ssh/github_ed25519` (pubkey comment `157917242@qq.com`, GitHub user `pie-tk`, no passphrase).
- Do **not** reuse `~/.ssh/id_ed25519` (comment `mutagen-sync`, used by Mutagen file-sync) or `gitlab_xingfu_id_ed25519` (work GitLab at `192.168.13.20`).
- `IdentitiesOnly yes` is required, else ssh offers all keys and GitHub rejects with too-many-auth-failures.
- Verify with `ssh -T git@github.com` (exits 1 even on success; look for "Hi pie-tk!").

Verified 2026/06/09: auth + push of the [[claude-code-instance-detection]] monitor repo (`github.com/pie-tk/claude-code-monitor`) both succeeded over 443. If 443 ever also breaks, fall back to HTTPS + a Personal Access Token (port 443 HTTPS is reliable here).
