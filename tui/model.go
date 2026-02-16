package tui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	ghpkg "github.com/revelo/pr-filter/internal/github"
	"github.com/revelo/pr-filter/internal/prdata"
)

// PRInfoView wraps prdata.PRInfo with TUI-local state.
type PRInfoView struct {
	prdata.PRInfo
	Checked       bool
	Saved         bool
	AIRecommended bool
	AIScore       int
	AIReasoning   string
}

type Options struct {
	PageSize    int
	SortBy      string
	SortDesc    bool
	Logs        []string
	GitHubToken string
	Filters     prdata.FilterState
	SaveFilters func(prdata.FilterState)
	SavePR      func(PRInfoView)
	DebugLog    func(string)
}

type columnWidths struct {
	repo  int
	stars int
	files int
	lines int
	issue int
}

type Model struct {
	allRows            []PRInfoView
	filters            prdata.FilterState
	filtered           []PRInfoView
	list               list.Model
	sortBy             string
	sortDesc           bool
	logs               []string
	logMode            bool
	logOffset          int
	width              int
	height             int
	columns            columnWidths
	githubToken        string
	viewport           viewport.Model
	detailMode         bool
	detailTab          string
	detailFocus        string
	detailPR           PRInfoView
	diffLoading        bool
	issueLoading       bool
	diffTitle          string
	diffError          string
	issueError         string
	diffContent        string
	issueContent       string
	diffSections       []diffSection
	diffFiles          list.Model
	saveFilters        func(prdata.FilterState)
	savePR             func(PRInfoView)
	viewMode           string
	diffLayout         string
	diffIndex          int
	diffFileWidth      int
	diffViewportWidth  int
	diffViewportHeight int
	debugLog           func(string)

	filterMode      bool
	inputs          []textinput.Model
	inputFocus      int
	userInteracted  map[string]bool // tracks PRs the user explicitly toggled
}

type prItem struct {
	pr      PRInfoView
	display string
	filter  string
}

func (i prItem) FilterValue() string {
	return i.filter
}

type fileItem struct {
	name string
}

func (i fileItem) FilterValue() string {
	return i.name
}

type fileDelegate struct{}

func (d fileDelegate) Height() int                             { return 1 }
func (d fileDelegate) Spacing() int                            { return 0 }
func (d fileDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d fileDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	file, ok := item.(fileItem)
	if !ok {
		return
	}
	width := m.Width()
	if width <= 0 {
		width = 30
	}
	line := formatCellTail(file.name, width)
	if index == m.Index() {
		line = lipgloss.NewStyle().Reverse(true).Render(line)
	}
	fmt.Fprint(w, line)
}

type prDelegate struct {
	getColumns func() columnWidths
}

func (d prDelegate) Height() int                             { return 1 }
func (d prDelegate) Spacing() int                            { return 0 }
func (d prDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d prDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pr, ok := item.(prItem)
	if !ok {
		return
	}
	line := pr.display
	if index == m.Index() {
		line = lipgloss.NewStyle().Reverse(true).Render(line)
	}
	fmt.Fprint(w, line)
}

const (
	inputRepo = iota
	inputMinFiles
	inputMinStars
	inputMinLines
	inputMaxLines
)

