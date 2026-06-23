package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/lipgloss"
)

// Styles for the three-pane shell. Kept minimal and dependency-light; lipgloss
// renders the borders/joins. The focused pane gets a brighter border.
var (
	focusedBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63"))
	blurredBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	bannerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
)

// layout sizes the three panes from the current terminal dimensions. The
// navigator takes the left third; the main + provenance panes split the rest.
func (m *Model) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	const chrome = 6 // header + footer + borders budget
	bodyH := m.height - chrome
	if bodyH < 3 {
		bodyH = 3
	}
	navW := m.width / 4
	if navW < 16 {
		navW = 16
	}
	rest := m.width - navW - 8
	if rest < 20 {
		rest = 20
	}
	mainW := rest / 2
	provW := rest - mainW

	m.nav.SetSize(navW, bodyH)
	m.main.Width, m.main.Height = mainW, bodyH
	m.prov.Width, m.prov.Height = provW, bodyH
	m.main.SetContent(m.mainContent)
	m.prov.SetContent(m.provContent)
}

// View renders the full frame: header (engine/schema version + connection dot +
// banner), the three panes (navigator / main / persistent provenance), and the
// read-only footer. View is PURE — it never performs I/O (AC-3 discipline).
func (m *Model) View() string {
	if m.quitting {
		return "bye.\n"
	}
	if !m.ready {
		return "graphi tui — initialising… (read-only surface)\n"
	}

	header := m.renderHeader()

	navBox := m.paneBorder(paneNav).Render(m.nav.View())
	mainBox := m.paneBorder(paneMain).Render(m.main.View())
	provBox := m.paneBorder(paneProv).Render(m.prov.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, navBox, mainBox, provBox)

	h := help.New()
	footer := footerStyle.Render(h.View(m.keys) + " · read-only surface")

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderHeader builds the persistent header: engine/schema version, a connection
// dot, the in-flight spinner + completion marker, and the degraded banner.
func (m *Model) renderHeader() string {
	dot := "●"
	conn := "connected"
	if !m.connected {
		dot, conn = "○", "disconnected"
	}
	status := fmt.Sprintf("graphi TUI  schema v%d  %s %s", m.schemaVersion, dot, conn)
	var right string
	if m.inFlight {
		right = m.sp.View() + " running…"
	} else if m.lastComplete != "" {
		right = m.lastComplete
	}
	line := headerStyle.Render(status)
	if right != "" {
		line += "  " + right
	}
	if m.banner != "" {
		line += "\n" + bannerStyle.Render(m.banner)
	}
	return line
}

// paneBorder returns the focused or blurred border style for a pane.
func (m *Model) paneBorder(p pane) lipgloss.Style {
	if m.focus == p {
		return focusedBorder
	}
	return blurredBorder
}

// String exposes a plain-text snapshot of the current content for tests/headless
// assertions (provenance pane + main pane + banner) without a real terminal.
func (m *Model) String() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n--- main ---\n")
	b.WriteString(m.mainContent)
	b.WriteString("\n--- provenance ---\n")
	b.WriteString(m.provContent)
	if m.banner != "" {
		b.WriteString("\n--- banner ---\n")
		b.WriteString(m.banner)
	}
	return b.String()
}
