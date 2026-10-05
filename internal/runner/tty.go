package runner

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// terminal は制御端末（/dev/tty）。端末が無い環境（cron・CI・パイプの向こう）では
// f が nil のままで、前景の受け渡しも確認の入力も行わない。
type terminal struct {
	f *os.File
}

func openTerminal() *terminal {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return &terminal{}
	}
	return &terminal{f: f}
}

func (t *terminal) ok() bool { return t != nil && t.f != nil }

func (t *terminal) fd() uintptr { return t.f.Fd() }

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// foregroundPgrp は端末の前景プロセスグループ（tcgetpgrp）を返す。
func (t *terminal) foregroundPgrp() (int, error) {
	var pgrp int32
	if err := ioctl(t.fd(), syscall.TIOCGPGRP, unsafe.Pointer(&pgrp)); err != nil {
		return 0, err
	}
	return int(pgrp), nil
}

// runnerIsForeground は runner のプロセスグループが端末の前景にいるかを返す。
// 背景で起動された runner は前景を奪わない。
func (t *terminal) runnerIsForeground() bool {
	if !t.ok() {
		return false
	}
	fg, err := t.foregroundPgrp()
	return err == nil && fg == syscall.Getpgrp()
}

// setForeground は端末の前景を pgid に移す（tcsetpgrp）。runner が背景にいる状態で
// 呼ぶと SIGTTOU が届くため、呼ぶ間だけ無視する。ハンドラを置く形だと
// システムコールが再開されて同じシグナルが届き続けるので、無視でなければならない。
// 無視をそのままにしておくと以後に起動する子へ SIG_IGN が引き継がれるので、すぐ戻す。
func (t *terminal) setForeground(pgid int) error {
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	p := int32(pgid)
	return ioctl(t.fd(), syscall.TIOCSPGRP, unsafe.Pointer(&p))
}

func (t *terminal) getAttr() (*syscall.Termios, error) {
	var a syscall.Termios
	if err := ioctl(t.fd(), ioctlGetTermios, unsafe.Pointer(&a)); err != nil {
		return nil, err
	}
	return &a, nil
}

// setAttr は端末設定を書き戻す（前景を runner に戻した後に呼ぶ）。
func (t *terminal) setAttr(a *syscall.Termios) error {
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	return t.setAttrPlain(a)
}

// setAttrPlain は SIGTTOU を無視せずに端末設定を変える。背景にいる runner が呼ぶと
// zsh 版と同じく SIGTTOU で止まり、前景の別のジョブの端末設定を変えない。
func (t *terminal) setAttrPlain(a *syscall.Termios) error {
	return ioctl(t.fd(), ioctlSetTermios, unsafe.Pointer(a))
}

// readKey は 1 文字だけ読む（zsh の read -k 1 と同じく Enter を待たない）。
// 端末が無い・読めないときは ok=false。ctx が取り消されたら待つのをやめる。
func (t *terminal) readKey(ctx context.Context) (byte, bool) {
	if !t.ok() {
		return 0, false
	}
	orig, err := t.getAttr()
	if err != nil {
		return 0, false
	}
	raw := *orig
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := t.setAttrPlain(&raw); err != nil {
		return 0, false
	}
	defer t.setAttrPlain(orig)

	type res struct {
		b  byte
		ok bool
	}
	ch := make(chan res, 1)
	go func() {
		buf := make([]byte, 1)
		n, err := t.f.Read(buf)
		ch <- res{buf[0], err == nil && n == 1}
	}()
	select {
	case r := <-ch:
		return r.b, r.ok
	case <-ctx.Done():
		return 0, false
	}
}

// handover は逐次ステップに端末の前景を渡した状態を表す。restore はどの終わり方でも
// 必ず呼ぶ（戻し忘れると runner の後に親シェルが端末を読めなくなる）。
type handover struct {
	t     *terminal
	attrs *syscall.Termios
}

// prepareHandover は runner が前景にいるときだけ、受け渡しの準備（端末設定の退避）をする。
// 前景にいなければ nil を返し、ステップは前景を持たずに走る。
func (t *terminal) prepareHandover() *handover {
	if !t.runnerIsForeground() {
		return nil
	}
	attrs, _ := t.getAttr()
	return &handover{t: t, attrs: attrs}
}

// restore は前景を runner のプロセスグループへ戻し、端末設定を渡す前の状態に戻す
// （ステップが raw モードのまま落ちても、親シェルへ壊れた端末を返さない）。
func (h *handover) restore() {
	if h == nil {
		return
	}
	_ = h.t.setForeground(syscall.Getpgrp())
	if h.attrs != nil {
		_ = h.t.setAttr(h.attrs)
	}
}