func NewModel(prs []PRInfoView, opts Options) Model {
	if opts.PageSize <= 0 {
		opts.PageSize = 20
	}
	if opts.SortBy == "" {
		opts.SortBy = "lines"
	}

	delegate := prDelegate{}
	l := list.New([]list.Item{}, delegate, 0, opts.PageSize)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(true)
	l.SetShowPagination(true)
	l.SetFilteringEnabled(true)

	filters := opts.Filters
	if filters == (prdata.FilterState{}) {
		filters = prdata.DefaultFilters()
	}

	// Build set of PRs the user has explicitly interacted with
	// (any PR that has Checked or Saved set at load time came from local-state.json)
	interacted := make(map[string]bool)
	for _, pr := range prs {
		if pr.Checked || pr.Saved {
			interacted[pr.URL] = true
		}
	}

	m := Model{
		allRows:        prs,
		filters:        filters,
		list:           l,
		sortBy:         opts.SortBy,
		sortDesc:       opts.SortDesc,
		logs:           append([]string{}, opts.Logs...),
		githubToken:    opts.GitHubToken,
		viewport:       viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		saveFilters:    opts.SaveFilters,
		savePR:         opts.SavePR,
		viewMode:       "active",
		diffLayout:     "side",
		debugLog:       opts.DebugLog,
		userInteracted: interacted,
	}
	m.viewport.FillHeight = true
	m.viewport.SoftWrap = false

	fileDelegate := fileDelegate{}
	fileList := list.New([]list.Item{}, fileDelegate, 0, opts.PageSize)
	fileList.SetShowTitle(false)
	fileList.SetShowHelp(false)
	fileList.SetShowStatusBar(false)
	fileList.SetShowFilter(false)
	fileList.SetShowPagination(false)
	fileList.SetFilteringEnabled(false)
	m.diffFiles = fileList

	delegate.getColumns = func() columnWidths { return m.columns }
	m.list.SetDelegate(delegate)

	m.initInputs()
	m.rebuild()
	if len(m.filtered) == 0 && len(m.allRows) > 0 && m.hasActiveFilters() {
		m.logs = append(m.logs, "No items matched current filters; showing all items instead")
		m.filters = prdata.FilterState{}
		m.syncInputs()
		m.rebuild()
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.RequestWindowSize
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.filterMode {
		return m.updateFilterMode(msg)
	}
	if m.logMode {
		return m.updateLogMode(msg)
	}
	if m.detailMode {
		return m.updateDetailMode(msg)
	}

	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			return m, m.openDetail()
		case "x":
			m.toggleChecked()
		case "m":
			m.toggleSaved()
		case "l":
			m.logMode = true
		case "f":
			m.enterFilterMode()
		case "r":
			m.filters = prdata.DefaultFilters()
			m.syncInputs()
			m.rebuild()
			m.persistFilters()
		case "c":
			m.filters = prdata.FilterState{}
			m.syncInputs()
			m.rebuild()
			m.persistFilters()
		case "v":
			m.toggleViewMode()
		case "s":
			m.cycleSort()
		case "o":
			m.sortDesc = !m.sortDesc
			m.rebuild()
		case "n":
			m.list.NextPage()
		case "p":
			m.list.PrevPage()
		case "g":
			m.list.GoToStart()
		case "G":
			m.list.GoToEnd()
		}
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	case diffMsg:
		m.handleDiffMsg(msg)
	case issueMsg:
		m.handleIssueMsg(msg)
	case dataFileChangedMsg:
		m.handleDataFileChanged(msg)
	}

	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// dataFileChangedMsg is sent when the data file is updated by the fetcher.
type dataFileChangedMsg struct {
	PRs         []prdata.PRInfo
	Stats       prdata.DataStats
	Evaluations map[string]prdata.AIEvaluation
}

func (m *Model) handleDataFileChanged(msg dataFileChangedMsg) {
	// Remember selected URL to restore cursor position
	var selectedURL string
	if item := m.list.SelectedItem(); item != nil {
		if pr, ok := item.(prItem); ok {
			selectedURL = pr.pr.URL
		}
	}

	// Build a map of current local state
	localState := make(map[string]struct{ Checked, Saved bool })
	for _, pr := range m.allRows {
		localState[pr.URL] = struct{ Checked, Saved bool }{pr.Checked, pr.Saved}
	}

	// Merge new data with local state and AI evaluations
	newRows := make([]PRInfoView, 0, len(msg.PRs))
	for _, pr := range msg.PRs {
		view := PRInfoView{PRInfo: pr}
		if state, ok := localState[pr.URL]; ok {
			view.Checked = state.Checked
			view.Saved = state.Saved
		}

		// Merge AI evaluation data
		if msg.Evaluations != nil {
			if eval, ok := msg.Evaluations[pr.URL]; ok {
				view.AIRecommended = eval.Recommended
				view.AIScore = eval.Score
				view.AIReasoning = eval.Reasoning

				// Auto-favorite: if AI recommends and user never interacted
				if eval.Recommended && !m.userInteracted[pr.URL] {
					view.Saved = true
					if m.savePR != nil {
						m.savePR(view)
					}
					// Mark as interacted so we don't re-favorite on next reload
					m.userInteracted[pr.URL] = true
				}
			}
		}

		newRows = append(newRows, view)
	}

	m.allRows = newRows
	m.rebuildKeepSelection(selectedURL)
}

func (m Model) View() tea.View {
	if m.filterMode {
		return tea.NewView(m.viewFilterMode())
	}
	if m.logMode {
		return tea.NewView(m.viewLogs())
	}
	if m.detailMode {
		return tea.NewView(m.viewDetail())
	}

	header := m.viewTabs()
	filters := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.filtersSummary())
	columns := m.columnHeader()
	selected := m.selectedInfo()
	status := m.statusLine()
	keys := "Keys: q quit | f filters | c clear | / search | v mode | x checked | m saved | n/p page | g/G first/last | s sort | o order | enter details | l logs"

	view := strings.Join([]string{
		header,
		filters,
		columns,
		m.list.View(),
		selected,
		status,
		keys,
	}, "\n")
	return tea.NewView(m.padView(view))
}

func (m *Model) rebuild() {
	filtered := make([]PRInfoView, 0, len(m.allRows))
	for _, pr := range m.allRows {
		if pr.Taken {
			continue
		}
		if m.viewMode == "active" && pr.Hydration < 1 {
			continue
		}
		if m.viewMode == "active" && pr.Checked {
			continue
		}
		if m.viewMode == "checked" && !pr.Checked {
			continue
		}
		if m.viewMode == "saved" && !pr.Saved {
			continue
		}
		if m.viewMode == "active" {
			if !m.filters.Matches(pr.PRInfo) {
				continue
			}
		}
		filtered = append(filtered, pr)
	}

	sortPRViews(filtered, m.sortBy, m.sortDesc)
	m.filtered = filtered
	m.refreshItems()
}

