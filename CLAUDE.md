# CLAUDE.md

`httpx` 的用户安装、测试、手工发布和快速上手说明见 [README.md](./README.md)。

这个文件面向维护者、协作工程师和智能体，描述项目设计、关键语义、状态模型、发布约定和仓库边界。

## 项目定位

`httpx` 是一个面向智能体和自动化脚本的 HTTP CLI。

它要解决的问题：

- 把登录流程、请求参数、状态复用和提取逻辑固化进 site 配置
- 让调用方主要执行 `httpx run <site> <action>`
- 让调用方可以先发现 `sites -> site -> actions -> state`，再执行具体请求
- 让“带状态调用接口”和“从响应里提取字段”变成稳定的 CLI 工作流

它不打算解决的问题：

- 不追求成为 `curl` 的完整替代
- 不在 CLI 内建复杂交互式调试界面
- 不把本地 state 文件承诺为稳定的外部 API

## 顶层模型

运行链路可以理解为：

`site -> action -> compiled request -> execute -> envelope/state`

含义如下：

- `site`：一个 TOML 文件，对应一个站点或系统
- `action`：site 中的一个命名请求动作
- `compiled request`：把默认值、action、动态值、CLI 参数合并后的最终请求
- `execute`：真正发起 HTTP 请求、处理 cookie、执行 `extract_*`/`save`
- `envelope/state`：将结果输出为 body 或 json，并把本地状态落盘

CLI 框架约定：

- 当前命令层使用 `spf13/cobra`
- 根命令负责全局参数和子命令装配
- `commandRequest` 是 CLI 层到 runtime 的稳定边界，Cobra 类型不进入 runtime/config/state 逻辑

## 配置语义

每个 site 对应一个 `<site>.toml` 文件。

最外层常见字段：

- `version`
- `description`
- `base_url`
- `login`
- `timeout`
- `retries`
- `headers`
- `query`
- `cookies`
- `actions`

这里的 `version = 1` 表示配置 schema 版本，不是 CLI 的发布版本号。CLI 发布版本只来自 Git tag，并在构建时通过 `ldflags` 注入到二进制中。

每个 action 可以定义：

- `description`
- `method`
- `path`
- `headers`
- `cookies`
- `query`
- `body`
- `form`
- `expect_status`
- `extract_type`
- `extract_expr`
- `extract_pattern`
- `extract_group`
- `extract_all`
- `params`
- `extracts`
- `save`

运行时还支持一个 CLI 级的 extractor 输入：

- `--extract <json-object>`

语义约定：

- `--param` 只参与请求编译
- `--extract` 只参与 extractor 执行
- jq extractor 通过 `.extract` 读取该输入
- regex extractor 通过 `{{extract.key}}` 模板占位符读取该输入

## 命令语义

### `run`

标准调用形式是：

```bash
httpx run <site> <action>
```

运行时会解析 site、合并 action 配置、解析动态值、执行请求，并按 `--format` 输出结果。

### `login`

`login` 是独立子命令，不是普通 action。

它的语义是：

- 要求 site 配置了 `[login]`
- 只处理简单用户名密码登录
- 默认从 `<secret-dir>/<site>.json` 读取 `username` / `password`
- 额外刷新本地 cookie 状态
- 如果配置了 `save`，把提取出的值写入本地 state
- 登录成功后写入 `last_login`
- 对于 OIDC/SSO 等复杂流程，应返回配置错误并引导调用方改用外部 Python 脚本

### `inspect`

```bash
httpx inspect <site> <action>
```

它只编译请求，不真正发请求；主要用于查看某个 action 最终会生成什么请求。

默认会对敏感值做脱敏；显式加 `--reveal` 才展示真实值。

### Discovery 命令

用于渐进披露的只读命令：

- `httpx sites`
- `httpx site <site>`
- `httpx action <site> <action>`
- `httpx actions <site>`
- `httpx state <site>`

语义约定：

- `sites`：列出可用 site、描述、action 数和是否有 local state
- `site <site>`：列出站点描述、`base_url`、内建 login 摘要和 state 摘要
- `action <site> <action>`：输出以 `httpx run <site> <action>` 为中心的接口说明页，展示 flags、`params` / `extracts` 字段表和示例
- `actions <site>`：列出每个 action 的名称和描述，作为短列表入口
- `state <site>`：只显示 state 摘要，不显示 state 里的具体值

## 站点测试约定

标准测试顺序：

1. 先做 discovery：`site <site>`、`actions <site>`、`action <site> <action>`、`state <site>`
2. 再做编译验证：`inspect <site> <action>`
3. 最后做真实请求验证：`run <site> <action>`

命令语义：

