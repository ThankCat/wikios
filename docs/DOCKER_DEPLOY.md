# Docker 部署指南

本文档用于从本地或新机器重新部署 WikiOS。部署入口在项目根目录，生产环境配置在 `deploy/`。

## 文件分工

| 文件 | 用途 |
| --- | --- |
| `docker-compose.yml` | Docker Compose 服务定义，开放 `9025` 端口。 |
| `Dockerfile` | 构建 Go 服务、Web 静态产物和运行镜像。 |
| `deploy/.env.prod` | 生产部署环境变量。真实密钥填到这里时，不要提交到 git。 |
| `deploy/config.prod.yaml` | 容器内使用的生产配置，对应 `/app/deploy/config.prod.yaml`。 |
| `deploy/docker-entrypoint.sh` | 容器启动前自动 clone/pull Wiki，并初始化 qmd collection。 |
| `.env` | 本地开发环境变量。不要用本地 `.env` 直接部署，容易误用本地配置。 |

## 1. 准备生产环境变量

编辑 `deploy/.env.prod`：

```env
DEEPSEEK_API_KEY=your-deepseek-api-key
WIKIOS_DEFAULT_ADMIN_USERNAME=admin
WIKIOS_DEFAULT_ADMIN_PASSWORD=change-me
WIKIOS_WIKI_GIT_URL=ssh://git@ssh.github.com:443/ThankCat/knowledge-base.git
WIKIOS_WIKI_GIT_BRANCH=main
WIKIOS_WIKI_GIT_PULL_ON_START=true
WIKIOS_WIKI_GIT_RESET_ON_START=false
```

重点检查：

- `DEEPSEEK_API_KEY` 必须是真实可用的 LLM API Key。
- `WIKIOS_DEFAULT_ADMIN_PASSWORD` 必须改掉，不要使用默认值。
- `WIKIOS_WIKI_GIT_URL` 推荐使用 `ssh://git@ssh.github.com:443/...`，适合普通 SSH 22 端口被网络限制的场景。
- `WIKIOS_WIKI_GIT_RESET_ON_START=false` 是安全默认值；改成 `true` 会在启动时丢弃 Wiki 仓库内未提交改动。

## 2. 配置 Wiki 仓库 SSH 权限

WikiOS 容器会把宿主机 `~/.ssh` 只读挂载到 `/root/.ssh`。如果需要容器自动拉取、提交、推送 Wiki 仓库，宿主机必须准备可写权限的 GitHub deploy key。

生成专用 SSH key：

```bash
ssh-keygen -t ed25519 -f ~/.ssh/wikios_github -C wikios@local -N ""
chmod 600 ~/.ssh/wikios_github
chmod 644 ~/.ssh/wikios_github.pub
```

配置 SSH over 443：

```sshconfig
Host ssh.github.com
  HostName ssh.github.com
  Port 443
  User git
  IdentityFile ~/.ssh/wikios_github
  IdentitiesOnly yes
```

把 GitHub host key 加入信任：

```bash
ssh-keyscan -p 443 ssh.github.com >> ~/.ssh/known_hosts
```

如果 `ssh-keyscan` 没有返回结果，但 `github.com` 已经可信，可以复制已有 ed25519 记录：

```bash
awk '$1=="github.com" && $2=="ssh-ed25519" {print "[ssh.github.com]:443 "$2" "$3}' ~/.ssh/known_hosts >> ~/.ssh/known_hosts
sort -u ~/.ssh/known_hosts -o ~/.ssh/known_hosts
```

把公钥加到 GitHub 仓库：

```bash
cat ~/.ssh/wikios_github.pub
```

GitHub 页面路径：

```text
ThankCat/knowledge-base -> Settings -> Deploy keys -> Add deploy key
```

需要勾选 `Allow write access`，否则管理后台只能读，不能推送 Wiki 变更。

如果本机 `gh` 已登录并具备权限，也可以直接执行：

```bash
gh repo deploy-key add ~/.ssh/wikios_github.pub \
  -R ThankCat/knowledge-base \
  -t wikios-local \
  --allow-write
```

## 3. Wiki-repo 如何部署