func (m *Model) rebuildKeepSelection(selectedURL string) {
	m.rebuild()

	if selectedURL == "" {
		return
	}
	for i, item := range m.list.Items() {
		if pr, ok := item.(prItem); ok && pr.pr.URL == selectedURL {
			m.list.Select(i)
			return
		}
	}
}

func (m *Model) refreshItems() {
	items := make([]list.Item, 0, len(m.filtered))
	for _, pr := range m.filtered {
		items = append(items, prItem{
			pr:      pr,
			display: m.formatRow(pr),
			filter:  fmt.Sprintf("%s %s %s", pr.Repository, pr.Title, pr.URL),
		})
	}
	m.list.SetItems(items)
	if len(items) > 0 {
		m.list.Select(0)
	}
}

func (m *Model) resize(width, height int) {
	m.width = width
	m.height = height
	m.debugf("resize width=%d height=%d", width, height)
	m.updateLayout()
}

func (m *Model) updateLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	m.columns = computeColumns(m.width)
	m.list.SetWidth(m.width)

	// header(tabs) + filters + columns + selected + status + keys = 6 lines
	// list also renders pagination (+1) and filter bar when active (+1)
	chrome := 8
	listHeight := m.height - chrome
	if listHeight < 3 {
		listHeight = 3
	}
	m.list.SetHeight(listHeight)

	detailChrome := 7
	diffHeight := m.height - detailChrome
	if diffHeight < 3 {
		diffHeight = 3
	}
	minViewport := 30
	filePaneWidth := int(float64(m.width) * 0.22)
	if filePaneWidth < 18 {
		filePaneWidth = 18
	}
	maxFileWidth := m.width - minViewport
	if maxFileWidth < 18 {
		maxFileWidth = 18
	}
	if filePaneWidth > maxFileWidth {
		filePaneWidth = maxFileWidth
	}
	innerFileWidth := filePaneWidth - 2
	if innerFileWidth < 1 {
		innerFileWidth = 1
	}
	m.diffFileWidth = innerFileWidth
	m.diffFiles.SetWidth(innerFileWidth)
	m.diffFiles.SetHeight(diffHeight)

	viewportWidth := m.width - filePaneWidth - 2
	if viewportWidth < minViewport {
		viewportWidth = minViewport
		filePaneWidth = m.width - viewportWidth - 2
		innerFileWidth = filePaneWidth - 2
		if innerFileWidth < 1 {
			innerFileWidth = 1
		}
		m.diffFileWidth = innerFileWidth
		m.diffFiles.SetWidth(innerFileWidth)
	}
	m.diffViewportWidth = viewportWidth
	m.diffViewportHeight = diffHeight
	if m.detailMode && m.detailTab == "diff" && m.diffLayout == "inline" {
		m.viewport.SetWidth(m.width - 2)
	} else {
		m.viewport.SetWidth(viewportWidth)
	}
	m.viewport.SetHeight(diffHeight)
	m.viewport.SoftWrap = false
	m.debugf("layout filePaneWidth=%d fileWidth=%d viewportWidth=%d diffHeight=%d", filePaneWidth, m.diffFileWidth, m.diffViewportWidth, m.diffViewportHeight)
	m.refreshItems()
}

func computeColumns(width int) columnWidths {
	fixed := 7 + 6 + 7 + 2
	spacing := 5
	available := width - fixed - spacing
	if available < 30 {
		available = 30
	}
	repoWidth := available / 3
	if repoWidth < 20 {
		repoWidth = 20
	}
	issueWidth := available - repoWidth
	if issueWidth < 20 {
		issueWidth = 20
		repoWidth = available - issueWidth
		if repoWidth < 10 {
			repoWidth = 10
		}
	}
	return columnWidths{
		repo:  repoWidth,
		stars: 7,
		files: 6,
		lines: 7,
		issue: issueWidth,
	}
}

func (m *Model) formatRow(pr PRInfoView) string {
	issue := pr.ResolvedIssue
	if issue == "" {
		issue = "-"
	}
	cols := m.columns
	mark := ""
	if pr.Checked {
		mark = "x"
	} else if pr.AIRecommended && pr.Saved {
		mark = "AS"
	} else if pr.AIRecommended {
		mark = "A"
	} else if pr.Saved {
		mark = "S"
	}
	repo := formatCell(pr.Repository, cols.repo)
	stars := formatCell(formatMetric(pr.Stars, pr.StarsKnown), cols.stars)
	files := formatCell(formatMetric(pr.FilesChanged, pr.Hydration >= 1), cols.files)
	lines := formatCell(formatMetric(pr.LinesChanged, pr.Hydration >= 1), cols.lines)
	iss := formatCell(issue, cols.issue)

	return fmt.Sprintf("%s %s %s %s %s %s", formatCell(mark, 2), repo, stars, files, lines, iss)
}

