# Indexer

> 负责范围：核心索引引擎 - 从 Codex/Claude JSONL 文件读取并构建内存索引
> 最后更新：2026-03-18

## 当前状态

双 Provider 架构：
- **Codex**: `~/.codex/sessions/*.jsonl` - rollout 文件名解析，UUID 提取
- **Claude**: `<claudeDir>/<project>/*.jsonl` - 命名空间 `claude:<project>:<sid>`

轮询模式：1.5秒间隔，tail 方式增量读取，支持文件重置处理

## 核心文件

```
internal/indexer/
├── indexer.go      # 主索引逻辑 (~1240 行)
└── indexer_test.go # 测试文件
```

## 关键数据结构

- **Message**: 单条消息，带 Provider/source/lineNo
- **Session**: 会话聚合，包含 CWD/CWDBase/Models/Roles/Tags
- **Indexer**: 主结构，sessions + messages 双 map，positions 记录文件偏移

## 核心流程

1. `scanAll()` - 扫描目录，定位所有 JSONL 文件
2. `tailFile()` - seek 到上次位置，读取新行
3. `ingestLine()` - 解析 JSON，提取字段，更新 session/message
4. `loadSessionMetadata()` - 加载 .meta.json 自定义标题

## 最近重要事项

- 2026-03-18: 上游合并完成，支持 Codex/Claude 双 provider
- 2026-01-28: 实现会话过滤功能（API 层面）

## Gotchas（开发必读）

⚠️ 以下是开发此 feature 时必须注意的事项：

- **Session ID 命名冲突**: Claude session ID 必须加 `claude:<project>:` 前缀避免与 Codex UUID 冲突
- **Rollout 文件名**: `rollout-YYYY-MM-DDTHH-mm-ss-UUID` 格式，需要提取最后 36 字符作为 UUID
- **内存限制**: 每个 session 最多保留 5000 条消息 (`maxMessagesPerSession`)
- **文件截断**: seek 失败时重置 position 为 0，重新读取整个文件
- **标题优先级**: .meta.json > Claude summary > explicit title > first message
- **CWD 提取**: 优先从 raw["cwd"]，其次 environment_context 中的 `<cwd>...</cwd>` 标签
- **Time 零值**: 没有 ts 字段时使用 time.Time{}，需要 IsZero() 检查

## 调试入口

**索引数据不一致**
1. 检查 `positions` map 是否正确记录文件偏移
2. 检查 `lineNos` map 是否正确递增
3. 运行 `Reindex()` 强制重建索引

**Session 标题缺失**
1. 检查 `.meta.json` 文件是否存在
2. 检查 `hasSummary` 和 `hasContent` 标志
3. 查看 fallback 逻辑是否正确

## 索引

- 设计决策：`decisions/`
- 变更历史：`changelog/`
- 相关文档：`docs/`
