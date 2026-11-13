package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/components"
	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"
	"github.com/travlytle/xpu-smi-tui/pkg/ui/views"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model represents the top-level Bubble Tea state
type Model struct {
	cliCollector  xpu.Collector
	mockCollector xpu.Collector
	currentCol    xpu.Collector

	devices  []xpu.DeviceInfo
	gpus     []*xpu.GPUCombined
	gpuMap   map[int]*xpu.GPUCombined
	topology *xpu.TopologyData

	activeTab       components.TabType
	selectedGPU     int
	refreshInterval time.Duration
	isPaused        bool
	isDemo          bool
	showHelp        bool
	showLegend      bool
	lastKey         string
	filterMode      bool
	filterText      string
	statusMsg       string

	termWidth  int
	termHeight int

	// lastDataAt is when telemetry last arrived. The header clock always shows
	// the current time, so without this a failed collection kept rendering old
	// numbers under a clock that looked live.
	lastDataAt time.Time
	// inFlight is set while a fetch is outstanding. A tick that arrives before
	// the previous fetch returns is skipped: at the default 1 s refresh a
	// collection takes about as long as the interval, and letting them pile up
	// meant several xpu-smi processes per card at once and DataLoadedMsgs
	// arriving out of order.
	inFlight bool
	// tickGen is the current tick chain; see TickMsg.
	tickGen int
	// ticksSinceHealth counts polls since the health check last ran. The check
	// is the slowest xpu-smi call (~0.8 s here) and its thresholds change on the
	// order of never, so it runs every healthEvery ticks rather than every one.
	ticksSinceHealth int
	// statusSeq tags the current status message; see StatusClearMsg.
	statusSeq int

	// viewport scrolls whatever the active tab renders. Several views are
	// simply taller than a terminal -- two full health cards run to about 66
	// rows -- and before this they were cut off at the bottom with no way to
	// reach the rest.
	viewport viewport.Model
}

// Config holds initial startup options
type Config struct {
	RefreshInterval time.Duration
	DemoMode        bool
	DefaultDevice   int
	GPUOrder        []string // PCI addresses, in the order GPUs are numbered
	IdlePolicy      xpu.IdlePolicy
}

// NewModel initializes the application model
func NewModel(cfg Config) *Model {
	cli := xpu.NewLiveCollector()
	cli.SetGPUOrder(cfg.GPUOrder)
	cli.SetIdlePolicy(cfg.IdlePolicy)
	mock := xpu.NewMockCollector()

	var activeCol xpu.Collector = cli
	if cfg.DemoMode {
		activeCol = mock
	}

	interval := cfg.RefreshInterval
	if interval <= 0 {
		interval = 1 * time.Second
	}

	// -1 means "all cards"; any non-negative value is a card index. The flag
	// used to default to 0 and treat 0 as unset, so --device 0 could not be
	// expressed at all.
	defaultGPU := cfg.DefaultDevice

	return &Model{
		cliCollector:    cli,
		mockCollector:   mock,
		currentCol:      activeCol,
		gpuMap:          make(map[int]*xpu.GPUCombined),
		refreshInterval: interval,
		isDemo:          cfg.DemoMode,
		selectedGPU:     defaultGPU,
		activeTab:       components.TabOverview,
		termWidth:       100,
		termHeight:      30,
		viewport:        viewport.New(viewport.WithWidth(100), viewport.WithHeight(20)),
	}
}

// Close releases what the collectors hold. Called by main once the program
// has returned.
func (m *Model) Close() {
	if c, ok := m.cliCollector.(interface{ Close() }); ok {
		c.Close()
	}
}

// Init starts initial discovery and data fetch
func (m *Model) Init() tea.Cmd {
	// Discovery plus the first fetch reliably takes over a second here --
	// xpu-smi's own discovery call alone was measured at ~850ms -- so the
	// first TickMsg fires before it returns. Without this, inFlight was still
	// false at that point (only the TickMsg handler ever set it), so the tick
	// saw an empty device list and an idle flag and dispatched a *second*,
	// overlapping discoverAndFetchCmd. The two raced CollectStats for the
	// same lock, and the loser is exactly what surfaced as "Error: previous
	// collection still running" -- on nearly every launch, since discovery is
	// almost always slower than one refresh interval.
	m.inFlight = true
	return tea.Batch(
		m.discoverAndFetchCmd(),
		m.tickCmd(),
	)
}

