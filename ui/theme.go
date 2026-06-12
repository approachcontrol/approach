package ui

import "github.com/charmbracelet/lipgloss"

type themePalette struct {
	bg            lipgloss.Color
	fg            lipgloss.Color
	fgStrong      lipgloss.Color
	muted         lipgloss.Color
	borderMuted   lipgloss.Color
	focus         lipgloss.Color
	info          lipgloss.Color
	success       lipgloss.Color
	successBright lipgloss.Color
	warning       lipgloss.Color
	danger        lipgloss.Color
	special       lipgloss.Color
	selectionBg   lipgloss.Color
	selectionFg   lipgloss.Color
}

type themeStyles struct {
	palette           themePalette
	repo              lipgloss.Style
	selected          lipgloss.Style
	placeholder       lipgloss.Style
	status            lipgloss.Style
	branch            lipgloss.Style
	clean             lipgloss.Style
	commit            lipgloss.Style
	activeMode        lipgloss.Style
	inactiveMode      lipgloss.Style
	shortcutTitle     lipgloss.Style
	shortcutMode      lipgloss.Style
	shortcutGroup     lipgloss.Style
	shortcutKey       lipgloss.Style
	shortcutText      lipgloss.Style
	shortcutSuccess   lipgloss.Style
	stashDate         lipgloss.Style
	stashMsg          lipgloss.Style
	stashSelected     lipgloss.Style
	branchSelected    lipgloss.Style
	root              lipgloss.Style
	locked            lipgloss.Style
	noUpstream        lipgloss.Style
	aheadBehind       lipgloss.Style
	merged            lipgloss.Style
	dirty             lipgloss.Style
	diffAdd           lipgloss.Style
	diffDel           lipgloss.Style
	diffHeader        lipgloss.Style
	flowTerminal      lipgloss.Style
	activeBorder      lipgloss.Color
	inactiveBorder    lipgloss.Color
	destructiveBorder lipgloss.Color
}

var clearDarkPalette = themePalette{
	bg:            lipgloss.Color("#191D27"),
	fg:            lipgloss.Color("#E0E0E0"),
	fgStrong:      lipgloss.Color("#E5EFF5"),
	muted:         lipgloss.Color("#465C6D"),
	borderMuted:   lipgloss.Color("#35424C"),
	focus:         lipgloss.Color("#67B5ED"),
	info:          lipgloss.Color("#84DDE0"),
	success:       lipgloss.Color("#79BE7E"),
	successBright: lipgloss.Color("#35F06D"),
	warning:       lipgloss.Color("#E5C872"),
	danger:        lipgloss.Color("#DF6C5A"),
	special:       lipgloss.Color("#D389E5"),
	selectionBg:   lipgloss.Color("#273D4C"),
	selectionFg:   lipgloss.Color("#E5EFF5"),
}

var clearDarkTheme = newThemeStyles(clearDarkPalette)

func newThemeStyles(p themePalette) themeStyles {
	selected := lipgloss.NewStyle().
		Foreground(p.selectionFg).
		Background(p.selectionBg).
		Bold(true)
	return themeStyles{
		palette:           p,
		repo:              lipgloss.NewStyle().Foreground(p.success),
		selected:          selected,
		placeholder:       lipgloss.NewStyle().Foreground(p.muted).Italic(true),
		status:            lipgloss.NewStyle().Foreground(p.muted),
		branch:            lipgloss.NewStyle().Foreground(p.fgStrong).Bold(true),
		clean:             lipgloss.NewStyle().Foreground(p.success),
		commit:            lipgloss.NewStyle().Foreground(p.muted),
		activeMode:        lipgloss.NewStyle().Foreground(p.focus).Bold(true),
		inactiveMode:      lipgloss.NewStyle().Foreground(p.muted),
		shortcutTitle:     lipgloss.NewStyle().Foreground(p.fgStrong).Bold(true),
		shortcutMode:      lipgloss.NewStyle().Foreground(p.focus).Bold(true),
		shortcutGroup:     lipgloss.NewStyle().Foreground(p.info).Bold(true),
		shortcutKey:       lipgloss.NewStyle().Foreground(p.focus).Bold(true),
		shortcutText:      lipgloss.NewStyle().Foreground(p.fg),
		shortcutSuccess:   lipgloss.NewStyle().Foreground(p.successBright).Bold(true),
		stashDate:         lipgloss.NewStyle().Foreground(p.muted),
		stashMsg:          lipgloss.NewStyle().Foreground(p.fgStrong),
		stashSelected:     selected,
		branchSelected:    selected,
		root:              lipgloss.NewStyle().Foreground(p.focus),
		locked:            lipgloss.NewStyle().Foreground(p.info),
		noUpstream:        lipgloss.NewStyle().Foreground(p.special),
		aheadBehind:       lipgloss.NewStyle().Foreground(p.warning),
		merged:            lipgloss.NewStyle().Foreground(p.info),
		dirty:             lipgloss.NewStyle().Foreground(p.danger),
		diffAdd:           lipgloss.NewStyle().Foreground(p.success),
		diffDel:           lipgloss.NewStyle().Foreground(p.danger),
		diffHeader:        lipgloss.NewStyle().Foreground(p.info),
		flowTerminal:      lipgloss.NewStyle().Foreground(p.success),
		activeBorder:      p.focus,
		inactiveBorder:    p.borderMuted,
		destructiveBorder: p.danger,
	}
}

var (
	repoStyle            = clearDarkTheme.repo
	selectedStyle        = clearDarkTheme.selected
	placeholderStyle     = clearDarkTheme.placeholder
	statusStyle          = clearDarkTheme.status
	branchStyle          = clearDarkTheme.branch
	cleanStyle           = clearDarkTheme.clean
	commitStyle          = clearDarkTheme.commit
	activeModeStyle      = clearDarkTheme.activeMode
	inactiveModeStyle    = clearDarkTheme.inactiveMode
	shortcutTitleStyle   = clearDarkTheme.shortcutTitle
	shortcutModeStyle    = clearDarkTheme.shortcutMode
	shortcutGroupStyle   = clearDarkTheme.shortcutGroup
	shortcutKeyStyle     = clearDarkTheme.shortcutKey
	shortcutTextStyle    = clearDarkTheme.shortcutText
	shortcutSuccessStyle = clearDarkTheme.shortcutSuccess
	stashDateStyle       = clearDarkTheme.stashDate
	stashMsgStyle        = clearDarkTheme.stashMsg
	stashSelStyle        = clearDarkTheme.stashSelected
	branchSelStyle       = clearDarkTheme.branchSelected
	rootStyle            = clearDarkTheme.root
	lockedStyle          = clearDarkTheme.locked
	noUpstreamStyle      = clearDarkTheme.noUpstream
	aheadBehindStyle     = clearDarkTheme.aheadBehind
	mergedStyle          = clearDarkTheme.merged
	dirtyRedStyle        = clearDarkTheme.dirty
	diffAddStyle         = clearDarkTheme.diffAdd
	diffDelStyle         = clearDarkTheme.diffDel
	diffHdrStyle         = clearDarkTheme.diffHeader
	flowTerminalStyle    = clearDarkTheme.flowTerminal
)
