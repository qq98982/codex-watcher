# Search

> 负责范围：Google 风格查询解析器和内存搜索引擎
> 最后更新：2026-03-18

## 当前状态

零依赖内存搜索，支持：
- **查询语法**: AND/OR、短语、排除、字段过滤、正则、通配符
- **Scope**: content-only / tools-only / all
- **超时保护**: 默认 350ms 软预算
- **结果限制**: 默认最多 200 条

## 核心文件

```
internal/search/
├── search.go         # 搜索引擎实现 (~810 行)
└── search_test.go    # 测试
```

## 查询语法

| 语法 | 说明 | 示例 |
|------|------|------|
| `term` | 子串匹配 | `foo` |
| `"phrase"` | 短语精确匹配 | `"hello world"` |
| `-term` | 排除 | `-error` |
| `OR` | 或逻辑 | `foo OR bar` |
| `/regex/` | 正则表达式 | `/foo.*bar/i` |
| `prefix*` | 前缀匹配（仅末尾） | `test*` |
| `role:assistant` | 字段过滤 | `role:user` |
| `in:tools` | Scope 切换 | `foo in:tools` |

## 数据结构

- **Query**: DNF 结构（OR of ANDs），Groups + Scope
- **Clause**: 单个条件，支持 Negative/Field/Kind/Regex
- **Result**: 匹配结果，带字段预览

## 最近重要事项

- 2026-01-28: 添加 `SessionFilter` 支持，由 API 层设置
- 初始版本: 完整的 Google 风格查询语法

## Gotchas（开发必读）

⚠️ 以下是开发此 feature 时必须注意的事项：

- **SessionFilter**: 全局变量，必须在 `AttachRoutes` 中设置
- **字段过滤器**: 所有 groups 的字段条件必须同时满足（AND 逻辑）
- **Scope 处理**: `in:tools` 会从 tokens 中移除，不影响其他解析
- **正则安全**: 使用 `safeCompile()`，失败时返回 nil
- **PCRE to RE2**: 自动转换 `\s` `\d` `\w` `\S` `\D` `\W`
- **时间预算**: 350ms 后设置 `truncated=true`，继续计算 total
- **通配符**: 仅支持 `foo*` 格式（前缀），多 `*` 转为正则

## 调试入口

**搜索结果为空**
1. 检查 SessionFilter 是否过滤了所有会话
2. 验证查询解析结果（tokenize + parseToDNF）
3. 检查 Scope 设置是否正确

**正则不匹配**
1. 确认是否使用了 PCRE 语法（自动转换）
2. 检查大小写标志 `/i` 是否生效

## 索引

- 设计决策：`decisions/`
- 变更历史：`changelog/`
- 相关文档：`docs/`
