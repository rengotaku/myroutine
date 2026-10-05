package runner

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/rengotaku/myroutine/internal/eventlog"
	"github.com/rengotaku/myroutine/internal/format"
	"github.com/rengotaku/myroutine/internal/render"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[2m"
	cRed    = "\x1b[31m"
	cGreen  = "\x1b[32m"
	cYellow = "\x1b[33m"
	cCyan   = "\x1b[36m"
)

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

type winsize struct{ Row, Col, Xpixel, Ypixel uint16 }

func termWidth(f *os.File) (int, bool) {
	var ws winsize
	if err := ioctl(f.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 {
		return 0, false
	}
	return int(ws.Col), true
}

// renderWidth はレンダラと同じ順（COLUMNS → 端末幅 → 100）で出力幅を決める。
func renderWidth(f *os.File) int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if isTTY(f) {
		if w, ok := termWidth(f); ok {
			return w
		}
	}
	return 100
}

// errWriter は最初の書き込みエラーを覚えておく。stdout の読み手が消えたことを
// 書き込みの失敗として受け取るために使う（SIGPIPE は通知で受けて無視している）。
type errWriter struct {
	w   io.Writer
	mu  sync.Mutex
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}

func (e *errWriter) failed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err != nil
}

// lossyWriter は表示専用の書き込み先。失敗したら以後は書かない（表示先の障害で
// runner やステップを止めない）。
type lossyWriter struct {
	w      io.Writer
	mu     sync.Mutex
	broken bool
}

func (l *lossyWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.broken {
		return 0, io.ErrClosedPipe
	}
	n, err := l.w.Write(p)
	if err != nil {
		l.broken = true
	}
	return n, err
}

// dailyLog は $MYROUTINE_LOG_DIR/YYYY-MM-DD.txt。追記のみで、ANSI は除去して書く。
type dailyLog struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

func openDailyLog(path string) *dailyLog {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return &dailyLog{path: path}
	}
	return &dailyLog{path: path, f: f}
}

func (l *dailyLog) Write(p []byte) (int, error) {
	if l == nil || l.f == nil {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.f.WriteString(stripANSI(string(p)))
	return len(p), nil
}

func (l *dailyLog) close() {
	if l != nil && l.f != nil {
		_ = l.f.Close()
	}
}

// diag は runner 自身の進捗・警告・エラー。stderr に出し、日次ログにも残す
// （stdout は人向けの結果か機械向けの JSON 専用にする）。
type diag struct {
	w     io.Writer
	color bool
	mu    sync.Mutex
	log   *dailyLog
}

func (d *diag) write(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = io.WriteString(d.w, s)
	if d.log != nil {
		_, _ = d.log.Write([]byte(s))
	}
}

func (d *diag) paint(code, s string) string {
	if !d.color || code == "" {
		return s
	}
	return code + s + cReset
}

func (d *diag) line(s string)   { d.write(s + "\n") }
func (d *diag) err(msg string)  { d.line("  " + d.paint(cRed, "✗ "+msg)) }
func (d *diag) warn(msg string) { d.line("  " + d.paint(cYellow+cBold, "▸ "+msg)) }
func (d *diag) info(msg string) { d.line("  " + msg) }
func (d *diag) dim(msg string)  { d.line("  " + d.paint(cDim, msg)) }

// emitter はイベントを各出力先へ配る唯一の出口。呼ぶのはメインの goroutine だけ。
type emitter struct {
	events *os.File // last-events.jsonl（myroutine last が再生する）
	jsonl  io.Writer
	rend   *renderSink
	logSt  *format.State // 日次ログに残すサマリの組み立て用
	log    *dailyLog
}

// renderSink は人が読む出力（レンダラの形）。既定では stdout、--json では stderr。
type renderSink struct {
	w   io.Writer
	st  *format.State
	opt render.Options
}

func newRenderSink(w io.Writer, f *os.File, noColor bool) *renderSink {
	return &renderSink{
		w:   w,
		st:  format.NewState(false),
		opt: render.Options{Width: renderWidth(f), Color: !noColor && isTTY(f)},
	}
}

func (e *emitter) emit(ev string, payload any) error {
	line, err := eventlog.EncodeEvent(ev, payload)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if e.jsonl != nil {
		if _, err := e.jsonl.Write(line); err != nil {
			return err
		}
	}
	if e.events != nil {
		_, _ = e.events.Write(line)
	}
	switch p := payload.(type) {
	case *eventlog.RunEvent:
		e.logSt.Run(*p)
		if e.rend != nil {
			e.rend.st.Run(*p)
		}
	case *eventlog.StartEvent:
		if e.rend != nil {
			render.Render(e.rend.w, []render.Block{e.rend.st.Start(*p)}, e.rend.opt)
		}
	case *eventlog.StepEvent:
		e.logSt.Step(*p)
		if e.rend != nil {
			if b := e.rend.st.Step(*p); len(b.Lines) > 0 {
				render.Render(e.rend.w, []render.Block{b}, e.rend.opt)
			}
		}
	case *eventlog.SummaryEvent:
		if e.log != nil {
			_, _ = e.log.Write([]byte("\n"))
			render.Render(e.log, e.logSt.Summary(*p), render.Options{Width: 100})
		}
		if e.rend != nil {
			render.Render(e.rend.w, e.rend.st.Summary(*p), e.rend.opt)
		}
	}
	return nil
}

// raw は dry-run のステップ出力をそのまま人向けの出力へ流す。
func (e *emitter) raw(text string) {
	if e.rend == nil || text == "" {
		return
	}
	if !e.rend.opt.Color {
		text = stripANSI(text)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	_, _ = io.WriteString(e.rend.w, text)
}
