# Exporter

> 负责范围：数据导出功能，支持多种格式和过滤策略
> 最后更新：2026-03-18

## 当前状态

支持 4 种导出格式：
- **jsonl**: NDJSON（每行一个 JSON）
- **json**: 标准 JSON 数组
- **md**: Markdown 格式
- **txt**: 纯文本格式

支持 2 种导出模式：
- **单会话**: `WriteSession()` - 导出单个会话
- **按目录**: `WriteByDirFlat()`, `WriteByDirAllMarkdown()` - 聚合导出

## 核心文件

```
internal/exporter/
├── exporter.go       # 导出逻辑 (~740 行)
└── exporter_test.go  # 测试
```

## Filters 结构

```go
type Filters struct {
    IncludeRoles      []string  // 按角色过滤
    IncludeTypes      []string  // 按类型过滤
    TextOnly          bool      // 只要文本消息
    After/Before      time.Time // 时间范围
    MaxMessages       int       // 最大消息数
    ExcludeShellCalls bool      // 排除 shell 工具调用
    ExcludeToolOutputs bool     // 排除所有工具输出
}
```

## 扁平模式 (WriteByDirFlat)

| 模式 | 说明 | 输出 |
|------|------|------|
| `user` | 仅用户消息 | `["text1", "text2"]` |
| `dialog` | 对话格式 | `[{role,text}]` |
| `dialog_with_thinking` | 含思考 | `[{role,text,type}]` |

## 最近重要事项

- 初始版本: 完整的导出功能，支持所有格式和模式

## Gotchas（开发必读）

⚠️ 以下是开发此 feature 时必须注意的事项：

- **排序**: 消息按 `Ts asc` 排序（旧→新），fallback 到 LineNo
- **Session 聚合**: 需要调用 `indexer.SessionView()` 获取完整信息
- **工具提取**: shell 工具命令从 `arguments.command[]` 提取
- **文件名**: 使用 `BuildAttachmentName()` / `BuildDirAttachmentName()` 生成
- **时间过滤**: `After/Before` 基于 `Ts` 字段，零值表示不限制
- **Content-Disposition**: 导出 HTTP 响应需要设置正确的文件名

## 调试入口

**导出格式错误**
1. 检查 format 参数是否在支持列表中
2. 验证 filter 逻辑是否正确过滤了消息

**工具输出格式**
1. 检查 `parseFuncCall()` 和 `parseFuncOutput()`
2. 验证 arguments 是 string 还是 map[string]any

## 索引

- 设计决策：`decisions/`
- 变更历史：`changelog/`
- 相关文档：`docs/`