- `site <site>`：检查站点摘要、`base_url`、内建 login 摘要和 state 摘要
- `actions <site>`：检查每个 action 的名称和描述，快速筛选目标 action
- `action <site> <action>`：检查 action 的完整输入契约、可用 flags 和调用示例
- `state <site>`：只检查本地 state 摘要，不暴露敏感值
- `inspect <site> <action>`：用于无副作用验证 action 编译结果
- `run <site> <action>`：用于真实请求验证，可能依赖登录态或站点自身匿名访问策略
- `run <site> <action> --extract '{...}'`：在不改配置的前提下给 extractor 传入运行时过滤条件
- `login <site>`：
  - 若配置了 `[login]`，应执行简单用户名密码登录并刷新 state
  - 若未配置 `[login]`，应返回配置错误
  - 若实际登录流程是 OIDC/SSO，应由外部 Python 脚本处理

维护约定：

- 新增或修改 site 配置后，默认先做 discovery，再做 inspect，最后做 run
- `inspect` 是站点联调前的主验证方式，因为它不发请求且能暴露配置编译问题
- `run` 的测试结果会受真实网络、服务端状态和本地 state/cookie 影响
- 标准仓库文档不写某个具体站点的私有测试流程
- 站点特有说明如果必须保留，应放在本地私有配置注释，不进入仓库通用文档

### `version`

`httpx version` 和 `httpx --version` 直接输出构建信息：

- 版本号
- commit
- build time

`version`、`run`、`inspect`、`login`、`sites`、`site`、`action`、`actions`、`state`、`help` 都是顶层保留字，不能作为 site 名使用。

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
- secret 默认目录为 `$XDG_DATA_HOME/secret/httpx` 或 `~/.local/secret/httpx`
- config 默认目录为 `$XDG_CONFIG_HOME/httpx` 或 `~/.config/httpx`
- 也可以用 `--state <path>` 覆盖默认目录
- 也可以用 `--secret <path>` 覆盖默认目录

运行约定：

- 不建议把 `--state` 指到 `/tmp/...`，因为这类目录常常跟着沙箱或容器生命周期一起销毁
- 推荐优先使用用户级持久目录：`~/.local/state/httpx`
- 如果需要显式路径，推荐 `--state "$HOME/.local/state/httpx"`
- 内建登录 secret 文件推荐放在 `~/.local/secret/httpx/<site>.json`
- `from = "file"` 读取的是任意文件路径；额外静态 secret 也推荐放在 `~/.local/secret/httpx/`
- 不建议把 mutable runtime state 和静态 secret 文件混放
- 在容器里能否持久化，关键取决于 `HOME` 或 `--state` 是否绑定到宿主机目录或持久卷，而不是路径名本身

每个 site 对应一个 state 文件：

- 文件名：`<site>.json`

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
- `last_login`：最近一次执行 `httpx login <site>` 的时间

写入时机：

- 执行请求后，`save` 结果会写入 `state.Values`
- 当前 cookie jar 会快照到 `state.Cookies`
- 如果本次命令是 `login`，还会更新 `state.LastLogin`
- 之后统一把 state 写回 `<site>.json`

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

- `--format text`
- `--format json`

`text`：

- 未配置 `extract_*` 时直接输出响应体原文
- 配置了 `extract_*` 时输出处理后的 body
- 失败时错误信息写到 stderr

`json`：

- 输出结构化 envelope
- 执行类命令包含 site、action、status、headers、body、state 更新字段等信息
- 配置了 `extract_*` 的 action 在 `json` 输出下仍保留完整 envelope，但 `body` 会被处理后的结果替换
- discovery 命令输出 `sites`、`site`、`actions`、`state` 这些结构化摘要

`inspect` 默认也使用结构化 JSON 输出编译后的请求描述。

`text`：

- discovery 命令的人读默认输出
- 适合交互式查看 sites、actions 和 state 摘要

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
- 共享机器或低信任环境下应显式指定安全的 `--state`
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

分发约定：

- 公网根目录：`https://cligrep.com/cli-releases/`
- httpx 固定版本路径：`/cli-releases/httpx/<version>/`
- httpx 稳定入口路径：`/cli-releases/httpx/latest/`
- 服务器落盘目录：`/docker/cli-releases/httpx/<version>/` 与 `/docker/cli-releases/httpx/latest/`
- `latest/` 下只放稳定文件名软链接与 `checksums.txt`

发布时保持以下规则：

- `vX.Y.Z/` 一旦上传后不覆盖、不改名
- 版本目录保留构建脚本生成的真实文件名
- `latest/` 是唯一允许更新的入口
- `latest/checksums.txt` 必须校验 `latest/` 目录下的稳定文件名，而不是版本目录原始文件名
- 根目录不直接放散落 bundle，所有文件都必须归属到某个 CLI 子目录

## 仓库约定

关键目录职责：

- `cmd/httpx`：CLI 主入口
- `internal/app`：Cobra 根命令/子命令、退出码语义、配置解析、请求编译、执行、state 管理
- `internal/buildinfo`：承接构建注入的版本元信息
- `third_party/cobra`：仓库内固定的 Cobra 依赖副本
- `scripts/release`：本地发布打包脚本
- `examples`：配置示例

文档职责：

- `README.md`：用户手册
- `CLAUDE.md`：设计与维护手册
