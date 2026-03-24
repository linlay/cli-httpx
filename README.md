# httpx

`httpx` 是一个面向智能体和自动化脚本的 HTTP CLI。

它不是 `curl` 的完全替代，而是把“登录、带状态访问接口、从返回结果提取字段”这些常见动作固化到配置里，让调用方只需要执行：

```bash
httpx run <profile> <action>
```

默认输出响应体，也就是默认等价于 `--format body`。

如果一个 action 需要运行时传参，也可以在命令行上追加：

```bash
httpx --param key=value run <profile> <action>
```

## 适合什么场景

- 先登录，再调用多个接口
- 请求头、查询参数、请求体里有密钥或动态值
- 希望把接口调用写成稳定的“动作”
- 希望默认输出结构化 JSON，方便脚本或智能体消费

## 安装

在当前仓库里构建：

```bash
go build .
```

安装到本机：

```bash
go install github.com/zengnianmei/httpx@latest
```

## 三个核心命令

```bash
httpx login <profile>
httpx run <profile> <action>
httpx inspect <profile> <action>
```

- `login`：执行配置里的登录动作，保存 cookie 和提取出的状态值
- `run`：执行一个接口动作
- `inspect`：只展示最终请求，不真正发请求，默认会脱敏

全局参数可以放在命令前，也可以放在命令后。例如下面两种写法都可以：

```bash
httpx --config ./config.toml --format json run demo me
httpx run --format json --config ./config.toml demo me
```

## 最快上手

1. 准备配置文件：

```toml
version = 1

[profiles.demo]
base_url = "https://api.example.com"
login_action = "login"

[profiles.demo.actions.login]
method = "POST"
path = "/session"
form = { username = { from = "env", key = "DEMO_USER" }, password = { from = "env", key = "DEMO_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }

[profiles.demo.actions.me]
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
httpx --config ./config.toml login demo
```

4. 调用接口：

```bash
httpx --config ./config.toml run demo me
```

5. 如果 action 需要运行时参数：

```bash
httpx --config ./config.toml --param user_id=100 run demo user
```

## 配置结构

最外层：

```toml
version = 1
```

每个网站或系统对应一个 `profile`：

```toml
[profiles.demo]
base_url = "https://api.example.com"
login_action = "login"
timeout = "10s"
retries = 1
```

常用字段：

- `base_url`：接口基础地址
- `login_action`：`httpx login <profile>` 时执行哪个 action
- `timeout`：默认超时，例如 `10s`
- `retries`：默认重试次数

你可以给整个 profile 设置默认请求头和默认查询参数：

```toml
[profiles.demo.headers]
Accept = "application/json"

[profiles.demo.query]
locale = "zh-CN"
```

每个 action 表示一个可执行请求：

