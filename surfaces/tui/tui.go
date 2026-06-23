package tui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the Bubble Tea program over the given Engine (the loopback HTTP/SSE
// adapter constructed in cmd/graphi). It constructs the Model, binds the context
// so async commands cancel cleanly on teardown, and runs the program against the
// supplied in/out (os.Stdin/os.Stdout in production; tea options let tests drive
// a headless harness). It blocks until the user quits or ctx is cancelled.
//
// The TUI imports no engine/* package: it consumes the Engine purely over the
// SW-044 HTTP/SSE surface (parity by construction), preserving the
// cmd → surfaces → engine → core layering and the local-first posture.
func Run(ctx context.Context, eng Engine, in io.Reader, out io.Writer) error {
	m := New(eng).WithContext(ctx)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	_, err := p.Run()
	if err != nil && err != tea.ErrProgramKilled && ctx.Err() != nil {
		return nil // clean cancellation
	}
	return err
}
