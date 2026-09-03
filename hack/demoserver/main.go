// Command demoserver is a throwaway SSH server for recording the demo, so the
// embedded panes in demo.gif show real shells rather than connection errors.
// It accepts any key and gives every session a shell on this machine, which is
// why it only ever listens on the loopback address.
package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"os/exec"

	cpty "github.com/creack/pty"
	gssh "github.com/gliderlabs/ssh"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:42222", "loopback address to listen on")
	hostKey := flag.String("hostkey", "", "path to a host private key")
	flag.Parse()

	srv := &gssh.Server{
		Addr:             *addr,
		PublicKeyHandler: func(gssh.Context, gssh.PublicKey) bool { return true },
		Handler: func(s gssh.Session) {
			ptyReq, winCh, isPty := s.Pty()
			if isPty {
				cmd := exec.Command("sh", "-i")
				cmd.Env = append(cmd.Environ(), "TERM="+ptyReq.Term, "PS1=$ ")
				f, err := cpty.Start(cmd)
				if err != nil {
					s.Exit(1)
					return
				}
				defer f.Close()
				go func() {
					for w := range winCh {
						cpty.Setsize(f, &cpty.Winsize{Rows: uint16(w.Height), Cols: uint16(w.Width)})
					}
				}()
				go func() { io.Copy(f, s) }()
				io.Copy(s, f)
				cmd.Wait()
				s.Exit(0)
				return
			}
			cmd := exec.Command("sh", "-c", s.RawCommand())
			cmd.Stdout, cmd.Stderr = s, s.Stderr()
			err := cmd.Run()
			code := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else if err != nil {
				code = 127
			}
			s.Exit(code)
		},
	}
	if err := gssh.HostKeyFile(*hostKey)(srv); err != nil {
		log.Fatal(err)
	}
	log.Fatal(srv.ListenAndServe())
}