func formatMetric(value int, known bool) string {
	if !known {
		return "-"
	}
	return strconv.Itoa(value)
}

func formatCell(value string, width int) string {
	if width <= 0 {
		return ""
	}
	truncated := runewidth.Truncate(value, width, "")
	return runewidth.FillRight(truncated, width)
}

func formatCellTail(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(value) <= width {
		return runewidth.FillRight(value, width)
	}
	if width == 1 {
		return "…"
	}
	parts := strings.Split(value, "/")
	if len(parts) > 1 {
		suffix := parts[len(parts)-1]
		for i := len(parts) - 2; i >= 0; i-- {
			candidate := parts[i] + "/" + suffix
			if runewidth.StringWidth(candidate)+2 > width {
				break
			}
			suffix = candidate
		}
		result := "…/" + suffix
		return runewidth.FillRight(runewidth.Truncate(result, width, ""), width)
	}
	trimmed := runewidth.TruncateLeft(value, width-1, "")
	return runewidth.FillRight("…"+trimmed, width)
}

func (m *Model) columnHeader() string {
	cols := m.columns
	if cols.repo == 0 {
		cols = computeColumns(80)
	}
	return fmt.Sprintf(
		"%s %s %s %s %s %s",
		formatCell("", 2),
		formatCell("REPOSITORY", cols.repo),
		formatCell("STARS", cols.stars),
		formatCell("FILES", cols.files),
		formatCell("LINES", cols.lines),
		formatCell("RESOLVED ISSUE", cols.issue),
	)
}

func (m Model) viewTabs() string {
	mode := m.viewMode
	if mode == "" {
		mode = "active"
	}

	tabs := []struct {
		key   string
		label string
	}{
		{"active", "Active"},
		{"saved", "Favorites"},
		{"checked", "Checked"},
	}

	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("62")).
		Padding(0, 2)
	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Background(lipgloss.Color("236")).
		Padding(0, 2)

	var parts []string
	for _, tab := range tabs {
		if tab.key == mode {
			parts = append(parts, activeStyle.Render(tab.label))
		} else {
			parts = append(parts, inactiveStyle.Render(tab.label))
		}
	}

	title := lipgloss.NewStyle().Bold(true).MarginRight(1).Render("PR Filter")
	return title + strings.Join(parts, " ")
}

