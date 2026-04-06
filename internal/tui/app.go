package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/search"
	"github.com/vndee/memex/internal/storage"
)

// pane tracks which pane has focus.
type pane int

const (
	paneKB pane = iota
	paneContent
	paneInspector
)

// collection tracks which data collection is displayed.
type collection int

const (
	collEntities collection = iota
	collRelations
	collEpisodes
	collSearchResults
)

func (c collection) String() string {
	switch c {
	case collEntities:
		return "Entities"
	case collRelations:
		return "Relations"
	case collEpisodes:
		return "Episodes"
	case collSearchResults:
		return "Search Results"
	default:
		return "Unknown"
	}
}

func (c collection) shortKey() string {
	switch c {
	case collEntities:
		return "e"
	case collRelations:
		return "r"
	case collEpisodes:
		return "p"
	default:
		return ""
	}
}

// model is the top-level bubbletea model.
type model struct {
	store    storage.Store
	searcher *search.Searcher
	sched    *ingestion.Scheduler

	// Layout
	width, height int
	focus         pane

	// KB list (left pane)
	kbs      []*domain.KnowledgeBase
	kbCursor int
	activeKB string

	// Content table (center pane)
	coll       collection
	contentTbl table.Model
	entities   []*domain.Entity
	relations  []*domain.Relation
	episodes   []*domain.Episode
	results    []*domain.SearchResult

	// Entity name cache for relation display
	entityNames map[string]string // id -> name

	// Inspector (right pane)
	inspector viewport.Model

	// Search input
	searchInput textinput.Model
	searching   bool
	lastQuery   string

	// Insert mode
	insertInput textinput.Model
	inserting   bool

	// KB creation wizard
	wizard     *kbWizard
	showWizard bool

	// Confirmation dialog
	confirm *confirmDialog

	// Stats overlay
	showStats bool
	stats     *domain.MemoryStats

	// Status bar
	statusMsg  string
	statusTime time.Time

	// Help overlay
	showHelp bool

	// Graph mode
	showGraph bool
	graphV    *graphView
}