`docker-compose.yml` 使用 Docker named volume 挂载 Wiki：

```yaml
volumes:
  - wikios-wiki-repo:/data/wiki-repo
```

容器启动时，`deploy/docker-entrypoint.sh` 会按下面规则处理 `/data/wiki-repo`：

| 状态 | 行为 |
| --- | --- |
| volume 为空，且配置了 `WIKIOS_WIKI_GIT_URL` | 自动 `git clone` 到 `/data/wiki-repo`。 |
| volume 已经是 git 仓库，且 `WIKIOS_WIKI_GIT_PULL_ON_START=true` | 自动 `git fetch` 和 `git pull --ff-only`。 |
| volume 非空但不是 git 仓库 | 拒绝启动，避免覆盖数据。 |
| 未配置 `WIKIOS_WIKI_GIT_URL` | 不自动 clone/pull，需要你手动准备 `/data/wiki-repo`。 |

首次部署推荐保留 `WIKIOS_WIKI_GIT_PULL_ON_START=true`，这样新 volume 会自动拉取 `ThankCat/knowledge-base`。不要执行 `docker compose down -v`，除非你明确要删除 Wiki volume。

qmd collection 会自动初始化：

```env
WIKIOS_QMD_AUTO_COLLECTION=true
WIKIOS_QMD_INDEX=zy-knowledge-base
```

入口脚本会执行等价于下面的操作：

```bash
qmd --index "$WIKIOS_QMD_INDEX" collection add wiki/ --name wiki
qmd --index "$WIKIOS_QMD_INDEX" update
```

## 4. 启动服务

从项目根目录执行：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

检查服务：

```bash
curl http://127.0.0.1:9025/healthz
```

局域网访问：

```text
http://<宿主机局域网 IP>:9025/chat
http://<宿主机局域网 IP>:9025/admin/login
```

macOS 查看局域网 IP：

```bash
ipconfig getifaddr en0
```

## 5. 验证 Wiki 和推送能力

检查 Wiki remote：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml exec wikios \
  sh -lc 'git -C /data/wiki-repo remote -v'
```

检查 SSH 认证：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml exec wikios \
  sh -lc 'ssh -T -p 443 git@ssh.github.com || true'
```

检查 push 权限：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml exec wikios \
  sh -lc 'git -C /data/wiki-repo push --dry-run origin main'
```

期望结果：

```text
origin ssh://git@ssh.github.com:443/ThankCat/knowledge-base.git
Hi ThankCat/knowledge-base! You have successfully authenticated, but GitHub does not provide shell access.
Everything up-to-date
```

如果 `git push` 要求输入 GitHub 用户名和密码，说明 remote 还是 HTTPS，需要改成 SSH：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml exec wikios \
  sh -lc 'git -C /data/wiki-repo remote set-url origin ssh://git@ssh.github.com:443/ThankCat/knowledge-base.git'
```

## 6. 更新和重新部署

代码更新后：

```bash
git pull --ff-only
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

只重启服务：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml restart wikios
```

查看日志：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml logs -f wikios
```

## 7. 常见问题

### API Key 丢失或 LLM 不可用

确认启动命令使用了生产 env：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

不要只运行 `docker compose up` 后依赖根目录 `.env`，因为根目录 `.env` 可能是本地开发配置。

### `public_answer_system.md` 找不到

这通常是镜像太旧或构建上下文不对。当前 Dockerfile 会复制：

```dockerfile
COPY internal/llm/prompts /app/internal/llm/prompts
```

重新执行：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

### `Permission denied (publickey)`

检查三件事：

- `~/.ssh/config` 里 `ssh.github.com` 是否指定了 `IdentityFile ~/.ssh/wikios_github`。
- GitHub deploy key 是否加到了 `ThankCat/knowledge-base`，并勾选了 `Allow write access`。
- `docker-compose.yml` 是否保留了 `~/.ssh:/root/.ssh:ro` 挂载。

### 局域网无法访问

确认端口映射存在：

```yaml
ports:
  - "9025:9025"
```

然后检查宿主机防火墙，以及访问地址是否使用宿主机局域网 IP，而不是容器 IP。
