package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// view.html は 0600 のまま置く（chmod で広げない）。
func TestWriteAtomicKeepsPrivatePerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "view.html")
	if err := writeAtomic(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("perm = %o, want no group/other bits", perm)
	}
}
