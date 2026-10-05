package runner

import (
	"io"
	"os"
	"time"
)

// followPoll はファイルの伸びを見に行く間隔。
const followPoll = 50 * time.Millisecond

// followDrain はステップの終了後、追随が最後まで読み切るのを待つ上限。表示先が
// 詰まっていても、これを過ぎたら待たずに進む（欠けるのは画面だけで、ログは完全）。
const followDrain = time.Second

// follower は step_interactive のステップの出力ファイルを追い、表示先へ流す。
//
// ステップの出力は表示先ではなくファイルに直接つないでいる。io.MultiWriter で
// 表示先にも同時に書くと、表示先が詰まる・閉じるとステップの書き込みが止まる。
// ここでは表示先への書き込みが失敗したら表示だけをやめ、ログの保存とステップの
// 進行には影響させない。
type follower struct {
	quit chan struct{}
	done chan struct{}
}

func startFollower(path string, dst io.Writer) *follower {
	f := &follower{quit: make(chan struct{}), done: make(chan struct{})}
	go f.loop(path, dst)
	return f
}

func (f *follower) loop(path string, dst io.Writer) {
	defer close(f.done)
	var src *os.File
	// ステップが起動する前にファイルを開けるとは限らないので、開けるまで待つ
	for src == nil {
		s, err := os.Open(path)
		if err == nil {
			src = s
			break
		}
		select {
		case <-f.quit:
			return
		case <-time.After(followPoll):
		}
	}
	defer src.Close()

	buf := make([]byte, 32*1024)
	broken := false
	stopping := false
	for {
		n, err := src.Read(buf)
		if n > 0 && !broken {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				broken = true
			}
		}
		if err == io.EOF || n == 0 {
			if stopping {
				return
			}
			select {
			case <-f.quit:
				// 終了の合図の後にもう 1 周だけ読み、最後に書かれた分を拾う
				stopping = true
			case <-time.After(followPoll):
			}
			continue
		}
		if err != nil {
			return
		}
	}
}

// stop は追随を止める。表示先への書き込みで詰まっていても followDrain で諦める。
func (f *follower) stop() {
	if f == nil {
		return
	}
	close(f.quit)
	select {
	case <-f.done:
	case <-time.After(followDrain):
	}
}
