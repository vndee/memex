package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
)

// graphConn represents a connection from the focused node to another entity.
type graphConn struct {
	entity   *domain.Entity
	relType  string
	summary  string
	weight   float64
	outgoing bool // true = focus→entity, false = entity→focus
}

type graphHistoryEntry struct {
	entityID string
	name     string
}

// graphView is an interactive graph explorer centered on a focused entity.
type graphView struct {
	store storage.Store
	kbID  string

	// Current focused entity and its connections.
	focus       *domain.Entity
	connections []*graphConn
	cursor      int

	// Which entity IDs came from the original search results (highlighted).
	originIDs map[string]bool

	// Navigation breadcrumb trail.
	history []graphHistoryEntry

	// State.
	loading bool
	errMsg  string
}

// Messages.
type graphFocusedMsg struct {
	entity      *domain.Entity
	connections []*graphConn
	err         error
}

func newGraphView(store storage.Store, kbID string, originIDs map[string]bool) *graphView {
	return &graphView{
		store:     store,
		kbID:      kbID,
		originIDs: originIDs,
		loading:   true,
	}
}

// loadNode fetches the entity and all its valid connections.
func (g *graphView) loadNode(entityID string) tea.Cmd {
	store := g.store
	kbID := g.kbID
	return func() tea.Msg {
		ctx := context.Background()
		entity, err := store.GetEntity(ctx, kbID, entityID)
		if err != nil {
			return graphFocusedMsg{err: fmt.Errorf("load entity: %w", err)}
		}

		rels, err := store.GetRelationsForEntity(ctx, kbID, entityID)
		if err != nil {
			return graphFocusedMsg{entity: entity, err: fmt.Errorf("load relations: %w", err)}
		}

		// Deduplicate: if multiple relations connect to the same entity with
		// the same type, keep only the first.
		seen := make(map[string]bool)
		var conns []*graphConn
		for _, rel := range rels {
			if rel.InvalidAt != nil {
				continue // skip invalidated
			}

			otherID := rel.TargetID
			outgoing := true
			if rel.SourceID != entityID {
				otherID = rel.SourceID
				outgoing = false
			}

			dedupKey := otherID + "|" + rel.Type
			if seen[dedupKey] {
				continue
			}
			seen[dedupKey] = true

			other, err := store.GetEntity(ctx, kbID, otherID)
			if err != nil {
				continue
			}

			conns = append(conns, &graphConn{
				entity:   other,
				relType:  rel.Type,
				summary:  rel.Summary,
				weight:   rel.Weight,
				outgoing: outgoing,
			})
		}

		return graphFocusedMsg{entity: entity, connections: conns}
	}
}

// handleFocused processes the result of loading a node.
func (g *graphView) handleFocused(msg graphFocusedMsg) {
	g.loading = false
	if msg.err != nil {
		g.errMsg = msg.err.Error()
		return
	}
	g.errMsg = ""
	g.focus = msg.entity
	g.connections = msg.connections
	g.cursor = 0
}

// update handles keyboard input. Returns true if the graph should close.
func (g *graphView) update(key string) (closed bool, cmd tea.Cmd) {
	if g.loading {
		return false, nil
	}

	switch key {
	case "esc", "q":
		return true, nil

	case "j", "down":
		if g.cursor < len(g.connections)-1 {
			g.cursor++
		}

	case "k", "up":
		if g.cursor > 0 {
			g.cursor--
		}

	case "g":
		g.cursor = 0

	case "G":
		if len(g.connections) > 0 {
			g.cursor = len(g.connections) - 1
		}

	case "enter", " ":
		// Expand: navigate into the selected connection's entity.
		if g.cursor >= 0 && g.cursor < len(g.connections) && g.focus != nil {
			conn := g.connections[g.cursor]
			g.history = append(g.history, graphHistoryEntry{
				entityID: g.focus.ID,
				name:     g.focus.Name,
			})
			g.loading = true
			return false, g.loadNode(conn.entity.ID)
		}

	case "backspace", "left", "h":
		// Go back in history.
		if len(g.history) > 0 {
			last := g.history[len(g.history)-1]
			g.history = g.history[:len(g.history)-1]
			g.loading = true
			return false, g.loadNode(last.entityID)
		}
		// No history — close.
		return true, nil
	}

	return false, nil
}

