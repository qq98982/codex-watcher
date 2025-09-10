package main

import (
    "context"
    "encoding/json"
    "errors"
    "flag"
    "log"
    "net/http"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "strconv"
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
    // Subcommand routing: start|stop|restart|browse|serve (internal) or default serve
    if len(os.Args) > 1 {
        switch os.Args[1] {
        case "start":
            cfg, err := resolveConfig()
            if err != nil { log.Fatal(err) }
            if err := cmdStart(cfg); err != nil { log.Fatal(err) }
            return
        case "stop":
            cfg, err := resolveConfig()
            if err != nil { log.Fatal(err) }
            if err := cmdStop(cfg); err != nil { log.Fatal(err) }
            return
        case "restart":
            cfg, err := resolveConfig()
            if err != nil { log.Fatal(err) }
            if err := cmdRestart(cfg); err != nil { log.Fatal(err) }
            return
        case "browse":
            cfg, err := resolveConfig()
            if err != nil { log.Fatal(err) }
            if err := cmdBrowse(cfg); err != nil { log.Fatal(err) }
            return
        case "serve":
            // fallthrough to run server normally (internal)
            os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
        }
    }

    cfg, err := resolveConfig()
    if err != nil {
        log.Fatal(err)
    }
    runServer(cfg)
}

func runServer(cfg config) {
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

    // write pid file
    _ = writePIDFile(cfg, os.Getpid())

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
    _ = removePIDFile(cfg)
    wg.Wait()
}

func pidFilePath(cfg config) string {
    return filepath.Join(cfg.CodexDir, "codex-watcher.pid")
}

func writePIDFile(cfg config, pid int) error {
    // ensure dir exists
    _ = os.MkdirAll(cfg.CodexDir, 0o755)
    return os.WriteFile(pidFilePath(cfg), []byte(strconv.Itoa(pid)), 0o644)
}

func readPIDFile(cfg config) (int, error) {
    b, err := os.ReadFile(pidFilePath(cfg))
    if err != nil { return 0, err }
    s := strings.TrimSpace(string(b))
    n, err := strconv.Atoi(s)
    if err != nil { return 0, err }
    return n, nil
}

func removePIDFile(cfg config) error {
    _ = os.Remove(pidFilePath(cfg))
    return nil
}

func isAlive(pid int) bool {
    if pid <= 0 { return false }
    // On Unix, signal 0 checks existence
    err := syscall.Kill(pid, 0)
    return err == nil || err == syscall.EPERM
}

func cmdStart(cfg config) error {
    // if pid exists and alive, refuse
    if pid, err := readPIDFile(cfg); err == nil && isAlive(pid) {
        log.Printf("already running (pid %d)", pid)
        return nil
    }
    exe, err := os.Executable()
    if err != nil { return err }
    // re-exec self with 'serve' subcommand
    args := []string{"serve"}
    if cfg.Port != "" { args = append(args, "--port", cfg.Port) }
    if cfg.CodexDir != "" { args = append(args, "--codex", cfg.CodexDir) }
    cmd := exec.Command(exe, args...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    // detach from parent session/process group
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    if err := cmd.Start(); err != nil { return err }
    // write child pid
    _ = writePIDFile(cfg, cmd.Process.Pid)
    log.Printf("started pid %d on http://localhost:%s", cmd.Process.Pid, cfg.Port)
    return nil
}

func cmdStop(cfg config) error {
    pid, err := readPIDFile(cfg)
    if err != nil {
        return errors.New("not running (no pid file)")
    }
    if !isAlive(pid) {
        _ = removePIDFile(cfg)
        return errors.New("not running")
    }
    // send SIGTERM
    if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
        return err
    }
    // wait up to 5s
    deadline := time.Now().Add(5 * time.Second)
    for time.Now().Before(deadline) {
        if !isAlive(pid) { _ = removePIDFile(cfg); return nil }
        time.Sleep(100 * time.Millisecond)
    }
    return errors.New("stop timeout; process still alive")
}

func cmdRestart(cfg config) error {
    _ = cmdStop(cfg)
    return cmdStart(cfg)
}

func cmdBrowse(cfg config) error {
    url := "http://localhost:" + cfg.Port
    // macOS 'open', Linux 'xdg-open'
    if p, _ := exec.LookPath("open"); p != "" {
        return exec.Command(p, url).Start()
    }
    if p, _ := exec.LookPath("xdg-open"); p != "" {
        return exec.Command(p, url).Start()
    }
    log.Printf("Open %s in your browser", url)
    return nil
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
