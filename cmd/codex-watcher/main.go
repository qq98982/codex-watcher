package main

import (
    "context"
    "encoding/json"
    "errors"
    "flag"
    "log"
    "net/http"
    "os"
    "os/signal"
    "path/filepath"
    "strings"
    "sync"
    "syscall"
    "time"

    "codex-watcher/internal/api"
    "codex-watcher/internal/indexer"
)

type config struct {
    Port     string
    CodexDir string
}

func getenv(key, def string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return def
}

func resolveConfig() (config, error) {
    var (
        portFlag  = flag.String("port", "", "port to listen on")
        dirFlag   = flag.String("codex", "", "path to ~/.codex directory")
        showUsage = flag.Bool("h", false, "show help")
    )
    flag.Parse()
    if *showUsage {
        flag.Usage()
        os.Exit(0)
    }
    cfg := config{
        Port:     getenv("PORT", "7077"),
        CodexDir: getenv("CODEX_DIR", filepath.Join(os.Getenv("HOME"), ".codex")),
    }
    if *portFlag != "" {
        cfg.Port = *portFlag
    }
    if *dirFlag != "" {
        cfg.CodexDir = *dirFlag
    }
    if cfg.CodexDir == "" {
        return cfg, errors.New("could not resolve ~/.codex directory; set CODEX_DIR or --codex")
    }
    return cfg, nil
}

func main() {
    cfg, err := resolveConfig()
    if err != nil {
        log.Fatal(err)
    }

    // Prepare indexer
    idx := indexer.New(cfg.CodexDir)

    // Kick off background polling watcher
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    var wg sync.WaitGroup
    wg.Add(1)
    go func() {
        defer wg.Done()
        idx.Run(ctx.Done())
    }()

    // HTTP server
    mux := http.NewServeMux()
    api.AttachRoutes(mux, idx)

    srv := &http.Server{
        Addr:              ":" + cfg.Port,
        Handler:           withLogging(mux),
        ReadHeaderTimeout: 5 * time.Second,
        IdleTimeout:       60 * time.Second,
    }

    log.Printf("codex-watcher listening on http://localhost:%s (watching %s)\n", cfg.Port, cfg.CodexDir)

    go func() {
        if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
            log.Fatalf("http server error: %v", err)
        }
    }()

    <-ctx.Done()
    log.Println("shutting down...")
    shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel2()
    _ = srv.Shutdown(shutdownCtx)
    wg.Wait()
}

func withLogging(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        lrw := &logResponseWriter{ResponseWriter: w, status: 200}
        next.ServeHTTP(lrw, r)
        dur := time.Since(start)
        log.Printf("%s %s %d %s", r.Method, r.URL.Path, lrw.status, dur.Truncate(time.Millisecond))
    })
}

type logResponseWriter struct {
    http.ResponseWriter
    status int
}

func (lrw *logResponseWriter) WriteHeader(code int) {
    lrw.status = code
    lrw.ResponseWriter.WriteHeader(code)
}

// helper for debug curl
func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    enc := json.NewEncoder(w)
    enc.SetEscapeHTML(false)
    _ = enc.Encode(v)
}

// safe join for templates/static
func joinURL(a, b string) string {
    return strings.TrimRight(a, "/") + "/" + strings.TrimLeft(b, "/")
}