func (m *Model) selectedInfo() string {
	item := m.list.SelectedItem()
	pr, ok := item.(prItem)
	if !ok || pr.pr.URL == "" {
		return ""
	}
	line := fmt.Sprintf("PR: %s", pr.pr.URL)
	if pr.pr.Title != "" {
		line = fmt.Sprintf("PR: %s | %s", pr.pr.URL, pr.pr.Title)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(line)
}

func (m *Model) statusLine() string {
	count := len(m.filtered)
	total := len(m.allRows)
	mode := m.viewMode
	if mode == "" {
		mode = "active"
	}
	if count == 0 && total > 0 {
		return fmt.Sprintf("Items: %d/%d | View: %s | Filter: %s | Tip: press c to clear filters", count, total, mode, m.list.FilterValue())
	}
	return fmt.Sprintf("Items: %d/%d | View: %s | Filter: %s", count, total, mode, m.list.FilterValue())
}

func (m *Model) hasActiveFilters() bool {
	f := m.filters
	return f.RepoQuery != "" || f.MinFiles > 0 || f.MinStars > 0 || f.MinLines > 0 || f.MaxLines > 0 || f.RequireTestFiles || f.RequireSingleIssue
}

func (m *Model) filtersSummary() string {
	parts := []string{}
	if m.filters.RepoQuery != "" {
		parts = append(parts, fmt.Sprintf("repo=%s", m.filters.RepoQuery))
	}
	if m.filters.MinFiles > 0 {
		parts = append(parts, fmt.Sprintf("minFiles=%d", m.filters.MinFiles))
	}
	if m.filters.MinStars > 0 {
		parts = append(parts, fmt.Sprintf("minStars=%d", m.filters.MinStars))
	}
	if m.filters.MinLines > 0 {
		parts = append(parts, fmt.Sprintf("minLines=%d", m.filters.MinLines))
	}
	if m.filters.MaxLines > 0 {
		parts = append(parts, fmt.Sprintf("maxLines=%d", m.filters.MaxLines))
	}
	if m.filters.RequireTestFiles {
		parts = append(parts, "tests=on")
	}
	if m.filters.RequireSingleIssue {
		parts = append(parts, "singleIssue=on")
	}
	if len(parts) == 0 {
		parts = append(parts, "filters=none")
	}

	return fmt.Sprintf("Filters: %s | Sort: %s %s | Not taken only", strings.Join(parts, " "), m.sortBy, sortLabel(m.sortDesc))
}

func (m *Model) cycleSort() {
	switch m.sortBy {
	case "lines":
		m.sortBy = "files"
	case "files":
		m.sortBy = "stars"
	case "stars":
		m.sortBy = "repository"
	default:
		m.sortBy = "lines"
	}
	m.rebuild()
}

func sortLabel(desc bool) string {
	if desc {
		return "desc"
	}
	return "asc"
}

func sortPRViews(prs []PRInfoView, sortBy string, desc bool) {
	sort.Slice(prs, func(i, j int) bool {
		var less bool
		switch sortBy {
		case "files":
			less = prs[i].FilesChanged < prs[j].FilesChanged
		case "stars":
			less = prs[i].Stars < prs[j].Stars
		case "repository":
			less = prs[i].Repository < prs[j].Repository
		default:
			less = prs[i].LinesChanged < prs[j].LinesChanged
		}
		if desc {
			return !less
		}
		return less
	})
}

func (m *Model) initInputs() {
	inputs := make([]textinput.Model, 5)
	for i := range inputs {
		inputs[i] = textinput.New()
		inputs[i].Prompt = ""
		inputs[i].CharLimit = 64
	}
	inputs[inputRepo].Placeholder = "owner/repo"
	inputs[inputMinFiles].Placeholder = "4"
	inputs[inputMinStars].Placeholder = "200"
	inputs[inputMinLines].Placeholder = "50"
	inputs[inputMaxLines].Placeholder = "0"

	m.inputs = inputs
	m.syncInputs()
}

func (m *Model) syncInputs() {
	m.inputs[inputRepo].SetValue(m.filters.RepoQuery)
	m.inputs[inputMinFiles].SetValue(intToString(m.filters.MinFiles))
	m.inputs[inputMinStars].SetValue(intToString(m.filters.MinStars))
	m.inputs[inputMinLines].SetValue(intToString(m.filters.MinLines))
	m.inputs[inputMaxLines].SetValue(intToString(m.filters.MaxLines))
}

func intToString(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func (m *Model) enterFilterMode() {
	m.filterMode = true
	m.inputFocus = 0
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.inputs[m.inputFocus].Focus()
}

func (m Model) updateFilterMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.filterMode = false
			return m, nil
		case "enter":
			m.applyInputs()
			m.filterMode = false
			return m, nil
		case "tab", "down":
			m.focusNextInput()
		case "shift+tab", "up":
			m.focusPrevInput()
		case "t":
			m.filters.RequireTestFiles = !m.filters.RequireTestFiles
		case "i":
			m.filters.RequireSingleIssue = !m.filters.RequireSingleIssue
		case "r":
			m.filters = prdata.DefaultFilters()
			m.syncInputs()
			m.persistFilters()
		case "c":
			m.filters = prdata.FilterState{}
			m.syncInputs()
			m.persistFilters()
		}
	}

	m.inputs[m.inputFocus], cmd = m.inputs[m.inputFocus].Update(msg)
	return m, cmd
}

func (m *Model) focusNextInput() {
	m.inputs[m.inputFocus].Blur()
	m.inputFocus = (m.inputFocus + 1) % len(m.inputs)
	m.inputs[m.inputFocus].Focus()
}

func (m *Model) focusPrevInput() {
	m.inputs[m.inputFocus].Blur()
	m.inputFocus--
	if m.inputFocus < 0 {
		m.inputFocus = len(m.inputs) - 1
	}
	m.inputs[m.inputFocus].Focus()
}

func (m *Model) applyInputs() {
	m.filters.RepoQuery = strings.TrimSpace(m.inputs[inputRepo].Value())
	m.filters.MinFiles = parseInt(m.inputs[inputMinFiles].Value())
	m.filters.MinStars = parseInt(m.inputs[inputMinStars].Value())
	m.filters.MinLines = parseInt(m.inputs[inputMinLines].Value())
	m.filters.MaxLines = parseInt(m.inputs[inputMaxLines].Value())
	m.rebuild()
	m.persistFilters()
}

