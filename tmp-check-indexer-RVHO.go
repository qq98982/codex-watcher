package main

import (
  "fmt"
  "time"

  "codex-watcher/internal/indexer"
)

func main() {
  idx := indexer.New("/home/henry/.codex", "/home/henry/.claude/projects")
  if err := idx.Reindex(); err != nil {
    panic(err)
  }
  msgs := idx.Messages("019d4d7b-5afe-7b71-8764-d3356876655c", 0)
  fmt.Println("len", len(msgs))
  if len(msgs) > 0 {
    fmt.Println("first", msgs[0].Ts.Format(time.RFC3339Nano), msgs[0].Type, msgs[0].Role)
    fmt.Println("last", msgs[len(msgs)-1].Ts.Format(time.RFC3339Nano), msgs[len(msgs)-1].Type, msgs[len(msgs)-1].Role)
  }
  vis := indexer.VisibleMessages(msgs, 0)
  fmt.Println("visible", len(vis))
  if len(vis) > 0 {
    fmt.Println("visible-first", vis[0].Ts.Format(time.RFC3339Nano), vis[0].Type, vis[0].Role)
  }
}