func (m *Model) tickCmd() tea.Cmd {
	gen := m.tickGen
	return tea.Tick(m.refreshInterval, func(t time.Time) tea.Msg {
		return TickMsg{At: t, Gen: gen}
	})
}

func (m *Model) clearStatusCmd(d time.Duration) tea.Cmd {
	seq := m.statusSeq
	return tea.Tick(d, func(time.Time) tea.Msg {
		return StatusClearMsg{Seq: seq}
	})
}

// setStatus shows a transient message and arms its own clear.
func (m *Model) setStatus(msg string, d time.Duration) tea.Cmd {
	m.statusSeq++
	m.statusMsg = msg
	return m.clearStatusCmd(d)
}

// fetchAll runs the per-tick collectors concurrently against col.
//
// It takes the collector as an argument rather than reading m.currentCol: the
// closure runs on bubbletea's command goroutine while the D key rewrites
// m.currentCol on the event loop -- an unsynchronised write against a read that
// could also leave one fetch running half against each collector. The command
// snapshots the collector when it is built, on the event loop.
//
// Only what changes is polled. Workloads come from fdinfo inside CollectStats
// and topology is static, read once at discovery.
func fetchAll(ctx context.Context, col xpu.Collector, devIDs []int, withHealth bool) (map[int]*xpu.GPUStats, error, map[int]xpu.DeviceHealth) {
	var (
		wg       sync.WaitGroup
		stats    map[int]*xpu.GPUStats
		statsErr error
		health   map[int]xpu.DeviceHealth
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats, statsErr = col.CollectStats(ctx, devIDs)
	}()
	if withHealth {
		wg.Add(1)
		go func() {
			defer wg.Done()
			health, _ = col.CollectHealth(ctx)
		}()
	}
	wg.Wait()
	return stats, statsErr, health
}

// healthEvery is how many polls apart the health check runs.
const healthEvery = 10

// fetchTimeout bounds one collection. It is tied to the refresh interval so a
// slow collection cannot outlive several ticks, with a floor that covers the
// per-command timeout inside the collector.
func (m *Model) fetchTimeout() time.Duration {
	d := 2 * m.refreshInterval
	if d < 4*time.Second {
		d = 4 * time.Second
	}
	return d
}

func (m *Model) discoverAndFetchCmd() tea.Cmd {
	col := m.currentCol
	timeout := m.fetchTimeout()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		devices, err := col.Discover(ctx)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("GPU discovery error: %w", err), Discovery: true}
		}

		devIDs := make([]int, len(devices))
		for i, d := range devices {
			devIDs[i] = d.DeviceID
		}

		stats, statsErr, health := fetchAll(ctx, col, devIDs, true)
		// Topology does not change while the machine is up; read it once.
		topo, _ := col.CollectTopology(ctx)

		return DiscoveredMsg{Devices: devices, Stats: stats, Health: health, Topology: topo, Err: statsErr}
	}
}

