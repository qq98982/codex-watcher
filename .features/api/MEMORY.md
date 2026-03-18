# API

> 负责范围：HTTP API 路由和嵌入式 Web UI
> 最后更新：2026-03-18

## 当前状态

单一文件架构 (`routes.go`)：
- **HTML/CSS/JS**: 全部内嵌在 Go 代码中（无独立静态文件）
- **路由**: 标准库 `http.ServeMux`
- **Session 过滤**: `shouldHideSession()` 过滤插件中间会话

## 核心文件

```
internal/api/
├── routes.go         # 所有路由和 HTML 模板 (~450 行)
└── routes_test.go    # API 测试
```

## API 端点

| 端点 | 方法 | 功能 |
|------|------|------|
| `/` | GET | 主页面（Web UI） |
| `/api/sessions` | GET | 会话列表（支持 source/project 过滤） |
| `/api/session/{id}` | GET | 单个会话详情 |
| `/api/search` | GET | 搜索接口 |
| `/api/export/{id}` | GET | 导出会话（支持 format 参数） |
| `/api/delete/session/{id}` | DELETE | 删除会话 |
| `/api/delete/message/{id}` | DELETE | 删除单条消息 |
| `/api/title/{id}` | POST | 更新会话标题 |

## 最近重要事项

- 2026-01-28: 添加 `shouldHideSession()` 过滤 thedotmack 插件中间会话
- 2026-01-28: Session 过滤应用到主 UI、API 和搜索

## Gotchas（开发必读）

⚠️ 以下是开发此 feature 时必须注意的事项：

- **SessionFilter 全局变量**: `search.SessionFilter` 需要在 `AttachRoutes` 中设置
- **过滤逻辑**: 应该同时应用到 UI、/api/sessions 和搜索
- **HTML 模板**: 使用 `template.Must()` + `indexHTML` 常量
- **CORS**: 当前未实现，如需跨域需添加
- **路径参数**: 使用 `{id}` 占位符，手动从路径提取

## 调试入口

**会话未被过滤**
1. 检查 `shouldHideSession()` 逻辑
2. 检查 `search.SessionFilter` 是否已设置
3. 验证 `visibleSessions()` 调用

**导出格式错误**
1. 检查 `format` 参数是否支持
2. 验证 exporter.WriteSession 调用

## 索引

- 设计决策：`decisions/`
- 变更历史：`changelog/`
- 相关文档：`docs/`