func parseInt(raw string) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func (m Model) viewFilterMode() string {
	labelStyle := lipgloss.NewStyle().Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	lines := []string{
		labelStyle.Render("Filters"),
		"",
		fmt.Sprintf("Repo query: %s", m.inputs[inputRepo].View()),
		fmt.Sprintf("Min files:  %s", m.inputs[inputMinFiles].View()),
		fmt.Sprintf("Min stars:  %s", m.inputs[inputMinStars].View()),
		fmt.Sprintf("Min lines:  %s", m.inputs[inputMinLines].View()),
		fmt.Sprintf("Max lines:  %s", m.inputs[inputMaxLines].View()),
		"",
		fmt.Sprintf("Require tests: %s", onOff(m.filters.RequireTestFiles)),
		fmt.Sprintf("Single issue:  %s", onOff(m.filters.RequireSingleIssue)),
		"",
		helpStyle.Render("Enter apply | Esc cancel | Tab move | t toggle tests | i toggle issue | r reset | c clear"),
	}

	return strings.Join(lines, "\n")
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func (m *Model) persistFilters() {
	if m.saveFilters == nil {
		return
	}
	m.saveFilters(m.filters)
}

func (m *Model) toggleChecked() {
	item := m.list.SelectedItem()
	prItem, ok := item.(prItem)
	if !ok || prItem.pr.URL == "" {
		return
	}

	for i, pr := range m.allRows {
		if pr.URL == prItem.pr.URL {
			pr.Checked = !pr.Checked
			if pr.Checked {
				pr.Saved = false
			}
			m.allRows[i] = pr
			m.userInteracted[pr.URL] = true
			if m.savePR != nil {
				m.savePR(pr)
			}
			break
		}
	}
	m.rebuild()
}

func (m *Model) toggleSaved() {
	item := m.list.SelectedItem()
	prItem, ok := item.(prItem)
	if !ok || prItem.pr.URL == "" {
		return
	}

	for i, pr := range m.allRows {
		if pr.URL == prItem.pr.URL {
			pr.Saved = !pr.Saved
			if pr.Saved {
				pr.Checked = false
			}
			m.allRows[i] = pr
			m.userInteracted[pr.URL] = true
			if m.savePR != nil {
				m.savePR(pr)
			}
			break
		}
	}
	m.rebuild()
}

func (m *Model) toggleViewMode() {
	switch m.viewMode {
	case "active":
		m.viewMode = "saved"
	case "saved":
		m.viewMode = "checked"
	case "checked":
		m.viewMode = "active"
	default:
		m.viewMode = "active"
	}
	m.rebuild()
}

func (m *Model) toggleDiffLayout() {
	if m.detailTab != "diff" {
		return
	}
	if m.diffLayout == "inline" {
		m.diffLayout = "side"
		m.diffIndex = -1
		m.updateDiffSelection()
		return
	}
	m.diffLayout = "inline"
	m.diffIndex = -1
	m.updateDiffSelection()
}

func (m *Model) debugf(format string, args ...any) {
	if m.debugLog == nil {
		return
	}
	m.debugLog(fmt.Sprintf(format, args...))
}

func (m Model) viewLogs() string {
	header := lipgloss.NewStyle().Bold(true).Render("PR Filter Logs")
	helper := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Keys: q quit | l back | j/k scroll | g/G top/bottom")
	if len(m.logs) == 0 {
		return m.padView(strings.Join([]string{header, "", "No logs yet.", "", helper}, "\n"))
	}

	start := m.logOffset
	if start < 0 {
		start = 0
	}
	maxLines := m.height - 4
	if maxLines < 1 {
		maxLines = 10
	}
	end := start + maxLines
	if end > len(m.logs) {
		end = len(m.logs)
	}

	body := strings.Join(m.logs[start:end], "\n")
	footer := fmt.Sprintf("Showing %d-%d of %d", start+1, end, len(m.logs))
	return m.padView(strings.Join([]string{header, "", body, "", footer, helper}, "\n"))
}

func (m Model) updateLogMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "l", "esc":
			m.logMode = false
		case "j", "down":
			m.logOffset++
		case "k", "up":
			m.logOffset--
		case "g":
			m.logOffset = 0
		case "G":
			m.logOffset = len(m.logs) - 1
		}
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	}

	if m.logOffset < 0 {
		m.logOffset = 0
	}
	if m.logOffset > len(m.logs)-1 {
		m.logOffset = len(m.logs) - 1
		if m.logOffset < 0 {
			m.logOffset = 0
		}
	}

	return m, nil
}

func (m Model) padView(view string) string {
	if m.width <= 0 && m.height <= 0 {
		return view
	}
	style := lipgloss.NewStyle()
	if m.width > 0 {
		style = style.Width(m.width)
	}
	if m.height > 0 {
		style = style.Height(m.height)
	}
	return style.Render(view)
}

type diffMsg struct {
	raw     string
	content string
	files   []string
	err     error
}

type hydrateMsg struct {
	pr  prdata.PRInfo
	err error
}

func hydratePRCmd(pr prdata.PRInfo, token string) tea.Cmd {
	return func() tea.Msg {
		updated, err := ghpkg.HydratePRPass2(context.Background(), pr, token)
		if err != nil {
			return hydrateMsg{err: err}
		}
		return hydrateMsg{pr: updated}
	}
}

