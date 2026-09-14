package sftpx_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/sftpx"
)

// A transfer over an existing file leaves the destination as either the old
// file or the new one, and never as part of the new one.
//
// That is the whole reason a transfer is written beside its destination and
// moved over it at the end. Writing straight to the destination truncated it
// before a single byte had arrived, so a transfer that then failed left
// neither what was there nor what was wanted — and anything else reading that
// file in the meantime saw whatever had arrived so far.
//
// Watched from another goroutine while a real transfer runs over a real SFTP
// connection, because the guarantee is about what someone else sees at that
// path, which no amount of reading the writer's own code can show.
func TestATransferIsNeverSeenHalfWritten(t *testing.T) {
	sess, dir := connect(t)
	defer sess.Close()

	old := bytes.Repeat([]byte("OLD"), 8)
	neu := bytes.Repeat([]byte("NEW"), 4<<20) // big enough to still be arriving
	src := filepath.Join(dir, "source.bin")
	dst := filepath.Join(dir, "dest.bin")
	if err := os.WriteFile(src, neu, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, old, 0o600); err != nil {
		t.Fatal(err)
	}

	var (
		mu    sync.Mutex
		saw   []string
		looks int
	)
	stop, watching := make(chan struct{}), sync.WaitGroup{}
	watching.Add(1)
	go func() {
		defer watching.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if b, err := os.ReadFile(dst); err == nil {
				mu.Lock()
				looks++
				if !bytes.Equal(b, old) && !bytes.Equal(b, neu) {
					saw = append(saw, strconv.Itoa(len(b))+" bytes, which is neither file")
				}
				mu.Unlock()
			}
			time.Sleep(time.Millisecond)
		}
	}()

	if err := sftpx.Copy(context.Background(), sess, dst, sftpx.Local{}, src, func(int64, int64) {}); err != nil {
		t.Fatal(err)
	}
	close(stop)
	watching.Wait()

	if got, err := os.ReadFile(dst); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(got, neu) {
		t.Fatalf("the destination is not the new file: %d bytes", len(got))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(saw) > 0 {
		t.Errorf("a reader saw a half-written file %d times, e.g. %s", len(saw), saw[0])
	}
	// Otherwise the transfer outran the watcher and this proved nothing.
	if looks < 10 {
		t.Errorf("the destination was only read %d times while the transfer ran", looks)
	}
}
