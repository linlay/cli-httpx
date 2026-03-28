# httpx

`httpx` 是一个面向智能体和自动化脚本的 HTTP CLI，用来把“登录、带状态请求、提取响应字段”固化进 profile 配置。

用户安装、测试、发版看这个 README；设计、状态模型和内部机制见 [CLAUDE.md](./CLAUDE.md)。

## 安装

优先从 GitHub Releases 下载对应平台压缩包：

- macOS Apple Silicon：`httpx_vX.Y.Z_darwin_arm64.tar.gz`
- macOS Intel：`httpx_vX.Y.Z_darwin_amd64.tar.gz`
- Linux ARM64：`httpx_vX.Y.Z_linux_arm64.tar.gz`
- Linux AMD64：`httpx_vX.Y.Z_linux_amd64.tar.gz`

解压后可以先确认版本信息：

```bash
tar -xzf httpx_v0.1.0_darwin_arm64.tar.gz
./httpx version
./httpx --version
```

如果从源码构建：

```bash
go build -o ./httpx ./cmd/httpx
go install github.com/linlay/cli-httpx/cmd/httpx@latest
```

## 快速上手

最常见的调用形式是：

```bash
httpx <profile> <action>
httpx --param key=value <profile> <action>
```

最小 profile 示例：

```toml
version = 1
base_url = "https://api.example.com"
login_action = "login"

[actions.login]
method = "POST"
path = "/session"
form = { username = { from = "env", key = "DEMO_USER" }, password = { from = "env", key = "DEMO_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }

[actions.me]
path = "/me"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
extract = ".body"
```

使用步骤：

1. 写一个 `demo.toml`
2. 设置环境变量
3. 执行 `httpx --config . demo login`
4. 执行 `httpx --config . demo me`

仓库内的通用示例配置见 [examples/config.toml](./examples/config.toml)。

实际站点的 profile 更适合放在用户本地配置目录：

- `$XDG_CONFIG_HOME/httpx`
- 或 `~/.config/httpx`

如果终端访问目标站点需要走代理，可以在 profile 顶层或 action 中配置：

```toml
proxy = "http://127.0.0.1:8001"
```

action 级别的 `proxy` 会覆盖 profile 级别的 `proxy`。未配置时，CLI 默认直连，不再自动继承环境变量代理。

## 常用命令

```bash
httpx <profile> login
httpx <profile> me
httpx --inspect <profile> me
httpx version
httpx --version
```

全局参数可以放在命令前，也可以放在命令后，例如：

```bash
httpx --config ./examples --format json demo me
httpx demo --format json --config ./examples me
```

常用全局参数：

- `--config <dir>`：配置目录，读取 `<dir>/<profile>.toml`
- `--format json|body`：输出格式，默认 `body`
- `--param key=value`：传入运行时参数，可重复
- `--timeout <duration>`：覆盖配置超时
- `--state-dir <path>`：覆盖默认状态目录
- `--inspect`：只编译请求，不发请求
- `--reveal`：仅 `--inspect` 下显示真实敏感值

## State 目录约定

`httpx` 的 state 用来保存登录 cookie、`save` 提取出的 token 和最近一次登录时间。它是本地运行时状态，不建议放到 `/tmp` 这类沙箱或容器退出后即丢失的目录。

推荐约定：

- 默认目录就是 `~/.local/httpx-state`
- 如果要显式指定路径，统一使用 `--state-dir "$HOME/.local/httpx-state"`
- 不建议默认放到 `~/.secret/httpx` 或 `~/.secret/httpx-state`；`state` 更适合放在用户级 state 目录，而不是和静态 secret 配置混用

容器或沙箱里要特别注意：

- 能否持久化，关键不在目录名，而在于这个目录是否挂载到宿主机或持久卷
- 如果 `HOME` 本身是持久挂载，直接用默认目录即可
- 如果 `HOME` 不是持久挂载，即使写到 `~/.local/...`，容器销毁后也一样会丢

推荐调用方式：

```bash
./httpx --format json nexus login
./httpx --format json nexus profile
```

或者：

```bash
./httpx --state-dir "$HOME/.local/httpx-state" --format json nexus login
./httpx --state-dir "$HOME/.local/httpx-state" --format json nexus profile
```

部署时建议由启动脚本或运维预创建 state 目录并设置为 `0700`。state 文件本身由 `httpx` 写为 `0600`。

## 测试

运行测试：

```bash
go test ./...
```

如果网络不稳定，可以显式带上 Go 代理：