func (m *Model) openDetail() tea.Cmd {
	item := m.list.SelectedItem()
	pr, ok := item.(prItem)
	if !ok || pr.pr.URL == "" {
		return nil
	}

	m.detailMode = true
	m.detailTab = "diff"
	m.detailFocus = "files"
	m.detailPR = pr.pr
	m.diffLoading = true
	m.diffError = ""
	m.diffTitle = fmt.Sprintf("Details: %s", pr.pr.Repository)
	m.diffContent = ""
	m.issueContent = ""
	m.issueError = ""
	m.issueLoading = false
	m.diffSections = nil
	m.diffFiles.SetItems(nil)
	m.diffIndex = -1
	m.viewport.SetContent("Loading diff...")

	if pr.pr.ResolvedIssue == "" {
		m.issueContent = "No resolved issue found for this PR."
	}

	cmds := []tea.Cmd{fetchDiffCmd(pr.pr.URL, m.githubToken)}
	if pr.pr.Hydration < 2 {
		cmds = append(cmds, hydratePRCmd(pr.pr.PRInfo, m.githubToken))
	}
	return tea.Batch(cmds...)
}

func (m Model) updateDetailMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.detailMode = false
			return m, nil
		case "tab":
			return m.switchDetailTab()
		case "t":
			m.toggleDiffLayout()
		case "H":
			if m.detailTab == "diff" {
				m.detailFocus = "files"
			}
		case "L":
			if m.detailTab == "diff" {
				m.detailFocus = "diff"
			}
		case "j":
			if m.detailTab != "diff" || m.detailFocus != "files" {
				m.viewport.ScrollDown(1)
			}
		case "k":
			if m.detailTab != "diff" || m.detailFocus != "files" {
				m.viewport.ScrollUp(1)
			}
		}
	case diffMsg:
		m.handleDiffMsg(msg)
	case issueMsg:
		m.handleIssueMsg(msg)
	case hydrateMsg:
		m.handleHydrateMsg(msg)
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	}

	if m.detailTab == "diff" && m.detailFocus == "files" {
		m.diffFiles, cmd = m.diffFiles.Update(msg)
		m.updateDiffSelection()
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m Model) switchDetailTab() (tea.Model, tea.Cmd) {
	if m.detailTab == "diff" {
		m.detailTab = "issue"
		m.detailFocus = "diff"
		if m.issueContent == "" && m.detailPR.ResolvedIssue != "" && !m.issueLoading {
			m.issueLoading = true
			m.viewport.SetContent("Loading issue...")
			return m, fetchIssueCmd(m.detailPR.ResolvedIssue, m.githubToken)
		}
		m.viewport.SetContent(m.issueContent)
		m.viewport.GotoTop()
		return m, nil
	}

	m.detailTab = "diff"
	m.detailFocus = "files"
	if m.diffContent == "" && !m.diffLoading {
		m.diffLoading = true
		m.viewport.SetContent("Loading diff...")
		return m, fetchDiffCmd(m.detailPR.URL, m.githubToken)
	}
	m.viewport.SetContent(m.diffContent)
	m.viewport.GotoTop()
	return m, nil
}

func (m *Model) handleDiffMsg(msg diffMsg) {
	m.diffLoading = false
	if msg.err != nil {
		m.diffError = msg.err.Error()
		m.diffContent = fmt.Sprintf("Diff error: %s", m.diffError)
		m.logs = append(m.logs, fmt.Sprintf("Diff error: %v", msg.err))
	} else {
		m.diffError = ""
		m.diffContent = msg.content
		m.diffSections = parseDiffSections(msg.raw)
		m.refreshDiffFiles()
	}
	if len(msg.files) > 0 {
		m.diffSections = make([]diffSection, 0, len(msg.files))
		for _, name := range msg.files {
			m.diffSections = append(m.diffSections, diffSection{file: name})
		}
		m.diffContent = "Diff too large to display. Use file list to navigate or open locally."
		m.refreshDiffFiles()
	}
	if m.detailMode && m.detailTab == "diff" {
		m.diffFiles.Select(0)
		m.updateDiffSelection()
		m.viewport.GotoTop()
	}
}

func (m *Model) handleIssueMsg(msg issueMsg) {
	m.issueLoading = false
	if msg.err != nil {
		m.issueError = msg.err.Error()
		m.issueContent = ""
		m.logs = append(m.logs, fmt.Sprintf("Issue error: %v", msg.err))
	} else {
		m.issueError = ""
		m.issueContent = msg.content
	}
	if m.detailMode && m.detailTab == "issue" {
		m.viewport.SetContent(m.issueContent)
		m.viewport.GotoTop()
	}
}

func (m *Model) handleHydrateMsg(msg hydrateMsg) {
	if msg.err != nil {
		m.logs = append(m.logs, fmt.Sprintf("Hydration error: %v", msg.err))
		return
	}
	for i, pr := range m.allRows {
		if pr.URL == msg.pr.URL {
			view := PRInfoView{
				PRInfo:  msg.pr,
				Checked: pr.Checked,
				Saved:   pr.Saved,
			}
			m.allRows[i] = view
			if m.savePR != nil {
				m.savePR(view)
			}
			break
		}
	}
	if m.detailPR.URL == msg.pr.URL {
		m.detailPR.PRInfo = msg.pr
	}
	m.rebuild()
}

