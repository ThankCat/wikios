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
WIKIOS_WIKI_GIT_URL=https://github.com/ThankCat/knowledge-base.git
WIKIOS_WIKI_GIT_TOKEN=github_pat_xxx
WIKIOS_WIKI_GIT_USERNAME=x-access-token
WIKIOS_WIKI_GIT_BRANCH=main
WIKIOS_WIKI_GIT_PULL_ON_START=true
WIKIOS_WIKI_GIT_RESET_ON_START=false
WIKIOS_SUPPORT_PHONE=400-1080-106
WIKIOS_SUPPORT_WECOM=企业微信
WIKIOS_LLM_TIMEOUT_SEC=300
WIKIOS_LLM_ADMIN_TIMEOUT_SEC=300
WIKIOS_PUBLIC_RESPONSE_TIMEOUT_SEC=300
WIKIOS_PUBLIC_ANSWER_LOG_ENABLED=true
WIKIOS_PUBLIC_ANSWER_LOG_REDACT=true
WIKIOS_PUBLIC_ANSWER_LOG_RETENTION_DAYS=14
```

重点检查：

- LLM 模型不再通过 `.env` 或 YAML 配置 API Key。服务启动后请进入管理后台，在“模型”模块新增并启用 OpenAI-compatible 模型；未启用模型时 public 问答和知识库助手会明确提示先配置模型。
- public 问答日志默认写入 `.workspace/public_answer_logs/*.jsonl`，并开启密钥、Token、手机号、邮箱脱敏与 14 天保留策略。
- WikiOS 当前不内置后台登录；如果对公网开放，请在反向代理或上游网关增加访问控制。
- `WIKIOS_WIKI_GIT_URL` 推荐使用 HTTPS 地址，例如 `https://github.com/<owner>/<repo>.git`。
- `WIKIOS_WIKI_GIT_TOKEN` 使用 GitHub fine-grained token，至少给 Wiki 仓库 `Contents: Read and write` 权限；Token 只放环境变量，不写入 remote、数据库或前端。
- `WIKIOS_WIKI_GIT_USERNAME` 默认 `x-access-token`，通常不用改。
- `WIKIOS_WIKI_GIT_RESET_ON_START=false` 是安全默认值；改成 `true` 会在启动时丢弃 Wiki 仓库内未提交改动。
- `WIKIOS_SUPPORT_PHONE` 和 `WIKIOS_SUPPORT_WECOM` 是 public query 注入给 LLM 的公开客服联系方式。

## 2. 配置 Wiki 仓库 Token 权限

推荐使用 GitHub fine-grained personal access token：

1. GitHub -> Settings -> Developer settings -> Personal access tokens -> Fine-grained tokens。
2. 选择 Wiki 仓库，例如 `ThankCat/knowledge-base`。
3. Repository permissions 至少开启 `Contents: Read and write`。
4. 把生成的 token 填入 `deploy/.env.prod` 的 `WIKIOS_WIKI_GIT_TOKEN`。

容器启动、后台同步页、知识库助手里的 `git.status/git.commit/git.push` 都会通过非交互 Git runner 使用这个 token。Token 不会写入 `git remote`，remote 会保持普通 HTTPS URL。

### 高级：继续使用 SSH deploy key

如果你明确要使用 SSH，可把 `WIKIOS_WIKI_GIT_URL` 改成 `ssh://git@ssh.github.com:443/<owner>/<repo>.git`，并自行在 Compose 中挂载 `~/.ssh:/root/.ssh:ro`。这种方式需要准备可写权限的 GitHub deploy key。

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
WIKIOS_QMD_INDEX=knowledge-base
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
http://<宿主机局域网 IP>:9025/dashboard
```

macOS 查看局域网 IP：

```bash
ipconfig getifaddr en0
```

## 5. 验证 Wiki 和推送能力

进入后台：

```text
http://<宿主机局域网 IP>:9025/knowledge?view=sync
```

推荐按顺序点击：

1. `刷新`：查看 repo、remote、branch、凭据状态。
2. `检测连接`：后端执行非交互 `git ls-remote`，认证、网络、权限错误会显示 stdout/stderr/exit code。
3. `修复配置`：在不 reset、不丢弃本地改动的前提下设置 remote、branch/upstream；空 Wiki volume 会自动 clone。

也可以用 API 检查：

```bash
curl -sS http://127.0.0.1:9025/api/v1/admin/sync/status
curl -sS -X POST http://127.0.0.1:9025/api/v1/admin/sync/test
```

检查容器内 Wiki remote：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml exec wikios \
  sh -lc 'git -C /data/wiki-repo remote -v'
```

期望结果：

```text
origin https://github.com/ThankCat/knowledge-base.git
```

不要在 remote URL 中写 token；如果看到 `https://token@...` 或 `https://x-access-token:...@...`，请在同步页点击“修复配置”，让 remote 回到普通 HTTPS URL。

## 6. 更新和重新部署

代码更新后：

```bash
git pull --ff-only
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

只要修改了 Go 代码、Web 前端、`internal/llm/prompts/*.md` 或 `configs/*.yaml`，都需要重新 build 镜像。当前 Dockerfile 会把 prompt 和配置复制进镜像，单纯 `restart` 不会应用这些文件的变更。

只重启服务：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml restart wikios
```

查看日志：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml logs -f wikios
```

## 7. 排障说明

### LLM 不可用

WikiOS 不再从环境变量读取默认模型。进入管理后台后，在“模型”模块新增 OpenAI-compatible 模型并启用；未启用模型时，对话会提示先配置模型。

如果后台配置后仍不可用，确认启动命令使用了生产 env：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

不要只运行 `docker compose up` 后依赖根目录 `.env`，因为根目录 `.env` 可能是本地开发配置。

### public routed prompt 找不到

这通常是镜像太旧或构建上下文不对。当前 Dockerfile 会复制：

```dockerfile
COPY internal/llm/prompts /app/internal/llm/prompts
```

公开问答当前使用 `public_router_system.md` 和 `public_specialist_*.md`。

重新执行：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

### 同步页提示认证失败

检查三件事：

- `WIKIOS_WIKI_GIT_URL` 是否是普通 HTTPS 地址。
- `WIKIOS_WIKI_GIT_TOKEN` 是否已配置，并且 token 没有过期。
- GitHub fine-grained token 是否给了目标仓库 `Contents: Read and write`。

### 高级 SSH：`Permission denied (publickey)`

检查三件事：

- `~/.ssh/config` 里 `ssh.github.com` 是否指定了 `IdentityFile ~/.ssh/wikios_github`。
- GitHub deploy key 是否加到了 `ThankCat/knowledge-base`，并勾选了 `Allow write access`。
- 是否在 `docker-compose.yml` 中自行挂载了 `~/.ssh:/root/.ssh:ro`。

### 局域网无法访问

确认端口映射存在：

```yaml
ports:
  - "9025:9025"
```

然后检查宿主机防火墙，以及访问地址是否使用宿主机局域网 IP，而不是容器 IP。
