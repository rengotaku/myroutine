package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rengotaku/myroutine/internal/ledger"
	"github.com/rengotaku/myroutine/internal/night"
	"github.com/rengotaku/myroutine/internal/view"
)

// runView は `myroutine view [--no-open]`。履歴を view.html に書き、既定ではブラウザで開く。
// night-done は parseArgs が view の経路へ載せ替えるので、ここで振り分ける。
func (r *Runner) runView(args []string) int {
	if r.opt.NightDone {
		return r.runNightDone(args)
	}
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
	in := view.Input{Entries: entries, Skipped: skipped, Now: r.now(), Host: host}
	in.Night = r.nightInput()
	html, err := view.Render(in)
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

// nightInput は夜間実行の入力を読む。NIGHT_RUN_DIR のディレクトリが無いとき（夜間実行を
// しない機械）は nil を返し、view は今までどおりタブを出さない。
// 結果（latest.json）と状態（night-run status）は独立に読み、片方が駄目でも他方は出す。
func (r *Runner) nightInput() *view.NightInput {
	dir := night.Dir()
	if dir == "" {
		return nil
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	ni := &view.NightInput{}
	if rep, err := night.LoadReport(dir); err != nil {
		ni.ReportErr = err.Error()
	} else {
		ni.Report = &rep
	}
	if st, err := night.RunStatus("", 5*time.Second); err != nil {
		ni.StatusErr = err.Error()
	} else {
		ni.Status = &st
	}
	ni.Acks, _ = night.LoadAcks(night.AckPath(r.cfg.LogDir))
	return ni
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
	// CreateTemp の 0600 のまま置く（要対応の本文を他のユーザーに読ませない）
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
