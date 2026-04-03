package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type stageRunner interface {
	Run(ctx context.Context, title string, fn func() error) error
}

type progressRenderer struct {
	out  io.Writer
	tty  bool
	lock sync.Mutex
}

func newProgressRenderer(out io.Writer) *progressRenderer {
	return &progressRenderer{
		out: out,
		tty: isTerminalWriter(out),
	}
}

func (p *progressRenderer) Run(ctx context.Context, title string, fn func() error) error {
	if p == nil {
		return fn()
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return fn()
	}

	if !p.tty {
		fmt.Fprintf(p.out, "%s ...\n", title)
		err := fn()
		if err != nil {
			fmt.Fprintf(p.out, "failed: %s: %v\n", title, err)
			return err
		}
		fmt.Fprintf(p.out, "done: %s\n", title)
		return nil
	}

	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- fn()
		close(done)
	}()

	frames := []string{"|", "/", "-", "\\"}
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	frame := 0

	for {
		select {
		case <-ctx.Done():
			p.clearLine()
			return ctx.Err()
		case err := <-errCh:
			p.clearLine()
			if err != nil {
				fmt.Fprintf(p.out, "x %s: %v\n", title, err)
				return err
			}
			fmt.Fprintf(p.out, "ok %s\n", title)
			return nil
		case <-ticker.C:
			p.lock.Lock()
			fmt.Fprintf(p.out, "\r[%s] %s", frames[frame%len(frames)], title)
			p.lock.Unlock()
			frame++
		}
	}
}

func (p *progressRenderer) clearLine() {
	p.lock.Lock()
	defer p.lock.Unlock()
	fmt.Fprint(p.out, "\r")
	fmt.Fprint(p.out, strings.Repeat(" ", 80))
	fmt.Fprint(p.out, "\r")
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