// view renders the graph explorer as a full-screen overlay.
func (g *graphView) view(w, h int) string {
	if g.loading {
		content := titleStyle.Render("Graph Explorer") + "\n\n" +
			mutedStyle.Render("  Loading...")
		return activePaneStyle.Width(w - 4).Height(h - 2).Padding(1, 2).Render(content)
	}

	if g.focus == nil {
		content := titleStyle.Render("Graph Explorer") + "\n\n" +
			errorStyle.Render("  "+g.errMsg)
		return activePaneStyle.Width(w - 4).Height(h - 2).Padding(1, 2).Render(content)
	}

	// Split into left (focus + connections) and right (detail).
	leftW := (w - 8) * 55 / 100
	rightW := (w - 8) - leftW - 3
	bodyH := h - 6

	left := g.renderLeft(leftW, bodyH)
	right := g.renderRight(rightW, bodyH)

	// Left pane border
	leftPane := lipgloss.NewStyle().
		Width(leftW).
		Height(bodyH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Render(left)

	// Right pane border
	rightBorderColor := colorBorder
	if len(g.connections) > 0 {
		rightBorderColor = colorSecondary
	}
	rightPane := lipgloss.NewStyle().
		Width(rightW).
		Height(bodyH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rightBorderColor).
		Render(right)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane)

	// Header
	header := g.renderBreadcrumb(w - 6)

	// Footer
	footer := g.renderFooter()

	content := header + "\n" + panels + "\n" + footer

	return lipgloss.NewStyle().
		Width(w - 2).
		Height(h - 1).
		Padding(1, 1).
		Render(content)
}

func (g *graphView) renderBreadcrumb(w int) string {
	var parts []string
	for _, h := range g.history {
		parts = append(parts, mutedStyle.Render(truncStr(h.name, 18)))
	}
	if g.focus != nil {
		parts = append(parts, labelStyle.Render(g.focus.Name))
	}

	crumb := strings.Join(parts, mutedStyle.Render(" > "))
	prefix := titleStyle.Render("Graph Explorer")
	if len(parts) > 0 {
		prefix += mutedStyle.Render("  ")
	}

	return prefix + crumb
}

func (g *graphView) renderLeft(w, h int) string {
	var b strings.Builder

	// Focused entity header.
	tag := string(nodeTag(g.focus.Type))
	isOrigin := g.originIDs[g.focus.ID]

	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(colorHighlight)
	b.WriteString(" " + tag + " " + nameStyle.Render(g.focus.Name))
	if isOrigin {
		b.WriteString(" " + lipgloss.NewStyle().Foreground(colorWarning).Render("★"))
	}
	b.WriteString("\n")
	b.WriteString(" " + mutedStyle.Render(g.focus.Type) + "\n")

	// Summary (truncated to fit).
	if g.focus.Summary != "" {
		sumLines := wrapText(g.focus.Summary, w-4)
		maxSumLines := 3
		for i, line := range sumLines {
			if i >= maxSumLines {
				b.WriteString("  " + mutedStyle.Render("...") + "\n")
				break
			}
			b.WriteString("  " + mutedStyle.Render(line) + "\n")
		}
	}
	b.WriteString("\n")

	// Connections header.
	connLabel := fmt.Sprintf("─── %d connections ", len(g.connections))
	connLabel += strings.Repeat("─", max(0, w-len(connLabel)-2))
	b.WriteString(" " + labelStyle.Render(connLabel) + "\n\n")

	if len(g.connections) == 0 {
		b.WriteString("  " + mutedStyle.Render("(no connections found)") + "\n")
		return b.String()
	}

	// Connection list.
	// Calculate visible window (scrolling).
	visibleLines := h - 10
	if visibleLines < 3 {
		visibleLines = 3
	}

	startIdx := 0
	if g.cursor >= visibleLines {
		startIdx = g.cursor - visibleLines + 1
	}
	endIdx := startIdx + visibleLines
	if endIdx > len(g.connections) {
		endIdx = len(g.connections)
	}
	// Adjust start if we're near the end.
	if endIdx-startIdx < visibleLines && startIdx > 0 {
		startIdx = max(0, endIdx-visibleLines)
	}

	// Scroll indicator top.
	if startIdx > 0 {
		b.WriteString("  " + mutedStyle.Render(fmt.Sprintf("  ▲ %d more", startIdx)) + "\n")
	}

	for i := startIdx; i < endIdx; i++ {
		conn := g.connections[i]
		isCursor := i == g.cursor

		// Direction arrow and relation type.
		var arrow, relLabel string
		relW := min(18, w/3)
		if conn.outgoing {
			arrow = "──→"
			relLabel = truncStr(conn.relType, relW)
		} else {
			arrow = "←──"
			relLabel = truncStr(conn.relType, relW)
		}

		// Entity name with type tag.
		entTag := string(nodeTag(conn.entity.Type))
		entName := truncStr(conn.entity.Name, w-relW-12)

		// Build the line.
		connInOrigin := g.originIDs[conn.entity.ID]

		var line string
		if isCursor {
			prefix := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(" ▸ ")
			relStyled := lipgloss.NewStyle().Foreground(colorMuted).Render(relLabel + " " + arrow)
			nameStyled := lipgloss.NewStyle().Foreground(colorHighlight).Bold(true).Render(entTag + " " + entName)
			line = prefix + relStyled + " " + nameStyled
		} else {
			prefix := "   "
			relStyled := mutedStyle.Render(relLabel + " " + arrow)
			nameStyled := lipgloss.NewStyle().Foreground(colorSecondary).Render(entTag + " " + entName)
			line = prefix + relStyled + " " + nameStyled
		}

		if connInOrigin {
			line += " " + lipgloss.NewStyle().Foreground(colorWarning).Render("★")
		}

		b.WriteString(line + "\n")
	}

	// Scroll indicator bottom.
	if endIdx < len(g.connections) {
		b.WriteString("  " + mutedStyle.Render(fmt.Sprintf("  ▼ %d more", len(g.connections)-endIdx)) + "\n")
	}

	return b.String()
}