```bash
GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn go test ./...
```

## 手工发布

推荐把 Git tag 作为正式版本号来源，例如 `v0.1.0`。

1. 跑测试：

```bash
go test ./...
```

2. 创建并推送 tag：

```bash
git tag v0.1.0
git push origin v0.1.0
```

3. 在 tag 对应提交上本地打包：

```bash
scripts/release/build.sh v0.1.0
```

如果网络不稳定，可以显式带上代理环境变量：

```bash
GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn scripts/release/build.sh v0.1.0
```

打包完成后会生成：

- `dist/v0.1.0/httpx_v0.1.0_darwin_amd64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_darwin_arm64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_linux_amd64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_linux_arm64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_checksums.txt`

4. 校验摘要：

```bash
cd dist/v0.1.0
shasum -a 256 -c httpx_v0.1.0_checksums.txt
```

5. 上传到 `cligrep.com` 的 CLI 发布目录：

```bash
ssh singapore02 'mkdir -p /docker/cli-releases/httpx/v0.1.0 /docker/cli-releases/httpx/latest'
scp \
  dist/v0.1.0/httpx_v0.1.0_darwin_amd64.tar.gz \
  dist/v0.1.0/httpx_v0.1.0_darwin_arm64.tar.gz \
  dist/v0.1.0/httpx_v0.1.0_linux_amd64.tar.gz \
  dist/v0.1.0/httpx_v0.1.0_linux_arm64.tar.gz \
  dist/v0.1.0/httpx_v0.1.0_checksums.txt \
  singapore02:/docker/cli-releases/httpx/v0.1.0/
```

6. 在服务器上更新稳定入口：

```bash
ssh singapore02 '
  set -euo pipefail
  cd /docker/cli-releases/httpx
  mkdir -p latest
  ln -sfn ../v0.1.0/httpx_v0.1.0_darwin_amd64.tar.gz latest/httpx_darwin_amd64.tar.gz
  ln -sfn ../v0.1.0/httpx_v0.1.0_darwin_arm64.tar.gz latest/httpx_darwin_arm64.tar.gz
  ln -sfn ../v0.1.0/httpx_v0.1.0_linux_amd64.tar.gz latest/httpx_linux_amd64.tar.gz
  ln -sfn ../v0.1.0/httpx_v0.1.0_linux_arm64.tar.gz latest/httpx_linux_arm64.tar.gz
  (
    cd latest
    shasum -a 256 \
      httpx_darwin_amd64.tar.gz \
      httpx_darwin_arm64.tar.gz \
      httpx_linux_amd64.tar.gz \
      httpx_linux_arm64.tar.gz \
      > checksums.txt
  )
'
```

7. 验证公网下载地址：

```bash
curl -I https://cligrep.com/cli-releases/httpx/v0.1.0/httpx_v0.1.0_linux_amd64.tar.gz
curl -I https://cligrep.com/cli-releases/httpx/latest/httpx_linux_amd64.tar.gz
```

当前分发约定：

- 固定版本：`https://cligrep.com/cli-releases/httpx/vX.Y.Z/...`
- 稳定入口：`https://cligrep.com/cli-releases/httpx/latest/...`
- `latest/checksums.txt` 校验的是 `latest/` 目录下的稳定文件名

## 常见问题

### 登录后的 cookie 或 access token 存在哪里？

默认保存在本地 state 文件里，不保存在配置文件里：

- 默认目录：`~/.local/httpx-state`
- 文件名：`<profile>.json`
- `values`：保存 `save = { ... }` 提取出来的字符串值，例如 access token
- `cookies`：保存登录态 cookie
- `last_login`：记录最近一次登录时间

运行约定补充：

- 不建议把 `--state-dir` 指到 `/tmp/...`
- 容器内如需跨重启保留登录态，应把 `HOME` 或显式 state 目录挂载到持久卷
- 可接受的显式目录示例：`~/.local/httpx-state`
- 不建议默认使用 `~/.secret/httpx` 作为 state 目录

state 文件是本地明文 JSON。更完整的状态模型、写回时机和安全约束见 [CLAUDE.md](./CLAUDE.md)。

### `version = 1` 是发布版本号吗？

不是。它表示配置 schema 版本。CLI 的发布版本来自 Git tag，并在构建时写入二进制。

### 看哪里了解内部设计？

设计、状态模型、cookie 复用、输出模型、发布约定和仓库职责边界见 [CLAUDE.md](./CLAUDE.md)。
