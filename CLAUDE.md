# CLAUDE.md

`httpx` 的用户安装、测试、手工发布和快速上手说明见 [README.md](./README.md)。

这个文件面向维护者、协作工程师和智能体，描述项目设计、关键语义、状态模型、发布约定和仓库边界。

## 项目定位

`httpx` 是一个面向智能体和自动化脚本的 HTTP CLI。

它要解决的问题：

- 把登录流程、请求参数、状态复用和提取逻辑固化进 profile 配置
- 让调用方主要执行 `httpx <profile> <action>`
- 让“带状态调用接口”和“从响应里提取字段”变成稳定的 CLI 工作流

它不打算解决的问题：

- 不追求成为 `curl` 的完整替代
- 不在 CLI 内建复杂交互式调试界面
- 不把本地 state 文件承诺为稳定的外部 API

## 顶层模型

运行链路可以理解为：

`profile -> action -> compiled request -> execute -> envelope/state`

含义如下：

- `profile`：一个 TOML 文件，对应一个站点或系统
- `action`：profile 中的一个命名请求动作
- `compiled request`：把默认值、action、动态值、CLI 参数合并后的最终请求
- `execute`：真正发起 HTTP 请求、处理 cookie、执行 `extract`/`save`
- `envelope/state`：将结果输出为 body 或 json，并把本地状态落盘

## 配置语义

每个 profile 对应一个 `<profile>.toml` 文件。

最外层常见字段：

- `version`
- `base_url`
- `login_action`
- `timeout`
- `retries`
- `headers`
- `query`
- `cookies`
- `actions`

这里的 `version = 1` 表示配置 schema 版本，不是 CLI 的发布版本号。CLI 发布版本只来自 Git tag，并在构建时通过 `ldflags` 注入到二进制中。

每个 action 可以定义：

- `method`
- `path`
- `headers`
- `cookies`
- `query`
- `body`
- `form`
- `expect_status`
- `extract`
- `save`

## 命令语义

### 普通 action

标准调用形式是：

```bash
httpx <profile> <action>
```

运行时会解析 profile、合并 action 配置、解析动态值、执行请求，并按 `--format` 输出结果。

### `login`

`login` 是保留动作名，不直接对应配置中的 action 名。

它的语义是：

- 要求 profile 配置了 `login_action`
- 执行 `login_action` 指向的真实 action
- 额外刷新本地 cookie 状态
- 如果配置了 `save`，把提取出的值写入本地 state
- 成功执行后写入 `last_login`

### `inspect`

`--inspect` 只编译请求，不真正发请求。

默认会对敏感值做脱敏；显式加 `--reveal` 才展示真实值。

### `version`

`httpx version` 和 `httpx --version` 直接输出构建信息：

- 版本号
- commit
- build time

`version` 是顶层保留命令，不能作为 profile 名使用。

## 动态值解析

当前支持这些动态值来源：

- `env`
- `file`
- `shell`
- `state`
- `param`
- `literal`

用途简述：

- `env`：适合账号、token 等外部注入值
- `file`：适合读取本地密钥或临时凭证
- `shell`：适合从密码管理器或命令输出动态取值
- `state`：适合复用上一次登录或请求保存的状态
- `param`：适合命令行传入的运行时参数
- `literal`：适合配置内固定值

风险和约束：

- `shell` 依赖本机环境，超时或命令失败会导致执行失败
- `state` 依赖本地 state 文件，适合会话复用，不适合作为跨环境共享机制
- `param` 缺失时会失败，除非配置了默认值

## State Persistence

登录后的 cookie 和 access token 都不保存在配置文件里，而是保存在本地 state 文件里。

默认目录规则：

- 如果设置了 `XDG_STATE_HOME`，目录为 `$XDG_STATE_HOME/httpx`
- 否则目录为 `~/.local/state/httpx`
- 也可以用 `--state-dir <path>` 覆盖默认目录

运行约定：

- 不建议把 `--state-dir` 指到 `/tmp/...`，因为这类目录常常跟着沙箱或容器生命周期一起销毁
- 推荐优先使用用户级持久目录：`~/.local/state/httpx`
- 如果需要显式路径，推荐 `--state-dir "$HOME/.local/httpx-state"`
- 不建议默认使用 `~/.secret/httpx` 或 `~/.secret/httpx-state`；这里保存的是 mutable runtime state，不是静态 secret 配置
- 在容器里能否持久化，关键取决于 `HOME` 或 `--state-dir` 是否绑定到宿主机目录或持久卷，而不是路径名本身

