# httpx

`httpx` 是一个面向智能体和自动化脚本的 HTTP CLI。

它不是 `curl` 的完全替代，而是把“登录、带状态访问接口、从返回结果提取字段”这些常见动作固化到配置里，让调用方只需要执行：

```bash
httpx <profile> <action>
```

默认输出响应体，也就是默认等价于 `--format body`。

如果一个 action 需要运行时传参，也可以在命令行上追加：

```bash
httpx --param key=value <profile> <action>
```

## 安装

在当前仓库里构建：

```bash
go build .
```

安装到本机：

```bash
go install github.com/zengnianmei/httpx@latest
```

## 核心命令

```bash
httpx <profile> login
httpx <profile> me
httpx --inspect <profile> me
```

- `login`：保留动作名，会按配置中的 `login_action` 执行真实登录动作，并持久化 cookie 和状态
- 其他 action：执行配置里的普通请求动作
- `--inspect`：只展示最终请求，不真正发请求，默认会脱敏

全局参数可以放在命令前，也可以放在命令后。例如下面两种写法都可以：

```bash
httpx --config ./examples --format json demo me
httpx demo --format json --config ./examples me
```

## 最快上手

1. 准备站点配置文件 `demo.toml`：

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

2. 设置环境变量：

```bash
export DEMO_USER="your-name"
export DEMO_PASS="your-password"
```

3. 执行登录：

```bash
httpx --config . demo login
```

4. 调用接口：

```bash
httpx --config . demo me
```

5. 如果 action 需要运行时参数：

```bash
httpx --config . --param user_id=100 demo user
```

## 配置结构

每个网站或系统对应一个独立的 TOML 文件，文件名就是 `profile`：

- `demo.toml` 对应 `httpx demo ...`
- `gtht.toml` 对应 `httpx gtht ...`

配置文件最外层结构：

```toml
version = 1
base_url = "https://api.example.com"
login_action = "login"
timeout = "10s"
retries = 1
```

常用字段：

- `base_url`：接口基础地址
- `login_action`：`httpx <profile> login` 时执行哪个 action
- `timeout`：默认超时，例如 `10s`
- `retries`：默认重试次数

你可以给整个站点设置默认请求头和默认查询参数：

```toml
[headers]
Accept = "application/json"

[query]
locale = "zh-CN"
```

每个 action 表示一个可执行请求：

```toml
[actions.me]
method = "GET"
path = "/me"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
extract = ".body"
```

action 常用字段：

- `method`：HTTP 方法，不写时会自动推断
- `path`：请求路径，必填
- `headers`：请求头
- `cookies`：显式请求 cookie
- `query`：查询参数
- `body`：JSON 请求体或纯文本请求体
- `form`：表单请求体，和 `body` 二选一
- `expect_status`：期望状态码，不写时默认接受 `2xx`
- `extract`：用 `jq` 风格表达式提取结果
- `save`：从响应里提取值并保存到本地状态

`form` 里的值默认按普通表单字段发送；如果某个表单值本身是对象或数组，`httpx` 会先把它编码成 JSON 字符串，再作为该字段的值发送。

## 动态取值

`httpx` 支持 5 种动态值来源。

### 1. 读取环境变量

```toml
headers = { Authorization = { from = "env", key = "API_TOKEN" } }
```

### 2. 读取文件

```toml
query = { api_key = { from = "file", path = "~/.secrets/api-key", trim = true } }
```

### 3. 执行 shell 命令

```toml
headers = { Authorization = { from = "shell", cmd = "pass demo/token", trim = true } }
```

### 4. 读取之前保存的状态

```toml
headers = { Authorization = { from = "state", key = "auth.authorization" } }
```

### 5. 读取命令行参数

```toml
query = { q = { from = "param", key = "keyword" } }
```

执行时传入：

```bash
httpx --config ./examples --param keyword=httpx demo search
```

也可以给参数配置默认值：

```toml
query = { page = { from = "param", key = "page", default = 1 } }
```

## 登录和状态复用

`httpx <profile> login` 是一个保留动作名。它本质上执行 `login_action` 指向的普通 action，并额外做两件事：

- 自动持久化 cookie
- 把 `save` 提取出的字段写入本地状态文件

例如：

```toml
login_action = "session_create"

[actions.session_create]
method = "POST"
path = "/session"
form = { username = { from = "env", key = "DEMO_USER" }, password = { from = "env", key = "DEMO_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }
```

后续 action 可以直接复用保存下来的状态：

```toml
[actions.me]
path = "/me"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
```

## 输出格式

默认输出响应体：

```bash
httpx --config ./examples demo me
```

如果你想拿结构化 JSON，可以显式指定：

```bash
httpx --config ./examples --format json demo me
```

## inspect 用法

先检查最终请求长什么样：

```bash
httpx --config ./examples --inspect demo me
```

默认会把敏感值替换成 `***`。如果你明确需要看真实值：

```bash
httpx --config ./examples --reveal --inspect demo me
```

## 全局参数

- `--config <dir>`：配置目录，运行时会读取 `<dir>/<profile>.toml`
- `--format json|body`：输出格式，默认 `body`
- `--param key=value`：给 action 传入运行时参数，可重复传入多次
- `--timeout <duration>`：覆盖配置中的超时
- `--state-dir <path>`：状态文件目录
- `--inspect`：只编译请求，不真正发出
- `--reveal`：仅 `--inspect` 有意义，显示真实敏感值

默认路径：

- 配置目录：`$XDG_CONFIG_HOME/httpx` 或 `~/.config/httpx`
- 配置文件：`<config-dir>/<profile>.toml`
- 状态目录：`$XDG_STATE_HOME/httpx` 或 `~/.local/state/httpx`

## 示例

完整示例见 [examples/config.toml](/Users/zengnianmei/workspace/source/httpx/examples/config.toml) 和 [examples/gtht.toml](/Users/zengnianmei/workspace/source/httpx/examples/gtht.toml)。
