# httpx

`httpx` 是一个面向智能体和自动化脚本的 HTTP CLI，用来把“登录、带状态请求、按业务提取响应字段”固化进 site 配置。

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
httpx action <site> <action>
httpx run <site> <action>
httpx inspect <site> <action>
httpx login <site>
```

最小 site 示例：

```toml
version = 1
description = "示例 API 站点"
base_url = "https://api.example.com"
login_action = "login"

[actions.login]
description = "创建会话"
method = "POST"
path = "/session"
form = { username = { from = "env", key = "DEMO_USER" }, password = { from = "env", key = "DEMO_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }

[actions.profile]
description = "获取当前用户资料"
path = "/profile"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
extracts = [{ name = "fields", type = "string[]", description = "需要保留的字段名", example = ["id", "name", "email"] }]
extract_type = "jq"
extract_expr = ".body | {id, name, email}"
```

使用步骤：

1. 写一个 `<site>.toml`
2. 设置环境变量
3. 执行 `httpx --config . login <site>`
4. 执行 `httpx --config . run <site> <action>`
5. 执行 `httpx --config . sites`、`httpx --config . actions <site>`、`httpx --config . action <site> <action>` 做渐进披露

仓库内的通用示例配置见 [examples/config.toml](./examples/config.toml)。

如果你用 `from = "file"` 读取静态 secret，推荐放在 `~/.local/share/httpx/secrets/`；`httpx` 只负责按路径读取文件，本身不保留 `.secrets` 之类的专用目录。

实际站点的 site 配置更适合放在用户本地配置目录：

- `$XDG_CONFIG_HOME/httpx`
- 或 `~/.config/httpx`

如果终端访问目标站点需要走代理，可以在 site 顶层或 action 中配置：

```toml
proxy = "http://127.0.0.1:8001"
```

action 级别的 `proxy` 会覆盖 site 级别的 `proxy`。未配置时，CLI 默认直连，不再自动继承环境变量代理。

## 常用命令

```bash
httpx sites
httpx site <site>
httpx action <site> <action>
httpx actions <site>
httpx state <site>
httpx login <site>
httpx run <site> <action>
httpx inspect <site> <action>
httpx version
httpx --version
```

全局参数可以放在命令前，也可以放在命令后，例如：

```bash
httpx --config ./examples --format json run <site> <action>
httpx run <site> --format json --config ./examples <action>
```

常用全局参数：

- `--config <dir>`：配置目录，读取 `<dir>/<site>.toml`
- `--state <path>`：覆盖默认状态目录
- `--format text|json|body`：输出格式
- `--param key=value`：传入运行时参数，可重复
- `--extract <json-object>`：传入 extractor 运行时输入
- `--timeout <duration>`：覆盖配置超时
- `--reveal`：仅 `inspect` 下显示真实敏感值

如果响应体很大，推荐在 action 里配置扁平的 `extract_*` 字段，先把有用字段裁剪出来再交给智能体：

```toml
[actions.repo]
extract_type = "jq"
extract_expr = ".body | {id, title, owner}"

[actions.page]
extract_type = "regex"
extract_pattern = "token=([A-Za-z0-9_-]+)"
extract_group = 1
```

规则约定：

- JSON 响应用 `extract_type = "jq"` 和 `extract_expr`
- 文本响应用 `extract_type = "regex"`，搭配 `extract_pattern`，可选 `extract_group` 和 `extract_all`
- 旧的 `extract = "..."` 和 `[actions.<name>.extractor]` 都不再支持
- 配置了 `extract_*` 后，`run/login --format body` 默认输出提取结果
- 配置了 `extract_*` 后，`run/login --format json` 只保留 `extract`，不再输出完整 `body`
- `jq` 可以一次组装多个 key，例如 `.body | {id, title, owner}` 或 `.body.items | map({id, name})`
- `--extract` 只给 extractor 用，不参与请求编译；在 jq extractor 里通过 `.extract` 访问
- regex extractor 可在 `extract_pattern` 里使用 `{{extract.key}}`
- action 可用 `params = [...]` 和 `extracts = [...]` 声明运行时输入契约，供 discovery 命令和智能体读取

例如给 extractor 传运行时过滤条件：

```bash
httpx run demo summary --extract '{"days":7,"group":["WRM","OFFICE"]}'
```

格式默认值：

- `run` / `login`：`body`
- `inspect`：`json`
- `sites` / `site` / `action` / `actions` / `state`：`text`

## 渐进披露

发现类命令现在是一级能力：

```bash
httpx sites
httpx site <site>
httpx action <site> <action>
httpx actions <site>
httpx state <site>
```

这些命令用于快速回答四个问题：

- 有哪些 site
- 某个 site 有哪些 actions
- 某个 action 支持哪些 `--param` 和 `--extract`
- 某个 site 是否存在 local state

`httpx state <site>` 只展示摘要，不展示 state 里的具体值。默认摘要字段包括 state 文件路径、是否存在、`last_login`、已保存键数量和 cookie 数量。

## 站点测试流程

推荐按这个顺序测试任意一个 `<site>`：

1. 先跑 discovery 命令，确认站点、action 和本地 state 摘要
2. 再跑 `action <site> <action>`，确认运行时输入契约
3. 再跑 `inspect <site> <action>`，验证配置能否成功编译
4. 最后跑 `run <site> <action>`，验证真实请求

通用命令如下：

```bash
httpx site <site>
httpx action <site> <action>
httpx actions <site>
httpx state <site>
httpx inspect <site> <action>
httpx run <site> <action>
httpx login <site>
```

测试约定：

- `login <site>` 只适用于配置了 `login_action` 的 site
- 如果 site 没有 `login_action`，`login <site>` 预期失败，这是正常行为
- `inspect <site> <action>` 不真正发请求，适合作为无副作用配置检查
- `run <site> <action>` 是否成功，通常依赖目标站点是否允许匿名访问，以及本地是否已有有效 state/cookie

通用批量测试流程：

```bash
httpx actions <site>
httpx inspect <site> <action>
httpx run <site> <action>
```

如果 site 支持登录，建议先执行 `httpx login <site>`，或者先确认 `httpx state <site>` 已存在有效状态。

## State 目录约定

`httpx` 的 state 用来保存登录 cookie、`save` 提取出的 token 和最近一次登录时间。它是本地运行时状态，不建议放到 `/tmp` 这类沙箱或容器退出后即丢失的目录。

推荐约定：

- 如果设置了 `XDG_STATE_HOME`，state 会落到 `$XDG_STATE_HOME/httpx`
- 未设置时，默认目录是 `~/.local/httpx-state`
- 如果要显式指定路径，统一使用 `--state "$HOME/.local/httpx-state"`
- 静态 secret 文件建议放在 `~/.local/share/httpx/secrets/`；不要把 runtime state 和静态 secret 文件混放

容器或沙箱里要特别注意：

- 能否持久化，关键不在目录名，而在于这个目录是否挂载到宿主机或持久卷
- 如果 `HOME` 本身是持久挂载，未设置 `XDG_STATE_HOME` 时直接用默认目录 `~/.local/httpx-state` 即可
- 如果 `HOME` 不是持久挂载，即使写到 `~/.local/...`，容器销毁后也一样会丢

推荐调用方式：

```bash
./httpx --format json login <site>
./httpx --format json run <site> <action>
./httpx site <site>
./httpx actions <site>
```

或者：

```bash
./httpx --state "$HOME/.local/httpx-state" --format json login <site>
./httpx --state "$HOME/.local/httpx-state" --format json run <site> <action>
./httpx --state "$HOME/.local/httpx-state" state <site>
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

- 默认目录：`$XDG_STATE_HOME/httpx` 或 `~/.local/httpx-state`
- 文件名：`<site>.json`
- `values`：保存 `save = { ... }` 提取出来的字符串值，例如 access token
- `cookies`：保存登录态 cookie
- `last_login`：记录最近一次登录时间

运行约定补充：

- 不建议把 `--state` 指到 `/tmp/...`
- 容器内如需跨重启保留登录态，应把 `HOME` 或显式 state 目录挂载到持久卷
- 可接受的显式目录示例：`~/.local/httpx-state`
- 静态 secret 文件建议放到 `~/.local/share/httpx/secrets/`
- 不要把 `state` 目录放进静态 secret 目录里

state 文件是本地明文 JSON。更完整的状态模型、写回时机和安全约束见 [CLAUDE.md](./CLAUDE.md)。

### `version = 1` 是发布版本号吗？

不是。它表示配置 schema 版本。CLI 的发布版本来自 Git tag，并在构建时写入二进制。

### 看哪里了解内部设计？

设计、状态模型、cookie 复用、输出模型、发布约定和仓库职责边界见 [CLAUDE.md](./CLAUDE.md)。