```toml
[profiles.demo.actions.me]
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

```toml
form = { data = { UserId = "alice", Password = "secret" } }
```

上面会发成：

```text
data={"UserId":"alice","Password":"secret"}
```

## 动态取值

`httpx` 支持 4 种动态值来源。

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

如果某个站点除了服务端下发的 cookie 之外，还要求你主动补一个 cookie，也可以显式写在 action 里：

```toml
cookies = { "oem.sessionid" = { from = "state", key = "oem.sessionid" } }
```

这常见于“登录响应体里给了一个 session id，但后续接口要求它出现在 cookie 里”的系统。

### 5. 读取命令行参数

```toml
query = { q = { from = "param", key = "keyword" } }
```

执行时传入：

```bash
httpx --config ./config.toml --param keyword=httpx run demo search
```

也可以给参数配置默认值：

```toml
query = { page = { from = "param", key = "page", default = 1 } }
```

### 6. 显式字面量

大多数情况下，直接写普通值就已经是字面量：

```toml
query = { locale = "zh-CN" }
```

如果你想在统一的 `from = ...` 写法里显式标明，也可以这样写：

```toml
headers = { X-Mode = { from = "literal", value = "agent" } }
```

## 登录和状态复用

`login` 本质上也是执行一个普通 action，只是额外做两件事：

- 自动持久化 cookie
- 把 `save` 提取出的字段写入本地状态文件

例如：

```toml
[profiles.demo.actions.login]
method = "POST"
path = "/session"
form = { username = { from = "env", key = "DEMO_USER" }, password = { from = "env", key = "DEMO_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }
```

这里的含义是：

- 请求 `/session` 登录
- 从响应 JSON 的 `.body.token` 里拿到 token
- 拼成 `Bearer xxx`
- 保存到本地状态键 `auth.authorization`

后续 action 就可以直接复用：

```toml
headers = { Authorization = { from = "state", key = "auth.authorization" } }
```

## 输出格式

默认输出响应体：

```bash
httpx --config ./config.toml run demo me
```

如果你想拿结构化 JSON，可以显式指定：

```bash
httpx --config ./config.toml --format json run demo me
```

示例 JSON 输出：

```json
{
  "ok": true,
  "profile": "demo",
  "action": "me",
  "status": 200,
  "duration_ms": 123,
  "headers": {
    "Content-Type": [
      "application/json"
    ]
  },
  "body": {
    "id": 1,
    "name": "demo"
  },
  "extract": {
    "id": 1,
    "name": "demo"
  }
}
```

如果你只想拿原始响应体：

```bash
httpx --config ./config.toml run demo me
```

## inspect 用法

先检查最终请求长什么样：

```bash
httpx --config ./config.toml inspect demo me
```

默认会把敏感值替换成 `***`。如果你明确需要看真实值：

```bash
httpx --config ./config.toml --reveal inspect demo me
```

## 全局参数

- `--config <path>`：配置文件路径
- `--format json|body`：输出格式，默认 `body`
- `--param key=value`：给 action 传入运行时参数，可重复传入多次
- `--timeout <duration>`：覆盖配置中的超时
- `--state-dir <path>`：状态文件目录
- `--reveal`：仅 `inspect` 有意义，显示真实敏感值

默认路径：

- 配置文件：`$XDG_CONFIG_HOME/httpx/config.toml` 或 `~/.config/httpx/config.toml`
- 状态目录：`$XDG_STATE_HOME/httpx` 或 `~/.local/state/httpx`

## 常见用法

登录：

```bash
httpx --config ./config.toml login demo
```

调用接口并提取字段：

```toml
[profiles.demo.actions.user-id]
path = "/me"
extract = ".body.id"
```

```bash
httpx --config ./config.toml run demo user-id
```

用运行时参数调用：

```toml
[profiles.demo.actions.user]
path = "/users"
query = { id = { from = "param", key = "user_id" } }
extract = ".body"
```

```bash
httpx --config ./config.toml --param user_id=100 run demo user
```

要求返回 201：

```toml
[profiles.demo.actions.create]
method = "POST"
path = "/items"
body = { name = "demo" }
expect_status = 201
```

接受多个状态码：

```toml
expect_status = [200, 201, 204]
```

## 常见问题

### 1. 为什么提示配置错误

`httpx` 会严格校验配置，未知字段会直接报错。这样做是为了避免拼写错误悄悄失效。

### 2. `body` 和 `form` 有什么区别

- `body`：通常用于 JSON 请求
- `form`：用于 `application/x-www-form-urlencoded`

一个 action 里只能使用其中一种。

### 3. `extract` 和 `save` 的表达式是基于什么

它们使用 `jq` 风格表达式，输入上下文包含：

- `.status`
- `.headers`
- `.headers_lower`
- `.body`
- `.body_text`

最常见的是从 JSON 响应里取值，例如：

```toml
extract = ".body.items[0].id"
save = { "auth.token" = ".body.token" }
```

## 示例文件

完整示例见 [examples/config.toml](/Users/zengnianmei/workspace/source/httpx/examples/config.toml)。
