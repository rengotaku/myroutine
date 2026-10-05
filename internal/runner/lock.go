package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// 多重起動ロック。zsh 版と同じく mkdir lock.d の原子性で排他し、中の pid ファイルで
// 残留（持ち主が死んでいる）かを判定する。zsh 版と共存する間、新旧が互いを排他
// できるよう、ディレクトリ名と pid ファイルの形は変えない。取れなければ中止する。
//
// 残留の回収は複数のプロセスが同時に行いうる。単純に「pid が空なら残留」「残留なら
// rename」とすると、次の取り違えで二重実行になる。
//   - 他プロセスが mkdir した直後で pid をまだ書いていないロックを、残留と見なして消す
//   - 片方が回収して取り直した新しいロックを、もう片方が古い判定のまま消す
//
// そのため、pid が無いロックは作成直後とみなして待ち、回収は lock.d.reclaim を
// mkdir で取った 1 プロセスだけが、pid を読み直して判定が変わっていないことを
// 確かめてから行う。
const (
	lockAttempts = 5
	// pidWait は pid ファイルが現れるのを待つ回数と間隔。
	pidWaitTries    = 5
	pidWaitInterval = 100 * time.Millisecond
	// lockFreshAge より新しい pid 無しのロックは、作成途中とみなして回収しない。
	lockFreshAge = 3 * time.Second
	// reclaimStaleAge より古い lock.d.reclaim は、回収中に落ちた残骸とみなして消す。
	reclaimStaleAge = 10 * time.Second
	lockRetryWait   = 50 * time.Millisecond
)

func readLockPid(lock string) string {
	b, err := os.ReadFile(filepath.Join(lock, "pid"))
	if err != nil {
		return ""
	}
	return trimSpace(string(b))
}

// waitLockPid は pid ファイルが空・無いときに少し待って読み直す。
func waitLockPid(lock string) string {
	for i := 0; i < pidWaitTries; i++ {
		if pid := readLockPid(lock); pid != "" {
			return pid
		}
		time.Sleep(pidWaitInterval)
	}
	return readLockPid(lock)
}

func olderThan(path string, age time.Duration) bool {
	st, err := os.Stat(path)
	return err == nil && time.Since(st.ModTime()) >= age
}

// writeLockPid は pid を一時ファイルに書いてから rename する（読み手が書きかけの
// pid を見ないように）。
func writeLockPid(lock string) error {
	tmp := filepath.Join(lock, "pid.tmp."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(lock, "pid"))
}

// acquireLockDir はロックを取り、取れたらそのパスを返す。
func acquireLockDir(lock string, d *diag) (string, bool) {
	for attempt := 0; attempt < lockAttempts; attempt++ {
		if err := os.Mkdir(lock, 0o755); err == nil {
			if err := writeLockPid(lock); err != nil {
				_ = os.RemoveAll(lock)
				d.line("ERROR: ロックに pid を書き込めません: " + err.Error())
				return "", false
			}
			return lock, true
		}
		other := waitLockPid(lock)
		if other != "" && pidAlive(other) {
			d.line(fmt.Sprintf("ERROR: myroutine は既に実行中です (pid=%s)", other))
			return "", false
		}
		if other == "" {
			if _, err := os.Stat(lock); err != nil {
				continue // 待っている間に消えた。取り直す
			}
			if !olderThan(lock, lockFreshAge) {
				// 作成直後で pid がまだ書かれていない。回収しない（安全側に中止）。
				d.line("ERROR: myroutine は既に実行中です (pid=unknown)")
				return "", false
			}
		}
		if !reclaimLock(lock, other, d) {
			time.Sleep(lockRetryWait)
		}
	}
	d.line("ERROR: ロックを取得できません: " + lock)
	return "", false
}

// reclaimLock は残留ロックを回収する。回収の権利（lock.d.reclaim）を取った 1 プロセス
// だけが、pid を読み直して「同じ残留 pid のまま・生存していない」ことを確かめてから
// rename → 削除する。確かめられなければ回収しない。
func reclaimLock(lock, stalePid string, d *diag) bool {
	rec := lock + ".reclaim"
	if err := os.Mkdir(rec, 0o755); err != nil {
		if !olderThan(rec, reclaimStaleAge) {
			return false
		}
		_ = os.Remove(rec)
		if err := os.Mkdir(rec, 0o755); err != nil {
			return false
		}
	}
	defer os.Remove(rec)

	cur := readLockPid(lock)
	if cur != stalePid {
		return false
	}
	if cur != "" && pidAlive(cur) {
		return false
	}
	if cur == "" && !olderThan(lock, lockFreshAge) {
		return false
	}
	shown := stalePid
	if shown == "" {
		shown = "unknown"
	}
	d.line(fmt.Sprintf("警告: 残留ロックを回収します (pid=%s)", shown))
	tomb := fmt.Sprintf("%s.stale.%d", lock, os.Getpid())
	if err := os.Rename(lock, tomb); err != nil {
		return false
	}
	_ = os.RemoveAll(tomb)
	return true
}

// releaseLockDir は、ロックの pid が自分のときだけ lock.d を消す（他人のロックを消さない）。
func releaseLockDir(lock string) {
	if readLockPid(lock) != strconv.Itoa(os.Getpid()) {
		return
	}
	_ = os.RemoveAll(lock)
}