func (m *Model) fetchTelemetryCmd() tea.Cmd {
	col := m.currentCol
	timeout := m.fetchTimeout()
	devIDs := make([]int, len(m.devices))
	for i, d := range m.devices {
		devIDs[i] = d.DeviceID
	}
	withHealth := m.ticksSinceHealth >= healthEvery
	if withHealth {
		m.ticksSinceHealth = 0
	} else {
		m.ticksSinceHealth++
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		// The collectors read sysfs and /proc directly, and a read that blocks
		// in the kernel ignores the context. Waiting on it here would have left
		// inFlight set for good and the display frozen on its last numbers
		// with no message; instead the tick reports the overrun and the next
		// one tries again (the collector refuses to overlap itself).
		type result struct {
			stats  map[int]*xpu.GPUStats
			err    error
			health map[int]xpu.DeviceHealth
		}
		done := make(chan result, 1)
		go func() {
			stats, statsErr, health := fetchAll(ctx, col, devIDs, withHealth)
			done <- result{stats, statsErr, health}
		}()
		select {
		case r := <-done:
			if len(r.stats) == 0 && r.err != nil {
				return ErrorMsg{Err: r.err}
			}
			return DataLoadedMsg{Stats: r.stats, Health: r.health, Err: r.err}
		case <-ctx.Done():
			return ErrorMsg{Err: fmt.Errorf("collection still running after %s", timeout)}
		}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	logMsg(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.viewport.SetWidth(msg.Width)

	case TickMsg:
		if msg.Gen != m.tickGen {
			break // a chain superseded by an interval change; let it die
		}
		if !m.isPaused && !m.inFlight {
			m.inFlight = true
			if len(m.devices) == 0 {
				cmds = append(cmds, m.discoverAndFetchCmd())
			} else {
				cmds = append(cmds, m.fetchTelemetryCmd())
			}
		}
		cmds = append(cmds, m.tickCmd())

	case StatusClearMsg:
		if msg.Seq == m.statusSeq {
			m.statusMsg = ""
		}

	case ErrorMsg:
		m.inFlight = false
		cmds = append(cmds, m.setStatus(fmt.Sprintf("Error: %v", msg.Err), 8*time.Second))
		if msg.Discovery {
			// Fall back to re-discovery on the next tick rather than fetching
			// against a device list that may belong to the previous collector.
			m.devices = nil
		}

	case DiscoveredMsg:
		m.inFlight = false
		m.devices = msg.Devices
		m.topology = msg.Topology
		if msg.Err != nil {
			cmds = append(cmds, m.setStatus(fmt.Sprintf("Error: %v", msg.Err), 8*time.Second))
		}

		m.gpus = make([]*xpu.GPUCombined, 0, len(m.devices))
		for _, dev := range m.devices {
			comb, exists := m.gpuMap[dev.DeviceID]
			if !exists {
				comb = &xpu.GPUCombined{
					Info:    dev,
					History: xpu.NewMetricHistory(xpu.DefaultHistoryCapacity),
				}
				m.gpuMap[dev.DeviceID] = comb
			} else {
				comb.Info = dev
			}
			if s, ok := msg.Stats[dev.DeviceID]; ok && s != nil {
				comb.Stats = *s
				comb.History.Record(*s)
			}
			if h, ok := msg.Health[dev.DeviceID]; ok {
				comb.Health = h
			}
			m.gpus = append(m.gpus, comb)
		}
		// Fresh only if something actually arrived; discovery can succeed while
		// every stats call fails, and stamping that as fresh hid it behind a
		// LIVE badge.
		if len(msg.Stats) > 0 {
			m.lastDataAt = time.Now()
		}
		if m.selectedGPU >= len(m.gpus) {
			m.selectedGPU = 0
			if len(m.gpus) > 1 {
				cmds = append(cmds, m.setStatus("Requested card does not exist; showing card 0", 5*time.Second))
			}
		}

	case DataLoadedMsg:
		m.inFlight = false
		if len(msg.Stats) > 0 {
			m.lastDataAt = time.Now()
		}
		if msg.Err != nil {
			cmds = append(cmds, m.setStatus(fmt.Sprintf("Partial: %v", msg.Err), 8*time.Second))
		}
		for _, comb := range m.gpus {
			devID := comb.Info.DeviceID
			if s, ok := msg.Stats[devID]; ok && s != nil {
				comb.Stats = *s
				comb.History.Record(*s)
			}
			if h, ok := msg.Health[devID]; ok {
				comb.Health = h // absent when this poll skipped the health check; keep the last
			}
		}

	case tea.KeyPressMsg:
		if debugKeys {
			m.lastKey = fmt.Sprintf("key: code=%d str=%q text=%q mod=%d",
				msg.Code, msg.String(), msg.Text, msg.Mod)
		}

		// When filter typing mode is active in Processes tab
		if m.filterMode {
			if isEscape(msg) {
				m.filterMode = false
				return m, nil
			}
			switch {
			case msg.String() == "ctrl+c":
				// Raw mode: no SIGINT, so this is the only way out.
				return m, tea.Quit
			case msg.Code == tea.KeyEnter:
				m.filterMode = false
			case msg.Code == tea.KeyBackspace:
				if len(m.filterText) > 0 {
					_, size := utf8.DecodeLastRuneInString(m.filterText)
					m.filterText = m.filterText[:len(m.filterText)-size]
				}
			case msg.Text != "" && msg.Mod&^tea.ModShift == 0:
				m.filterText += msg.Text
			}
			return m, nil
		}

		// Help modal. It renders over the whole frame (see View), so it must
		// consume every key: keys used to fall through to the global switch and
		// fire invisibly behind it -- you could toggle demo mode, pause polling,
		// or silently enter the process filter with no visible prompt.
		if m.showHelp {
			if isEscape(msg) {
				m.showHelp = false
				return m, nil
			}
			switch msg.String() {
			case "?", "f1", "q", "enter":
				m.showHelp = false
			case "i":
				m.showHelp, m.showLegend = false, true
				m.viewport.GotoTop()
			case "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}

		// The legend is a page, so scrolling still has to work on it. Only the
		// keys that leave it are intercepted. Escape is matched on the key type
		// as well as its name, so a terminal that reports it differently still
		// gets out.
		if m.showLegend {
			if isEscape(msg) {
				m.showLegend = false
				m.viewport.GotoTop()
				return m, nil
			}
			switch msg.String() {
			case "i", "q", "enter", "?", "f1":
				m.showLegend = false
				m.viewport.GotoTop()
				if msg.String() == "?" || msg.String() == "f1" {
					m.showHelp = true
				}
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			// Reading keys pass through: scrolling and tab switching still
			// work on the page. Everything else is swallowed, the same way the
			// help overlay does -- pressing space to page down used to pause
			// polling behind a screen you could not see it on.
			case "up", "down", "j", "k", "pgup", "pgdown", "ctrl+u", "ctrl+d", "ctrl+f", "ctrl+b",
				"home", "end", "g", "G", "1", "2", "3", "tab", "shift+tab", "left", "right", "h", "l":
			default:
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "?", "f1":
			m.showHelp = !m.showHelp
			m.showLegend = false

		case "i":
			// The strips are necessarily terse; this is the key to them.
			m.showLegend = !m.showLegend
			m.showHelp = false

		case "1":
			m.gotoTab(components.TabOverview)
		case "2":
			m.gotoTab(components.TabDetail)
		case "3":
			m.gotoTab(components.TabTrends)

		case "tab", "right", "l":
			m.gotoTab((m.activeTab + 1) % components.TabCount)
			cmds = append(cmds, m.setStatus(fmt.Sprintf("View: %s", components.TabNames[m.activeTab]), 2*time.Second))

		case "shift+tab", "left", "h":
			if m.activeTab == 0 {
				m.gotoTab(components.TabCount - 1)
			} else {
				m.gotoTab(m.activeTab - 1)
			}
			cmds = append(cmds, m.setStatus(fmt.Sprintf("View: %s", components.TabNames[m.activeTab]), 2*time.Second))

		// Scrolling owns the arrows and hjkl's vertical pair, which is what they
		// mean in every comparable tool. They used to cycle GPU focus -- an
		// operation that did nothing on three of the five tabs -- while
		// scrolling was exiled to PgUp/PgDn.
		case "down", "j":
			m.viewport.ScrollDown(1)

		case "up", "k":
			m.viewport.ScrollUp(1)

		case "pgdown", "ctrl+f":
			m.viewport.PageDown()

		case "pgup", "ctrl+b":
			m.viewport.PageUp()

		case "ctrl+d":
			m.viewport.HalfPageDown()

		case "ctrl+u":
			m.viewport.HalfPageUp()

		case "home", "g":
			m.viewport.GotoTop()

		case "end", "G":
			m.viewport.GotoBottom()

		case "]":
			m.cycleCard(1)

		case "[":
			m.cycleCard(-1)

		case "b":
			if m.activeTab == components.TabDetail {
				cmds = append(cmds, m.setStatus("Detail shows one card at a time; use [ and ]", 2*time.Second))
			} else if len(m.gpus) > 1 {
				m.selectedGPU = -1
				cmds = append(cmds, m.setStatus("Showing all cards", 2*time.Second))
			}

		case " ":
			m.isPaused = !m.isPaused
			if m.isPaused {
				m.statusMsg = "Polling paused"
			} else {
				m.statusMsg = "Polling resumed"
			}
			cmds = append(cmds, m.clearStatusCmd(2*time.Second))

		case "t":
			t := styles.NextTheme()
			msg := "Theme: " + t.Name
			if err := saveTheme(t.Name); err != nil {
				msg += " (not saved: " + err.Error() + ")"
			}
			cmds = append(cmds, m.setStatus(msg, 2*time.Second))

		case "r":
			m.statusMsg = "Refreshing telemetry..."
			cmds = append(cmds, m.fetchTelemetryCmd(), m.clearStatusCmd(2*time.Second))

		// Bidirectional, and it steps rather than cycling through a fixed list,
		// so a custom --interval is no longer discarded on the first keypress.
		case "+", "=":
			m.setInterval(m.refreshInterval - time.Second)
			cmds = append(cmds, m.setStatus(fmt.Sprintf("Refresh interval %s", m.refreshInterval), 2*time.Second), m.tickCmd())

		case "-", "_":
			m.setInterval(m.refreshInterval + time.Second)
			cmds = append(cmds, m.setStatus(fmt.Sprintf("Refresh interval %s", m.refreshInterval), 2*time.Second), m.tickCmd())

		case "D":
			m.isDemo = !m.isDemo
			if m.isDemo {
				m.currentCol = m.mockCollector
			} else {
				m.currentCol = m.cliCollector
			}
			// Both collectors number their cards 0 and 1, so without a reset the
			// live history was spliced onto the simulated one as a single curve,
			// and a failed live discovery left the demo devices in place forever.
			m.devices, m.gpus, m.gpuMap = nil, nil, make(map[int]*xpu.GPUCombined)
			m.lastDataAt = time.Time{}
			m.inFlight = true
			mode := "LIVE hardware"
			if m.isDemo {
				mode = "DEMO simulation"
			}
			cmds = append(cmds, m.discoverAndFetchCmd(), m.setStatus("Switched to "+mode+" mode", 3*time.Second))

		case "/":
			// Filtering applies to the workload list, which lives on Overview.
			if m.activeTab == components.TabOverview {
				m.filterMode = true
			} else {
				cmds = append(cmds, m.setStatus("Filter applies to the workload list on Overview [1]", 2*time.Second))
			}

		case "esc":
			if m.filterText != "" {
				m.filterText = ""
				cmds = append(cmds, m.setStatus("Filter cleared", 2*time.Second))
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the full terminal UI.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.BackgroundColor, v.ForegroundColor = styles.TerminalColors()
	return v
}

func (m *Model) render() string {
	width := m.termWidth
	height := m.termHeight
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	// 1. Header
	header := components.RenderHeader(
		m.activeTab,
		m.staleFor(time.Now()),
		m.isPaused,
		m.isDemo,
		m.refreshInterval,
		len(m.gpus),
		width,
		m.ramSummary(),
	)

	// 2. Active Tab Content.
	//
	// The views used to be handed the full terminal height, header and footer
	// included, so they sized charts against space they did not have. Give them
	// the rows that actually remain.
	var content string
	var selectedG *xpu.GPUCombined
	if len(m.gpus) > 0 {
		targetIdx := m.selectedGPU
		if targetIdx < 0 || targetIdx >= len(m.gpus) {
			targetIdx = 0
		}
		selectedG = m.gpus[targetIdx]
	}

	// Footer text is needed before the content so its height can be subtracted.
	status := m.statusMsg
	if debugKeys && m.lastKey != "" {
		status = m.lastKey + "   log: " + debugLogPath
	}
	if m.filterMode {
		status = fmt.Sprintf("Type to filter processes: %s █ (Esc/Enter to finish)", m.filterText)
	}
	footerProbe := components.RenderFooter(width, m.selectedGPU, len(m.gpus), status, " 100% ")

	contentH := height - lipgloss.Height(header) - lipgloss.Height(footerProbe)
	if contentH < 3 {
		contentH = 3
	}

	// The legend is a scrollable page, not an overlay: it is long, and as a
	// centred box it had to truncate or drop entries to fit the terminal.
	if m.showLegend {
		content = views.RenderLegend(width, contentH)
		m.viewport.SetWidth(width)
		m.viewport.SetHeight(contentH)
		m.viewport.SetContent(content)
		m.viewport.SetYOffset(m.viewport.YOffset())
		footer := components.RenderFooter(width, m.selectedGPU, len(m.gpus), status, m.scrollInfo())
		return clampFrame(lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View(), footer), height)
	}

	switch m.activeTab {
	case components.TabOverview:
		content = views.RenderOverview(m.gpus, m.selectedGPU, m.filterText, width)
	case components.TabDetail:
		content = views.RenderDetailAmong(selectedG, m.gpus, m.topology, width, contentH)
	case components.TabTrends:
		content = views.RenderTrends(m.gpus, m.selectedGPU, width, contentH)
	}

	// Scroll whatever is taller than the space available, rather than cutting it
	// off at the bottom with no way to reach the rest.
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(contentH)
	m.viewport.SetContent(content)
	// SetContent clamps only against len(lines)-1, not against the maximum
	// offset for the current height, so switching from a tall view to a short
	// one left the offset stranded and rendered a screen of blank rows.
	m.viewport.SetYOffset(m.viewport.YOffset())
	content = m.viewport.View()

	// 3. Footer
	footer := components.RenderFooter(width, m.selectedGPU, len(m.gpus), status, m.scrollInfo())

	// Combine vertical layout
	mainLayout := lipgloss.JoinVertical(lipgloss.Left, header, content, footer)

	// Backstop only. The viewport already clips the content to contentH, so this
	// should never fire; it stays so a miscount in the header or footer cannot
	// push the terminal into scrollback.
	mainLayout = clampFrame(mainLayout, height)

	// Overlay Help Modal if active. Clamped like any other frame: this path
	// returns directly rather than going through the viewport, so without the
	// clamp an oversized modal pushes the terminal into scrollback.
	if m.showHelp {
		modal := views.RenderHelp(width, height)
		return clampFrame(lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal), height)
	}

	return mainLayout
}

// scrollInfo describes the reader's position in a scrollable view, and is empty
// when everything already fits.
func (m *Model) scrollInfo() string {
	total := m.viewport.TotalLineCount()
	if total <= m.viewport.Height() || m.viewport.Height() <= 0 {
		return ""
	}
	hidden := total - m.viewport.Height()

	switch {
	case m.viewport.AtTop():
		return fmt.Sprintf("↓ %d more (PgDn)", hidden)
	case m.viewport.AtBottom():
		return "↑ end (PgUp)"
	default:
		return fmt.Sprintf("%d%% ↕ (PgUp/PgDn)", int(m.viewport.ScrollPercent()*100))
	}
}

// gotoTab switches tabs and returns to the top of the new view. Without the
// reset, the scroll offset carried over from the previous tab, so arriving at a
// shorter view landed the reader in the middle of it -- or past its end.
func (m *Model) gotoTab(tab components.TabType) {
	if m.activeTab != tab {
		m.viewport.GotoTop()
	}
	m.activeTab = tab
}

// setInterval clamps the refresh rate and starts a new tick chain. Bumping the
// generation retires the timer already in flight -- without that, a change
// took effect only when the old timer fired, up to 30 s later, and re-arming
// early would have left two chains polling.
func (m *Model) setInterval(d time.Duration) {
	const lo, hi = 1 * time.Second, 30 * time.Second
	if d < lo {
		d = lo
	}
	if d > hi {
		d = hi
	}
	m.refreshInterval = d
	m.tickGen++
}

// clampFrame keeps a rendered frame within the terminal so it cannot push the
// display into scrollback.
func clampFrame(frame string, height int) string {
	lines := strings.Split(frame, "\n")
	if height > 0 && len(lines) > height {
		return strings.Join(lines[:height], "\n")
	}
	return frame
}

// cycleCard moves the focused card, skipping the "Both" state on views that
// cannot show both.
//
// Detail renders one card and clamps "Both" to card 0, so leaving that state in
// the cycle meant two of every three presses produced no visible change: -1 and
// 0 both display card 0.
func (m *Model) cycleCard(delta int) {
	n := len(m.gpus)
	if n == 0 {
		return
	}
	if n == 1 {
		m.selectedGPU = 0
		return
	}

	if m.activeTab == components.TabDetail {
		cur := m.selectedGPU
		if cur < 0 {
			cur = 0
		}
		m.selectedGPU = ((cur+delta)%n + n) % n
	} else {
		// Overview and Trends can show every card at once, so "Both" (-1) is a
		// real state and stays in the cycle.
		states := n + 1
		cur := m.selectedGPU + 1 // map -1..n-1 onto 0..n
		next := ((cur+delta)%states + states) % states
		m.selectedGPU = next - 1
	}

	if m.selectedGPU < 0 {
		m.statusMsg = "Showing all cards"
	} else {
		m.statusMsg = fmt.Sprintf("Card %d (%s)", m.selectedGPU, m.gpus[m.selectedGPU].Info.DeviceName)
	}
}

// isEscape reports whether a key should be treated as Escape. Inside an
// overlay there are no Alt bindings, so an Alt-modified key is an Escape that
// was coalesced with the next byte.
func isEscape(msg tea.KeyPressMsg) bool {
	if msg.Code == tea.KeyEscape || msg.Mod.Contains(tea.ModAlt) {
		return true
	}
	switch msg.String() {
	case "esc", "ctrl+[":
		return true
	}
	return false
}

// debugKeys echoes every key the terminal sends into the status line, and logs
// every message to debugLogPath.
var debugKeys = debugEnabled(os.Getenv("XPU_TUI_DEBUG_KEYS"))

// debugEnabled treats "0" and "false" as off; "!= \"\"" made both turn it on.
func debugEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// debugLogPath is where the message log goes when debugging is on; a private
// file, since a fixed name in /tmp could be pre-created as a symlink by another
// user and truncated through.
var debugLogPath string

var (
	debugLogOnce sync.Once
	debugLogFile *os.File
)

// logMsg records a message and its concrete type.
//
// The type matters: bubbletea reports input it cannot classify as an internal
// message that is not a KeyMsg, so a key can reach the program and still never
// appear in any KeyMsg handler.
func logMsg(msg tea.Msg) {
	if !debugKeys {
		return
	}
	switch msg.(type) {
	case TickMsg, DataLoadedMsg:
		return // periodic noise, not keys
	}

	debugLogOnce.Do(func() {
		f, err := os.CreateTemp("", "xpu-smi-tui-keys-*.log")
		if err == nil {
			debugLogFile = f
			debugLogPath = f.Name()
			fmt.Fprintf(f, "# xpu-smi-tui message log, started %s\n", time.Now().Format(time.RFC3339))
		}
	})
	if debugLogFile == nil {
		return
	}

	line := fmt.Sprintf("%-28T %+v", msg, msg)
	if k, ok := msg.(tea.KeyPressMsg); ok {
		line += fmt.Sprintf("   [code=%d mod=%d text=%q str=%q]", k.Code, k.Mod, k.Text, k.String())
	}
	fmt.Fprintln(debugLogFile, line) // no fsync per message; it stalled the event loop
}

// staleFor reports how long the displayed telemetry has gone without a refresh,
// or zero when it is current. A few missed intervals are tolerated before it
// counts, since a single slow xpu-smi call is not worth alarming anyone over.
func (m *Model) staleFor(now time.Time) time.Duration {
	if m.isPaused || m.lastDataAt.IsZero() {
		return 0
	}
	age := now.Sub(m.lastDataAt)
	if age < 3*m.refreshInterval {
		return 0
	}
	return age.Truncate(time.Second)
}

// ramSummary is the host RAM figure for the header: "RAM 7/123G". Context
// for mmap'd model files and for cards evicting to host memory; absent in
// demo mode and when /proc/meminfo cannot be read.
func (m *Model) ramSummary() string {
	if m.isDemo {
		return ""
	}
	mem, ok := xpu.ReadSystemMemory()
	if !ok {
		return ""
	}
	const gib = 1 << 30
	return fmt.Sprintf("RAM %.0f/%.0fG", float64(mem.Used())/gib, float64(mem.TotalBytes)/gib)
}
