package runner

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// killGrace は TERM から KILL へ切り替えるまでの猶予。
const killGrace = 2 * time.Second

// reapGrace は KILL の後、ステップ本体が回収できるまで待つ上限。これを過ぎたら
// 待たずに進む（runner が終われなくなることだけは防ぐ）。
const reapGrace = 3 * time.Second

// exitCodeOf はシェルと同じ流儀の終了コードを返す（シグナルで落ちたら 128+番号）。
func exitCodeOf(ps *os.ProcessState, err error) int {
	if ps == nil {
		if err != nil {
			return 127
		}
		return 0
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// signaledBy はプロセスが sig で終わったかを返す。
func signaledBy(ps *os.ProcessState, sig syscall.Signal) bool {
	if ps == nil {
		return false
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == sig
}

// procEntry は ps の 1 行。
type procEntry struct{ pid, ppid, pgid int }

// snapshotProcs はその時点の全プロセスの親子関係を取る。
func snapshotProcs() []procEntry {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "pgid=").Output()
	if err != nil {
		return nil
	}
	var list []procEntry
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		pgid, e3 := strconv.Atoi(f[2])
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		list = append(list, procEntry{pid, ppid, pgid})
	}
	return list
}

// descendants は root の子孫の PID を親子関係から全部数え上げる（root 自身は含まない）。
func descendants(procs []procEntry, root int) []int {
	children := map[int][]int{}
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p.pid)
	}
	var out []int
	stack := []int{root}
	seen := map[int]bool{root: true}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range children[cur] {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			stack = append(stack, c)
		}
	}
	return out
}

// killTree はステップを止める。停止の直前に子孫の PID を親子関係から全部列挙し、
// その集合とステップのプロセスグループの両方に TERM → 猶予 → KILL を送る。
//
// 子孫を辿りながら順に kill すると取りこぼす。
//   - 子を先に落とすと、親スクリプトは「子が終わった」と見なして次の行へ進む
//   - 親を先に落とすと、孫が init へ里子に出され親子関係から辿れなくなる
//
// そのため木を全部数え上げてから一括で落とす。グループだけに頼ると、setsid で別の
// グループに移った子（親はまだ生きている）を取りこぼす。親が先に死んで里子に出た
// 別グループの子は、列挙でも拾えない（zsh 版と同じ限界）。
func killTree(pid, pgid int) {
	set := append([]int{pid}, descendants(snapshotProcs(), pid)...)
	send := func(sig syscall.Signal) {
		if pgid > 0 {
			_ = syscall.Kill(-pgid, sig)
		}
		for _, p := range set {
			_ = syscall.Kill(p, sig)
		}
	}
	send(syscall.SIGTERM)
	// 止まっている（SIGTTIN 等）プロセスは TERM を処理できないので起こす
	send(syscall.SIGCONT)

	deadline := time.Now().Add(killGrace)
	for time.Now().Before(deadline) {
		if !anyAlive(pid, pgid, set) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	// TERM を捕捉・無視するプロセスが残ると、打ち切ったはずの実行が終われない
	send(syscall.SIGKILL)
}

func anyAlive(pid, pgid int, set []int) bool {
	if pgid > 0 && syscall.Kill(-pgid, 0) == nil {
		return true
	}
	for _, p := range set {
		if p == pid {
			// 本体は runner の子なので、回収されるまではゾンビとして見える。
			// 回収は待ち側の goroutine が行うので、グループ側の判定に任せる。
			continue
		}
		if syscall.Kill(p, 0) == nil {
			return true
		}
	}
	return false
}

// running は実行中のステップ（pid → pgid）。シグナルや読み手の消失で
// 実行を打ち切るとき、どの経路からでも止められるようにする。
type running struct {
	mu    sync.Mutex
	procs map[int]int
}

func newRunning() *running { return &running{procs: map[int]int{}} }

func (r *running) add(pid, pgid int) {
	r.mu.Lock()
	r.procs[pid] = pgid
	r.mu.Unlock()
}

func (r *running) remove(pid int) {
	r.mu.Lock()
	delete(r.procs, pid)
	r.mu.Unlock()
}

// killAll は実行中の全ステップを並行して止める。
func (r *running) killAll() {
	r.mu.Lock()
	snapshot := make(map[int]int, len(r.procs))
	for k, v := range r.procs {
		snapshot[k] = v
	}
	r.mu.Unlock()
	var wg sync.WaitGroup
	for pid, pgid := range snapshot {
		wg.Add(1)
		go func(pid, pgid int) {
			defer wg.Done()
			killTree(pid, pgid)
		}(pid, pgid)
	}
	wg.Wait()
}
