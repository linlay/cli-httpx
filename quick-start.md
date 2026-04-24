# Quick Start

```sh
# 1. 安装
go install github.com/linlay/cli-httpx/cmd/httpx@latest
go install ./cmd/httpx
```

```sh
# 2. 确认使用的是新版本
which httpx
```

```sh
# 3. 准备 secret JSON
mkdir -p ~/.local/secret/httpx
cat > ~/.local/secret/httpx/jira.xxxqh.net.json <<'JSON'
{
  "cookie": "JSESSIONID=xxx; atlassian.xsrf.token=yyy"
}
JSON
```

```toml
# 4. 配置 TOML
# 文件路径：
# /Users/joe/xxx/linlay/zenmind-env/skills-market/jira/httpx/jira.xxxqh.net.toml

version = 1
description = "Jira"
base_url = "https://jira.xxxqh.net"
timeout = "20s"
retries = 1

[headers]
Accept = "application/json"
User-Agent = "httpx/1.0"
Cookie = { from = "env", key = "cookie" }

[actions.get_worklogs]
description = "获取 Jira issue 的工时列表"
path = { from = "param", key = "path" }
expect_status = 200
query = { startAt = { from = "param", key = "start_at", default = 0 }, maxResults = { from = "param", key = "max_results", default = 20 } }
params = [
  { name = "path", type = "string", required = true, description = "完整 API path", example = "/rest/api/2/issue/QIUER-5185/worklog" },
  { name = "start_at", type = "number", required = false, description = "分页起始偏移", example = 0 },
  { name = "max_results", type = "number", required = false, description = "单次返回条数", example = 20 }
]
```

```sh
# 5. 加载 secret 和 config 到当前 shell secret会从默认路径加载，config从指定目录加载
eval $(httpx load jira.xxxqh.net \
  --config /Users/joe/xxx/linlay/zenmind-env/skills-market/jira/httpx)
```

```sh
# 6. 验证环境变量
env | grep jira_xxxqh_net
```

```sh
# 7. 手动导出 cookie 到环境变量 前缀是site名拼接 site_cookie
export jira_xxxqh_net_cookie='JSESSIONID=xxx; atlassian.xsrf.token=yyy'
```

```sh
# 8. 手动导出 config 到环境变量 前缀是site名拼接 site_config
export jira_xxxqh_net_config='/Users/joe/xxx/linlay/zenmind-env/skills-market/jira/httpx/jira.xxxqh.net.toml'
```

```sh
# 9. 预览请求，不发送
httpx inspect jira.xxxqh.net get_worklogs --reveal \
  --param path=/rest/api/2/issue/QIUER-5185/worklog \
  --param max_results=1
```

```sh
# 10. 真正发送请求
httpx run jira.xxxqh.net get_worklogs \
  --param path=/rest/api/2/issue/QIUER-5185/worklog \
  --param max_results=1
```

```sh
# 11. 不使用 load 的历史写法
httpx --config /Users/joe/xxx/linlay/zenmind-env/skills-market/jira/httpx \
  run jira.xxxqh.net get_worklogs \
  --param path=/rest/api/2/issue/QIUER-5185/worklog \
  --param max_results=1
```

```sh
# 12. 查看站点
httpx sites
```

```sh
# 13. 查看站点 actions
httpx actions jira.xxxqh.net
```

```sh
# 14. 查看 action 参数
httpx action jira.xxxqh.net get_worklogs
```