func (g *graphView) renderRight(w, h int) string {
	if len(g.connections) == 0 || g.cursor < 0 || g.cursor >= len(g.connections) {
		return " " + mutedStyle.Render("no connection selected")
	}

	conn := g.connections[g.cursor]
	var b strings.Builder

	// Entity detail.
	tag := string(nodeTag(conn.entity.Type))
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(colorHighlight)
	b.WriteString(" " + tag + " " + nameStyle.Render(conn.entity.Name) + "\n")
	b.WriteString(" " + mutedStyle.Render(conn.entity.Type) + "\n\n")

	// Entity summary.
	if conn.entity.Summary != "" {
		b.WriteString(" " + labelStyle.Render("Summary") + "\n")
		for _, line := range wrapText(conn.entity.Summary, w-4) {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n")
	}

	// Relation info.
	b.WriteString(" " + labelStyle.Render("Relation") + "\n")
	if conn.outgoing {
		b.WriteString("  " + mutedStyle.Render(g.focus.Name+" → "+conn.entity.Name) + "\n")
	} else {
		b.WriteString("  " + mutedStyle.Render(conn.entity.Name+" → "+g.focus.Name) + "\n")
	}
	b.WriteString("  " + labelStyle.Render("type: ") + conn.relType + "\n")
	if conn.weight > 0 {
		b.WriteString("  " + labelStyle.Render("weight: ") + fmt.Sprintf("%.2f", conn.weight) + "\n")
	}
	b.WriteString("\n")

	// Relation summary.
	if conn.summary != "" {
		b.WriteString(" " + labelStyle.Render("Detail") + "\n")
		for _, line := range wrapText(conn.summary, w-4) {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n")
	}

	// Highlight status.
	if g.originIDs[conn.entity.ID] {
		b.WriteString(" " + lipgloss.NewStyle().Foreground(colorWarning).Render("★ In search results") + "\n")
	} else {
		b.WriteString(" " + mutedStyle.Render("○ Expanded via graph") + "\n")
	}

	b.WriteString("\n")
	b.WriteString(" " + mutedStyle.Render("ID: "+conn.entity.ID[:safeLen(conn.entity.ID, 12)]+"...") + "\n")

	return b.String()
}

func (g *graphView) renderFooter() string {
	hints := []keyHint{
		{"j/k", "navigate"},
		{"enter", "expand"},
		{"backspace", "back"},
		{"g/G", "top/bottom"},
		{"esc", "close"},
	}
	return " " + renderKeyHints(hints)
}

// wrapText wraps a string at word boundaries to fit within width.
func wrapText(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}

	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return lines
}

// nodeTag returns a character to represent the entity type.
func nodeTag(typ string) rune {
	switch strings.ToLower(typ) {
	case "person":
		return '●'
	case "project":
		return '◆'
	case "organization", "company":
		return '■'
	case "technology", "tool":
		return '▲'
	case "event":
		return '★'
	case "concept":
		return '◇'
	default:
		return '○'
	}
}
