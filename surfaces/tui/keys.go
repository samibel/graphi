package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the TUI's complete, read-only key binding set. Every binding maps to
// navigation, selection, or a read-only engine operation (query/search/analyze).
// NONE maps to a mutating operation (no edit/refactor/undo/comment) — the
// read-only guarantee is structural at the key-map level (AC-6), and a test
// enumerates these bindings to assert that.
type keyMap struct {
	NextPane key.Binding
	PrevPane key.Binding
	Up       key.Binding
	Down     key.Binding
	Select   key.Binding
	Search   key.Binding
	Help     key.Binding
	Retry    key.Binding
	Quit     key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		NextPane: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next pane")),
		PrevPane: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev pane")),
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Select:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run/select")),
		Search:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Retry:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp / FullHelp implement bubbles/help.KeyMap for the footer hints. The
// footer always advertises the read-only nature of the surface.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.NextPane, k.Up, k.Down, k.Select, k.Search, k.Retry, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.NextPane, k.PrevPane, k.Up, k.Down},
		{k.Select, k.Search, k.Retry, k.Help, k.Quit},
	}
}

// mutatingBindings returns the (empty) set of bindings that map to mutating
// engine operations. It is exported-to-test via the package and asserted empty:
// the TUI exposes NO mutation affordance anywhere in the navigation model.
func (k keyMap) mutatingBindings() []key.Binding { return nil }