每个 profile 对应一个 state 文件：

- 文件名：`<profile>.json`

state 文件当前结构：

```json
{
  "values": {
    "auth.authorization": "Bearer ..."
  },
  "cookies": [
    {
      "name": "session",
      "value": "...",
      "path": "/",
      "domain": "example.com",
      "expires": "2026-03-26T07:00:00Z",
      "secure": true,
      "http_only": true
    }
  ],
  "last_login": "2026-03-26T07:00:00Z"
}
```

字段含义：

- `values`：保存 `save = { ... }` 提取出的字符串值，例如 access token、header 值、业务状态
- `cookies`：保存登录后或请求过程中捕获到的 cookie
- `last_login`：最近一次执行 `httpx <profile> login` 的时间

写入时机：

- 执行请求后，`save` 结果会写入 `state.Values`
- 当前 cookie jar 会快照到 `state.Cookies`
- 如果本次请求动作是 `login`，还会更新 `state.LastLogin`
- 之后统一把 state 写回 `<profile>.json`

实现位置：

- 目录规则：`internal/app/paths.go`
- 结构定义和读写：`internal/app/state.go`
- 写回时机：`internal/app/runtime.go`

## Cookie 机制

`httpx` 自己维护一个持久化 cookie jar，并在 state 中保存 cookie 快照。

关键行为：

- 响应里的 cookie 会写入 jar
- jar 会按 `domain`、`path`、`secure` 和过期时间过滤可用 cookie
- 请求执行结束后，jar 会序列化回 state 文件
- action 中也可以显式配置 `cookies = { ... }`，与 jar 中 cookie 一起参与请求

这意味着：

- 站点登录态可以跨多次 CLI 调用复用
- 过期 cookie 不会继续用于后续请求
- state 文件里既可能有服务器返回的 cookie，也可能有通过 `state` 显式引用的业务字段

## 输出模型

CLI 有两种主要输出模式：

- `--format body`
- `--format json`

`body`：

- 成功时直接输出响应体原文
- 失败时错误信息写到 stderr

`json`：

- 输出结构化 envelope
- 包含 profile、action、status、headers、body、extract、state 更新字段等信息

`inspect` 默认也使用结构化 JSON 输出编译后的请求描述。

## 安全约束

当前实现的安全特征：

- `inspect` 会默认脱敏常见敏感头和动态值
- state 文件以 JSON 明文保存在本地
- state 文件权限写为 `0600`
- state 目录如果不存在，会由程序自动创建；当前实现的目录权限是 `0755`

需要明确的限制：

- `values` 中的 token/access token 是明文保存的
- `cookies` 中的 session cookie 也是明文保存的
- 这些文件不应该提交到仓库
- 共享机器或低信任环境下应显式指定安全的 `--state-dir`
- 更稳妥的部署约定是由启动脚本或运维预创建 state 目录，并将目录权限设置为 `0700`

这次文档方案只澄清当前行为，不引入 keychain 或加密存储。

## 发布机制

发布版本号来源：

- Git tag，例如 `v0.1.0`

构建时注入：

- `internal/buildinfo.Version`
- `internal/buildinfo.Commit`
- `internal/buildinfo.BuildTime`

本地打包脚本：

- `scripts/release/build.sh <version>`

产物约定：

- 输出目录：`dist/<version>/`
- 平台矩阵：`darwin/linux × amd64/arm64`
- 文件名：`httpx_<version>_<goos>_<goarch>.tar.gz`
- 额外生成：`httpx_<version>_checksums.txt`

这个脚本只负责本地构建和校验文件准备，不负责自动上传 GitHub Release。

## 仓库约定

关键目录职责：

- `cmd/httpx`：CLI 主入口
- `internal/app`：参数解析、配置解析、请求编译、执行、state 管理
- `internal/buildinfo`：承接构建注入的版本元信息
- `scripts/release`：本地发布打包脚本
- `examples`：配置示例

文档职责：

- `README.md`：用户手册
- `CLAUDE.md`：设计与维护手册