// New creates a new TUI model.
func New(store storage.Store, searcher *search.Searcher, sched *ingestion.Scheduler) model {
	ti := textinput.New()
	ti.Placeholder = "search query..."
	ti.CharLimit = 200

	ii := textinput.New()
	ii.Placeholder = "type memory text, then press enter to store..."
	ii.CharLimit = 4096

	tbl := table.New(
		table.WithColumns(entityColumns(60)),
		table.WithRows(nil),
		table.WithFocused(true),
		table.WithHeight(10),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		Bold(true).
		Foreground(colorSecondary)
	s.Selected = s.Selected.
		Foreground(colorHighlight).
		Background(lipgloss.Color("57")).
		Bold(true)
	tbl.SetStyles(s)

	vp := viewport.New(30, 10)

	return model{
		store:       store,
		searcher:    searcher,
		sched:       sched,
		focus:       paneKB,
		coll:        collEntities,
		contentTbl:  tbl,
		inspector:   vp,
		searchInput: ti,
		insertInput: ii,
		entityNames: make(map[string]string),
	}
}

// --- Messages ---

type kbsLoadedMsg []*domain.KnowledgeBase
type contentLoadedMsg struct {
	entities  []*domain.Entity
	relations []*domain.Relation
	episodes  []*domain.Episode
}
type searchDoneMsg []*domain.SearchResult
type statsLoadedMsg *domain.MemoryStats
type insertDoneMsg struct{ err error }
type deleteDoneMsg struct {
	what string
	err  error
}
type statusMsg string
type statusClearMsg struct{}
type entityNamesMsg map[string]string

// --- Init ---

func (m model) Init() tea.Cmd {
	return m.loadKBs()
}

func (m model) loadKBs() tea.Cmd {
	return func() tea.Msg {
		kbs, err := m.store.ListKBs(context.Background())
		if err != nil {
			return statusMsg(fmt.Sprintf("error: %v", err))
		}
		return kbsLoadedMsg(kbs)
	}
}

func (m model) loadContent() tea.Cmd {
	if m.activeKB == "" {
		return nil
	}
	coll := m.coll
	kbID := m.activeKB
	store := m.store
	return func() tea.Msg {
		ctx := context.Background()
		var msg contentLoadedMsg
		switch coll {
		case collEntities:
			ents, err := store.ListEntities(ctx, kbID, 500, 0)
			if err != nil {
				return statusMsg(fmt.Sprintf("error: %v", err))
			}
			msg.entities = ents
		case collRelations:
			rels, err := store.ListRelations(ctx, kbID, 500, 0)
			if err != nil {
				return statusMsg(fmt.Sprintf("error: %v", err))
			}
			msg.relations = rels
		case collEpisodes:
			eps, err := store.ListEpisodes(ctx, kbID, 500, 0)
			if err != nil {
				return statusMsg(fmt.Sprintf("error: %v", err))
			}
			msg.episodes = eps
		}
		return msg
	}
}

func (m model) loadEntityNames() tea.Cmd {
	kbID := m.activeKB
	store := m.store
	return func() tea.Msg {
		ents, err := store.ListEntityNames(context.Background(), kbID)
		if err != nil {
			return statusMsg(fmt.Sprintf("error loading entity names: %v", err))
		}
		names := make(map[string]string, len(ents))
		for _, e := range ents {
			names[e.ID] = e.Name
		}
		return entityNamesMsg(names)
	}
}

func (m model) doSearch(query string) tea.Cmd {
	kbID := m.activeKB
	searcher := m.searcher
	return func() tea.Msg {
		opts := search.DefaultOptions()
		opts.TopK = 50
		results, err := searcher.Search(context.Background(), kbID, query, opts)
		if err != nil {
			return statusMsg(fmt.Sprintf("search error: %v", err))
		}
		return searchDoneMsg(results)
	}
}

func (m model) doInsert(text string) tea.Cmd {
	kbID := m.activeKB
	sched := m.sched
	return func() tea.Msg {
		_, err := sched.Submit(context.Background(), kbID, text, ingestion.IngestOptions{
			Source: "tui",
		})
		return insertDoneMsg{err: err}
	}
}

func (m model) doDeleteEntity(kbID, id, name string) tea.Cmd {
	store := m.store
	return func() tea.Msg {
		err := store.DeleteEntity(context.Background(), kbID, id)
		return deleteDoneMsg{what: "entity " + name, err: err}
	}
}

func (m model) doDeleteRelation(kbID, id string) tea.Cmd {
	store := m.store
	return func() tea.Msg {
		err := store.InvalidateRelation(context.Background(), kbID, id, time.Now().UTC())
		return deleteDoneMsg{what: "relation " + id[:safeLen(id, 8)], err: err}
	}
}

func (m model) doDeleteEpisode(kbID, id string) tea.Cmd {
	store := m.store
	return func() tea.Msg {
		err := store.DeleteEpisode(context.Background(), kbID, id)
		return deleteDoneMsg{what: "episode " + id[:safeLen(id, 8)], err: err}
	}
}

func (m model) doDeleteKB(id string) tea.Cmd {
	store := m.store
	return func() tea.Msg {
		err := store.DeleteKB(context.Background(), id)
		return deleteDoneMsg{what: "KB " + id, err: err}
	}
}

func (m model) loadStats() tea.Cmd {
	kbID := m.activeKB
	store := m.store
	return func() tea.Msg {
		stats, err := store.GetStats(context.Background(), kbID)
		if err != nil {
			return statusMsg(fmt.Sprintf("error: %v", err))
		}
		return statsLoadedMsg(stats)
	}
}

// --- Update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		return m, nil

	case kbsLoadedMsg:
		m.kbs = msg
		if len(m.kbs) > 0 && m.activeKB == "" {
			m.activeKB = m.kbs[0].ID
			cmds = append(cmds, m.loadContent(), m.loadEntityNames())
		}
		if len(m.kbs) == 0 {
			m.setStatus("no KBs found — press n to create one")
		}
		return m, tea.Batch(cmds...)

	case contentLoadedMsg:
		m.entities = msg.entities
		m.relations = msg.relations
		m.episodes = msg.episodes
		m.rebuildTable()
		m.updateInspector()
		return m, nil

	case entityNamesMsg:
		m.entityNames = msg
		if m.coll == collRelations {
			m.rebuildTable()
		}
		return m, nil

	case searchDoneMsg:
		m.results = msg
		m.coll = collSearchResults
		m.rebuildTable()
		m.updateInspector()
		m.setStatus(fmt.Sprintf("found %d results for %q", len(msg), m.lastQuery))
		return m, nil

	case graphFocusedMsg:
		if m.graphV != nil {
			m.graphV.handleFocused(msg)
		}
		return m, nil

	case statsLoadedMsg:
		m.stats = msg
		m.showStats = true
		return m, nil

	case kbCreatedMsg:
		m.showWizard = false
		m.wizard = nil
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("create KB failed: %v", msg.err))
		} else {
			m.activeKB = msg.kb.ID
			m.setStatus(fmt.Sprintf("created KB: %s", msg.kb.ID))
			cmds = append(cmds, m.loadKBs(), m.loadEntityNames())
		}
		return m, tea.Batch(cmds...)

	case insertDoneMsg:
		m.inserting = false
		m.insertInput.Blur()
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("insert failed: %v", msg.err))
		} else {
			m.setStatus("memory stored — ingestion queued")
			cmds = append(cmds, m.loadContent(), m.loadEntityNames())
		}
		return m, tea.Batch(cmds...)

	case deleteDoneMsg:
		m.confirm = nil
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("delete failed: %v", msg.err))
		} else {
			m.setStatus(fmt.Sprintf("deleted %s", msg.what))
			cmds = append(cmds, m.loadKBs(), m.loadContent())
		}
		return m, tea.Batch(cmds...)

	case statusMsg:
		m.setStatus(string(msg))
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Pass to focused component.
	if m.searching {
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.inserting {
		var cmd tea.Cmd
		m.insertInput, cmd = m.insertInput.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.focus == paneContent {
		var cmd tea.Cmd
		m.contentTbl, cmd = m.contentTbl.Update(msg)
		cmds = append(cmds, cmd)
		m.updateInspector()
	} else if m.focus == paneInspector {
		var cmd tea.Cmd
		m.inspector, cmd = m.inspector.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global quit.
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	// Wizard mode.
	if m.showWizard && m.wizard != nil {
		cancelled, cmd := m.wizard.update(msg)
		if cancelled {
			m.showWizard = false
			m.wizard = nil
		}
		return m, cmd
	}

	// Confirm dialog.
	if m.confirm != nil {
		switch key {
		case "y", "Y":
			cmd := m.confirm.onYes
			m.confirm = nil
			return m, cmd
		default:
			m.confirm = nil
			return m, nil
		}
	}

	// Help overlay.
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	// Stats overlay.
	if m.showStats {
		m.showStats = false
		return m, nil
	}

	// Graph overlay — delegate all keys to graphView.
	if m.showGraph && m.graphV != nil {
		closed, cmd := m.graphV.update(key)
		if closed {
			m.showGraph = false
			m.graphV = nil
		}
		return m, cmd
	}

	// Search mode.
	if m.searching {
		switch key {
		case "enter":
			query := m.searchInput.Value()
			m.searching = false
			m.searchInput.Blur()
			if query != "" && m.activeKB != "" {
				m.lastQuery = query
				return m, m.doSearch(query)
			}
			return m, nil
		case "esc":
			m.searching = false
			m.searchInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}

	// Insert mode.
	if m.inserting {
		switch key {
		case "enter":
			text := m.insertInput.Value()
			if text != "" && m.activeKB != "" {
				m.insertInput.Blur()
				return m, m.doInsert(text)
			}
			return m, nil
		case "esc":
			m.inserting = false
			m.insertInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.insertInput, cmd = m.insertInput.Update(msg)
		return m, cmd
	}

	// Normal mode.
	switch key {
	case "q":
		return m, tea.Quit
	case "tab", "l", "right":
		m.focus = (m.focus + 1) % 3
		m.updateInspector()
		return m, nil
	case "shift+tab", "h", "left":
		m.focus = (m.focus + 2) % 3
		m.updateInspector()
		return m, nil
	case "/":
		if m.activeKB == "" {
			m.setStatus("select a KB first")
			return m, nil
		}
		m.searching = true
		m.searchInput.SetValue("")
		m.searchInput.Focus()
		return m, textinput.Blink
	case "i":
		if m.activeKB == "" {
			m.setStatus("select a KB first")
			return m, nil
		}
		m.inserting = true
		m.insertInput.SetValue("")
		m.insertInput.Focus()
		return m, textinput.Blink
	case "n":
		w := newKBWizard(m.store)
		m.wizard = &w
		m.showWizard = true
		return m, textinput.Blink
	case "e":
		m.coll = collEntities
		return m, m.loadContent()
	case "r":
		m.coll = collRelations
		return m, tea.Batch(m.loadContent(), m.loadEntityNames())
	case "p":
		m.coll = collEpisodes
		return m, m.loadContent()
	case "s":
		if m.activeKB != "" {
			return m, m.loadStats()
		}
		return m, nil
	case "x":
		return m, m.promptDelete()
	case "V":
		if m.activeKB == "" {
			return m, nil
		}
		var startID string
		originIDs := make(map[string]bool)

		switch m.coll {
		case collSearchResults:
			for _, r := range m.results {
				if r.Type == "entity" {
					originIDs[r.ID] = true
					if startID == "" {
						startID = r.ID
					}
				}
				if r.Type == "relation" {
					if src, ok := r.Metadata["source_id"]; ok {
						originIDs[src] = true
					}
					if tgt, ok := r.Metadata["target_id"]; ok {
						originIDs[tgt] = true
					}
				}
			}
		case collEntities:
			idx := m.contentTbl.Cursor()
			if idx >= 0 && idx < len(m.entities) {
				startID = m.entities[idx].ID
			}
			for _, e := range m.entities {
				originIDs[e.ID] = true
			}
		}

		if startID == "" {
			m.setStatus("no entity to start graph from — search first")
			return m, nil
		}

		m.graphV = newGraphView(m.store, m.activeKB, originIDs)
		m.showGraph = true
		return m, m.graphV.loadNode(startID)
	case "?":
		m.showHelp = true
		return m, nil
	}

	// Pane-specific keys.
	switch m.focus {
	case paneKB:
		switch key {
		case "j", "down":
			if m.kbCursor < len(m.kbs)-1 {
				m.kbCursor++
			}
		case "k", "up":
			if m.kbCursor > 0 {
				m.kbCursor--
			}
		case "g":
			m.kbCursor = 0
		case "G":
			if len(m.kbs) > 0 {
				m.kbCursor = len(m.kbs) - 1
			}
		case "enter":
			if m.kbCursor < len(m.kbs) {
				m.activeKB = m.kbs[m.kbCursor].ID
				m.focus = paneContent // jump to content after selecting KB
				m.setStatus(fmt.Sprintf("switched to KB: %s", m.activeKB))
				return m, tea.Batch(m.loadContent(), m.loadEntityNames())
			}
		case "d", "backspace":
			if m.kbCursor < len(m.kbs) {
				kb := m.kbs[m.kbCursor]
				m.confirm = &confirmDialog{
					title:   "Delete Knowledge Base",
					message: fmt.Sprintf("Delete KB %q and ALL its data?\nThis cannot be undone.", kb.ID),
					onYes:   m.doDeleteKB(kb.ID),
				}
			}
			return m, nil
		}
		return m, nil

	case paneContent:
		switch key {
		case "enter":
			// Jump to inspector to view detail
			m.updateInspector()
			m.focus = paneInspector
			return m, nil
		case "g":
			m.contentTbl.GotoTop()
			m.updateInspector()
			return m, nil
		case "G":
			m.contentTbl.GotoBottom()
			m.updateInspector()
			return m, nil
		}
		// Delegate j/k/up/down to table
		var cmd tea.Cmd
		m.contentTbl, cmd = m.contentTbl.Update(msg)
		m.updateInspector()
		return m, cmd

	case paneInspector:
		switch key {
		case "enter", "esc":
			// Return to content pane
			m.focus = paneContent
			return m, nil
		case "g":
			m.inspector.GotoTop()
			return m, nil
		case "G":
			m.inspector.GotoBottom()
			return m, nil
		}
		// Delegate j/k/up/down to viewport for scrolling
		var cmd tea.Cmd
		m.inspector, cmd = m.inspector.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *model) promptDelete() tea.Cmd {
	if m.focus == paneKB {
		if m.kbCursor < len(m.kbs) {
			kb := m.kbs[m.kbCursor]
			m.confirm = &confirmDialog{
				title:   "Delete Knowledge Base",
				message: fmt.Sprintf("Delete KB %q and ALL its data?", kb.ID),
				onYes:   m.doDeleteKB(kb.ID),
			}
		}
		return nil
	}

	if m.focus != paneContent || m.activeKB == "" {
		return nil
	}

	idx := m.contentTbl.Cursor()
	switch m.coll {
	case collEntities:
		if idx >= 0 && idx < len(m.entities) {
			e := m.entities[idx]
			m.confirm = &confirmDialog{
				title:   "Delete Entity",
				message: fmt.Sprintf("Delete entity %q (%s)?", e.Name, e.Type),
				onYes:   m.doDeleteEntity(m.activeKB, e.ID, e.Name),
			}
		}
	case collRelations:
		if idx >= 0 && idx < len(m.relations) {
			r := m.relations[idx]
			src := m.resolveEntityName(r.SourceID)
			tgt := m.resolveEntityName(r.TargetID)
			m.confirm = &confirmDialog{
				title:   "Invalidate Relation",
				message: fmt.Sprintf("Invalidate: %s -[%s]-> %s?", src, r.Type, tgt),
				onYes:   m.doDeleteRelation(m.activeKB, r.ID),
			}
		}
	case collEpisodes:
		if idx >= 0 && idx < len(m.episodes) {
			ep := m.episodes[idx]
			m.confirm = &confirmDialog{
				title:   "Delete Episode",
				message: fmt.Sprintf("Delete episode %s?", ep.ID[:safeLen(ep.ID, 12)]),
				onYes:   m.doDeleteEpisode(m.activeKB, ep.ID),
			}
		}
	}
	return nil
}

// resolveEntityName returns the entity name from cache, or a truncated ID fallback.
func (m *model) resolveEntityName(id string) string {
	if name, ok := m.entityNames[id]; ok {
		return name
	}
	return id[:safeLen(id, 8)] + "..."
}

// --- View ---

func (m model) View() string {
	if m.width == 0 {
		return "loading..."
	}

	// Wizard overlay.
	if m.showWizard && m.wizard != nil {
		return m.wizard.view(m.width, m.height)
	}

	// Confirm dialog overlay.
	if m.confirm != nil {
		return m.confirm.view(m.width, m.height)
	}

	// Help overlay.
	if m.showHelp {
		return m.renderHelp()
	}

	// Stats overlay.
	if m.showStats && m.stats != nil {
		return m.renderStats()
	}

	// Graph overlay.
	if m.showGraph && m.graphV != nil {
		return m.graphV.view(m.width, m.height)
	}

	// 3-pane layout.
	kbPane := m.renderKBPane()
	contentPane := m.renderContentPane()
	inspectorPane := m.renderInspectorPane()

	main := lipgloss.JoinHorizontal(lipgloss.Top, kbPane, contentPane, inspectorPane)

	// Input bar or status bar.
	bar := m.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, main, bar)
}

// --- Layout ---

func (m *model) kbPaneWidth() int {
	return max(22, min(m.width/5, 30))
}

func (m *model) inspPaneWidth() int {
	return max(m.width/4, 30)
}

func (m *model) contentPaneWidth() int {
	w := m.width - m.kbPaneWidth() - m.inspPaneWidth() - 6
	if w < 30 {
		w = 30
	}
	return w
}

func (m *model) recalcLayout() {
	contentH := m.height - 4
	if contentH < 5 {
		contentH = 5
	}

	contentW := m.contentPaneWidth()
	inspW := m.inspPaneWidth()

	m.contentTbl.SetWidth(contentW)
	m.contentTbl.SetHeight(contentH - 4) // room for title + tabs
	m.inspector.Width = inspW - 2
	m.inspector.Height = contentH - 2

	m.rebuildTable()
}

func (m *model) rebuildTable() {
	contentW := m.contentPaneWidth() - 4 // padding

	// Clear rows before changing columns to prevent column/row count mismatch
	// panic in bubbles table.renderRow when SetColumns triggers UpdateViewport.
	m.contentTbl.SetRows(nil)

	switch m.coll {
	case collEntities:
		m.contentTbl.SetColumns(entityColumns(contentW))
		rows := make([]table.Row, len(m.entities))
		nameW := contentW * 25 / 100
		sumW := contentW - nameW - 14 // type col = ~12 + padding
		for i, e := range m.entities {
			rows[i] = table.Row{truncStr(e.Name, nameW), e.Type, truncStr(e.Summary, sumW)}
		}
		m.contentTbl.SetRows(rows)

	case collRelations:
		m.contentTbl.SetColumns(relationColumns(contentW))
		rows := make([]table.Row, len(m.relations))
		nameW := (contentW - 18) / 3 // type ~14, remainder split 3 ways
		for i, r := range m.relations {
			src := m.resolveEntityName(r.SourceID)
			tgt := m.resolveEntityName(r.TargetID)
			rows[i] = table.Row{
				truncStr(src, nameW),
				r.Type,
				truncStr(tgt, nameW),
				truncStr(r.Summary, nameW),
			}
		}
		m.contentTbl.SetRows(rows)

	case collEpisodes:
		m.contentTbl.SetColumns(episodeColumns(contentW))
		rows := make([]table.Row, len(m.episodes))
		for i, ep := range m.episodes {
			rows[i] = table.Row{
				ep.ID[:safeLen(ep.ID, 8)],
				ep.Source,
				ep.CreatedAt.Format("Jan 02 15:04"),
				truncStr(strings.ReplaceAll(ep.Content, "\n", " "), contentW-36),
			}
		}
		m.contentTbl.SetRows(rows)

	case collSearchResults:
		m.contentTbl.SetColumns(searchColumns(contentW))
		rows := make([]table.Row, len(m.results))
		for i, r := range m.results {
			rows[i] = table.Row{
				r.Type,
				renderScore(r.Score),
				truncStr(strings.ReplaceAll(r.Content, "\n", " "), contentW-26),
			}
		}
		m.contentTbl.SetRows(rows)
	}
}

func (m *model) updateInspector() {
	idx := m.contentTbl.Cursor()
	var content string

	switch m.coll {
	case collEntities:
		if idx >= 0 && idx < len(m.entities) {
			content = renderEntityDetail(m.entities[idx])
		}
	case collRelations:
		if idx >= 0 && idx < len(m.relations) {
			content = m.renderRelationDetail(m.relations[idx])
		}
	case collEpisodes:
		if idx >= 0 && idx < len(m.episodes) {
			content = renderEpisodeDetail(m.episodes[idx])
		}
	case collSearchResults:
		if idx >= 0 && idx < len(m.results) {
			content = renderSearchResultDetail(m.results[idx])
		}
	}

	if content == "" {
		content = mutedStyle.Render("no item selected")
	}
	m.inspector.SetContent(content)
}

func (m *model) setStatus(msg string) {
	m.statusMsg = msg
	m.statusTime = time.Now()
}

// --- Render panes ---

func (m model) renderKBPane() string {
	contentH := m.height - 4
	if contentH < 5 {
		contentH = 5
	}
	w := m.kbPaneWidth()

	title := titleStyle.Render("Knowledge Bases")
	var items strings.Builder
	for i, kb := range m.kbs {
		isActive := kb.ID == m.activeKB
		isCursor := i == m.kbCursor && m.focus == paneKB

		// KB name/ID line
		prefix := "  "
		name := kb.ID
		if kb.Name != "" && kb.Name != kb.ID {
			name = kb.Name
		}
		name = truncStr(name, w-6)

		var line string
		if isActive {
			prefix = "▸ "
			line = kbActiveStyle.Render(prefix + name)
		} else {
			line = "  " + name
		}
		if isCursor {
			line = kbCursorStyle.Render(padRight(line, w-2))
		}
		items.WriteString(line + "\n")

		// Provider info line (compact)
		if isActive {
			meta := kbMetaStyle.Render(
				truncStr(kb.EmbedConfig.Provider+"/"+kb.EmbedConfig.Model, w-6),
			)
			items.WriteString(meta + "\n")
		}
	}
	if len(m.kbs) == 0 {
		items.WriteString(mutedStyle.Render("  (empty)\n"))
		items.WriteString(mutedStyle.Render("  press n to create\n"))
	}

	footer := "\n" + helpStyle.Render("n new  d delete")

	body := title + "\n" + items.String() + footer

	style := paneStyle
	if m.focus == paneKB {
		style = activePaneStyle
	}
	return style.Width(w).Height(contentH).Render(body)
}

func (m model) renderContentPane() string {
	contentH := m.height - 4
	if contentH < 5 {
		contentH = 5
	}
	w := m.contentPaneWidth()

	// Title with count
	title := titleStyle.Render(m.coll.String())
	count := m.collectionCount()
	if count > 0 {
		title += countBadgeStyle.Render(fmt.Sprintf(" (%d)", count))
	}

	// Collection tabs
	tabs := m.renderTabs()

	body := title + "\n" + tabs + "\n" + m.contentTbl.View()

	style := paneStyle
	if m.focus == paneContent {
		style = activePaneStyle
	}
	return style.Width(w).Height(contentH).Render(body)
}

func (m model) renderInspectorPane() string {
	contentH := m.height - 4
	if contentH < 5 {
		contentH = 5
	}
	w := m.inspPaneWidth()

	title := titleStyle.Render("Inspector")
	body := title + "\n" + m.inspector.View()

	style := paneStyle
	if m.focus == paneInspector {
		style = activePaneStyle
	}
	return style.Width(w).Height(contentH).Render(body)
}

func (m model) renderTabs() string {
	tabs := []struct {
		coll collection
		name string
	}{
		{collEntities, "Entities"},
		{collRelations, "Relations"},
		{collEpisodes, "Episodes"},
	}

	var parts []string
	for _, t := range tabs {
		label := "[" + t.coll.shortKey() + "] " + t.name
		if t.coll == m.coll {
			parts = append(parts, tabActiveStyle.Render(label))
		} else {
			parts = append(parts, tabInactiveStyle.Render(label))
		}
	}

	if m.coll == collSearchResults {
		parts = append(parts, tabActiveStyle.Render("Search"))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (m model) collectionCount() int {
	switch m.coll {
	case collEntities:
		return len(m.entities)
	case collRelations:
		return len(m.relations)
	case collEpisodes:
		return len(m.episodes)
	case collSearchResults:
		return len(m.results)
	}
	return 0
}

func (m model) renderStatusBar() string {
	w := m.width
	if w < 10 {
		w = 80
	}

	var left string
	if m.searching {
		left = " / " + m.searchInput.View()
	} else if m.inserting {
		left = " > " + m.insertInput.View()
	} else if m.statusMsg != "" {
		age := time.Since(m.statusTime)
		if age < 30*time.Second {
			left = " " + m.statusMsg
		} else {
			left = " ready"
		}
	} else {
		left = " ready"
	}

	keys := renderKeyHints([]keyHint{
		{"h/l", "panes"},
		{"j/k", "nav"},
		{"/", "search"},
		{"i", "insert"},
		{"n", "new"},
		{"e/r/p", "collections"},
		{"V", "graph"},
		{"x", "del"},
		{"?", "help"},
	})

	leftW := w / 2
	rightW := w - leftW

	return lipgloss.JoinHorizontal(lipgloss.Bottom,
		statusBarStyle.Width(leftW).Render(left),
		statusBarStyle.Width(rightW).Align(lipgloss.Right).Render(keys),
	)
}

type keyHint struct {
	key  string
	desc string
}

func renderKeyHints(hints []keyHint) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, statusBarKeyStyle.Render(h.key)+" "+statusBarDescStyle.Render(h.desc))
	}
	return strings.Join(parts, "  ")
}

func (m model) renderHelp() string {
	help := `
  ` + titleStyle.Render("Memex TUI — Keyboard Shortcuts") + `

  ` + labelStyle.Render("Navigation (vim-style)") + `
    h / l              Move focus left / right between panes
    j / k              Navigate items up / down
    g / G              Jump to first / last item
    enter              Select KB / open inspector detail
    esc                Back to content from inspector

  ` + labelStyle.Render("Knowledge Bases") + `
    n                  Create new KB (wizard)
    d                  Delete selected KB

  ` + labelStyle.Render("Collections") + `
    e                  Show entities
    r                  Show relations
    p                  Show episodes

  ` + labelStyle.Render("Actions") + `
    /                  Search (hybrid semantic + keyword)
    i                  Insert new memory
    x                  Delete selected item
    s                  Show KB stats
    V (shift+v)        Graph visualization of results

  ` + labelStyle.Render("General") + `
    ?                  Toggle this help
    q / ctrl+c         Quit
`
	w := min(m.width-4, 60)
	h := min(m.height-2, 26)
	return activePaneStyle.Width(w).Height(h).Padding(1, 2).Render(help)
}

func (m model) renderStats() string {
	st := m.stats
	kb := m.activeKBObj()
	content := titleStyle.Render("Knowledge Base Statistics") + "\n\n"
	if kb != nil {
		name := kb.ID
		if kb.Name != "" && kb.Name != kb.ID {
			name = kb.Name + " (" + kb.ID + ")"
		}
		content += labelStyle.Render("KB:          ") + name + "\n" +
			labelStyle.Render("Embed:       ") + kb.EmbedConfig.Provider + "/" + kb.EmbedConfig.Model + "\n" +
			labelStyle.Render("LLM:         ") + kb.LLMConfig.Provider + "/" + kb.LLMConfig.Model + "\n\n"
	}
	content += labelStyle.Render("Episodes:    ") + fmt.Sprintf("%d", st.TotalEpisodes) + "\n" +
		labelStyle.Render("Entities:    ") + fmt.Sprintf("%d", st.TotalEntities) + "\n" +
		labelStyle.Render("Relations:   ") + fmt.Sprintf("%d", st.TotalRelations) + "\n" +
		labelStyle.Render("Communities: ") + fmt.Sprintf("%d", st.TotalCommunities) + "\n" +
		labelStyle.Render("DB Size:     ") + formatBytes(st.DBSizeBytes) + "\n"

	if !st.LastIngestion.IsZero() {
		content += "\n" + mutedStyle.Render("Last ingestion: "+st.LastIngestion.Format("Jan 02, 15:04"))
	}

	content += "\n\n" + helpStyle.Render("press any key to close")

	w := min(m.width-4, 55)
	h := min(m.height-2, 18)
	return activePaneStyle.Width(w).Height(h).Padding(1, 2).Render(content)
}

func (m model) activeKBObj() *domain.KnowledgeBase {
	for _, kb := range m.kbs {
		if kb.ID == m.activeKB {
			return kb
		}
	}
	return nil
}

// --- Table columns (dynamic width) ---

func entityColumns(totalW int) []table.Column {
	nameW := totalW * 25 / 100
	typeW := 12
	sumW := totalW - nameW - typeW - 4
	if sumW < 10 {
		sumW = 10
	}
	return []table.Column{
		{Title: "Name", Width: nameW},
		{Title: "Type", Width: typeW},
		{Title: "Summary", Width: sumW},
	}
}

func relationColumns(totalW int) []table.Column {
	typeW := 14
	remaining := totalW - typeW - 6
	colW := remaining / 3
	if colW < 8 {
		colW = 8
	}
	return []table.Column{
		{Title: "Source", Width: colW},
		{Title: "Type", Width: typeW},
		{Title: "Target", Width: colW},
		{Title: "Summary", Width: colW},
	}
}

func episodeColumns(totalW int) []table.Column {
	contentW := totalW - 32
	if contentW < 20 {
		contentW = 20
	}
	return []table.Column{
		{Title: "ID", Width: 10},
		{Title: "Src", Width: 6},
		{Title: "Date", Width: 14},
		{Title: "Content", Width: contentW},
	}
}

func searchColumns(totalW int) []table.Column {
	contentW := totalW - 24
	if contentW < 20 {
		contentW = 20
	}
	return []table.Column{
		{Title: "Type", Width: 10},
		{Title: "Score", Width: 12},
		{Title: "Content", Width: contentW},
	}
}

// --- Detail renderers ---

func renderEntityDetail(e *domain.Entity) string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Name") + "\n")
	b.WriteString("  " + e.Name + "\n\n")
	b.WriteString(labelStyle.Render("Type") + "\n")
	b.WriteString("  " + e.Type + "\n\n")
	b.WriteString(labelStyle.Render("Summary") + "\n")
	b.WriteString("  " + wordWrap(e.Summary, 35) + "\n\n")
	b.WriteString(mutedStyle.Render("ID: "+e.ID) + "\n")
	b.WriteString(mutedStyle.Render("Created: "+e.CreatedAt.Format("Jan 02, 2006 15:04")) + "\n")
	b.WriteString(mutedStyle.Render("Updated: "+e.UpdatedAt.Format("Jan 02, 2006 15:04")))
	return b.String()
}

func (m model) renderRelationDetail(r *domain.Relation) string {
	src := m.resolveEntityName(r.SourceID)
	tgt := m.resolveEntityName(r.TargetID)

	var b strings.Builder
	b.WriteString(labelStyle.Render("Relation") + "\n")
	b.WriteString("  " + src + " -> " + tgt + "\n\n")
	b.WriteString(labelStyle.Render("Type") + "\n")
	b.WriteString("  " + r.Type + "\n\n")
	b.WriteString(labelStyle.Render("Weight") + "\n")
	b.WriteString(fmt.Sprintf("  %.2f\n\n", r.Weight))
	if r.Summary != "" {
		b.WriteString(labelStyle.Render("Summary") + "\n")
		b.WriteString("  " + wordWrap(r.Summary, 35) + "\n\n")
	}
	b.WriteString(mutedStyle.Render("ID: "+r.ID) + "\n")
	b.WriteString(mutedStyle.Render("Valid: "+r.ValidAt.Format("Jan 02, 2006")) + "\n")
	b.WriteString(mutedStyle.Render("Created: "+r.CreatedAt.Format("Jan 02, 2006 15:04")))
	if r.InvalidAt != nil {
		b.WriteString("\n" + errorStyle.Render("Invalidated: "+r.InvalidAt.Format("Jan 02, 2006")))
	}
	return b.String()
}

func renderEpisodeDetail(ep *domain.Episode) string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Episode") + "\n")
	b.WriteString(mutedStyle.Render("  "+ep.ID) + "\n\n")
	b.WriteString(labelStyle.Render("Source") + "\n")
	b.WriteString("  " + ep.Source + "\n\n")
	b.WriteString(labelStyle.Render("Content") + "\n")
	b.WriteString("  " + wordWrap(ep.Content, 35) + "\n\n")
	b.WriteString(mutedStyle.Render("Created: "+ep.CreatedAt.Format("Jan 02, 2006 15:04")))
	return b.String()
}

func renderSearchResultDetail(r *domain.SearchResult) string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Type") + "\n")
	b.WriteString("  " + r.Type + "\n\n")
	b.WriteString(labelStyle.Render("Score") + "\n")
	b.WriteString(fmt.Sprintf("  %.4f\n\n", r.Score))
	b.WriteString(labelStyle.Render("Content") + "\n")
	b.WriteString("  " + wordWrap(r.Content, 35) + "\n")
	if len(r.Metadata) > 0 {
		b.WriteString("\n" + labelStyle.Render("Metadata"))
		for k, v := range r.Metadata {
			b.WriteString("\n  " + mutedStyle.Render(k+":") + " " + v)
		}
	}
	b.WriteString("\n\n" + mutedStyle.Render("ID: "+r.ID))
	return b.String()
}

// --- Helpers ---

func renderScore(score float64) string {
	return fmt.Sprintf("%.4f", score)
}

func wordWrap(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}

	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = "  " + w // indent continuation
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func truncStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func safeLen(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