func (m Model) viewDetail() string {
	active := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	diffTab := "Diff"
	issueTab := "Issue"
	if m.detailTab == "diff" {
		diffTab = active.Render(diffTab)
		issueTab = muted.Render(issueTab)
	} else {
		diffTab = muted.Render(diffTab)
		issueTab = active.Render(issueTab)
	}

	header := lipgloss.NewStyle().Bold(true).Render(m.diffTitle)
	issueLine := "Issue: -"
	if m.detailPR.ResolvedIssue != "" {
		issueLine = fmt.Sprintf("Issue: %s", m.detailPR.ResolvedIssue)
	}
	prLine := fmt.Sprintf("PR: %s", m.detailPR.URL)
	if m.detailPR.Title != "" {
		prLine = fmt.Sprintf("PR: %s | %s", m.detailPR.URL, m.detailPR.Title)
	}
	stars := "-"
	if m.detailPR.StarsKnown {
		stars = strconv.Itoa(m.detailPR.Stars)
	}
	tests := "-"
	if m.detailPR.HasTestKnown {
		if m.detailPR.HasTestFiles {
			tests = "yes"
		} else {
			tests = "no"
		}
	}
	metaLine := fmt.Sprintf("Stars: %s | Files: %d | Lines: %d | Tests changed: %s", stars, m.detailPR.FilesChanged, m.detailPR.LinesChanged, tests)
	if m.detailPR.AIScore > 0 {
		metaLine += fmt.Sprintf(" | AI: %d/10 — %q", m.detailPR.AIScore, m.detailPR.AIReasoning)
	}
	tabs := fmt.Sprintf("[%s] [%s]", diffTab, issueTab)
	status := ""
	if m.detailTab == "diff" {
		if m.diffLoading {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Loading diff...")
		} else if m.diffError != "" {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("160")).Render(m.diffError)
		} else if m.viewport.GetContent() == "" {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("No diff content")
		}
	} else {
		if m.issueLoading {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Loading issue...")
		} else if m.issueError != "" {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("160")).Render(m.issueError)
		}
	}

	content := m.viewport.View()
	if m.detailTab == "diff" {
		left := m.diffFiles.View()
		leftStyle := lipgloss.NewStyle()
		rightStyle := lipgloss.NewStyle()
		if m.detailFocus == "files" {
			leftStyle = leftStyle.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("75"))
			rightStyle = rightStyle.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
		} else {
			leftStyle = leftStyle.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
			rightStyle = rightStyle.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("75"))
		}
		filePaneWidth := m.diffFileWidth + 2
		paneHeight := m.diffViewportHeight + 2
		left = leftStyle.Width(filePaneWidth).Height(paneHeight).Render(left)
		content = rightStyle.Width(m.diffViewportWidth + 2).Height(paneHeight).Render(content)
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, content)
	}

	help := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("Keys: tab switch | t layout | H/L focus | esc back | q quit | j/k scroll")
	return m.padView(strings.Join([]string{header, prLine, metaLine, issueLine, tabs, status, content, help}, "\n"))
}

func (m *Model) refreshDiffFiles() {
	items := make([]list.Item, 0, len(m.diffSections))
	for _, section := range m.diffSections {
		name := section.file
		if name == "" {
			name = "(unknown file)"
		}
		items = append(items, fileItem{name: name})
	}
	if len(items) == 0 {
		items = append(items, fileItem{name: "(no diff)"})
	}
	m.diffFiles.SetItems(items)
	if len(items) > 0 {
		m.diffFiles.Select(0)
	}
}

func (m *Model) updateDiffSelection() {
	index := m.diffFiles.Index()
	if index < 0 || index >= len(m.diffSections) {
		m.viewport.SetContent(m.diffContent)
		m.viewport.GotoTop()
		return
	}
	if index == m.diffIndex {
		return
	}
	m.diffIndex = index
	section := m.diffSections[index]
	if m.diffLayout == "side" {
		if section.renderSide == "" && section.raw != "" {
			section.renderSide = renderSideBySideDiff(section.raw, m.diffViewportWidth)
			m.diffSections[index] = section
		}
		if section.renderSide != "" {
			m.viewport.SetContent(section.renderSide)
			m.viewport.GotoTop()
		}
		return
	}
	if section.render == "" && section.raw != "" {
		rendered, err := renderDiffSection(section)
		if err != nil {
			section.render = section.raw
			m.logs = append(m.logs, fmt.Sprintf("Diff render error: %v", err))
		} else {
			section.render = rendered
		}
		m.diffSections[index] = section
	}
	if section.raw == "" && section.render == "" {
		m.viewport.SetContent(m.diffContent)
		m.viewport.GotoTop()
		return
	}
	if section.render != "" {
		m.viewport.SetContent(section.render)
		m.viewport.GotoTop()
	}
}

// SetAllRows replaces the PR list (used by file watcher).
func (m *Model) SetAllRows(prs []PRInfoView) {
	m.allRows = prs
	m.rebuild()
}
