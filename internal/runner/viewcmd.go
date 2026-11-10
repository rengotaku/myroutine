package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/rengotaku/myroutine/internal/ledger"
	"github.com/rengotaku/myroutine/internal/view"
)

// runView は `myroutine view [--no-open]`。履歴を view.html に書き、既定ではブラウザで開く。
func (r *Runner) runView(args []string) int {
	if len(args) > 0 {
		r.diag.line("Usage: myroutine view [--no-open]")
		return 1
	}
	open := !r.opt.NoOpen
	entries, skipped, err := ledger.Load(ledger.Path(r.cfg.LogDir))
	if err != nil {
		r.diag.line("ERROR: 要対応の履歴を読めません: " + err.Error())
		return 1
	}
	host, _ := os.Hostname()
	html, err := view.Render(view.Input{Entries: entries, Skipped: skipped, Now: r.now(), Host: host})
	if err != nil {
		r.diag.line("ERROR: HTML を作れません: " + err.Error())
		return 1
	}
	path := filepath.Join(r.cfg.LogDir, "view.html")
	if err := writeAtomic(path, html); err != nil {
		r.diag.line("ERROR: " + path + " を書けません: " + err.Error())
		return 1
	}
	fmt.Fprintln(r.stdout, path)
	if !open {
		return 0
	}
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	if err := exec.Command(cmd, path).Run(); err != nil {
		r.diag.line("ERROR: " + cmd + " で開けません: " + err.Error())
		return 1
	}
	return 0
}

// writeAtomic は一時ファイルに書いてから rename する（読む側に書きかけを見せない）。
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".view-*.html")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
