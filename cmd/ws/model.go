package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const typingDebounceDuration = 2 * time.Second

type SessionState int

const (
	StateActive SessionState = iota
	StateQuitting
)

type (
	errMsg           error
	connMsg          any
	typingTimeoutMsg int

	chatMsg struct {
		Sender  string `json:"sender"`
		Message string `json:"message"`
	}

	typingMsg struct {
		Sender   string `json:"sender"`
		IsTyping bool   `json:"is_typing"`
	}
)

type Styles struct {
	Dim         lipgloss.Style
	Prompt      lipgloss.Style
	MessageText lipgloss.Style
	Sender      lipgloss.Style
}

func NewStyles() Styles {
	return Styles{
		Dim:         lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Prompt:      lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true),
		MessageText: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Sender:      lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
	}
}

type model struct {
	viewport     viewport.Model
	textarea     textarea.Model
	styles       Styles
	client       Client
	sessionState SessionState
	messages     []string
	err          error
	isTyping     bool
	typingUser   string
	typingTag    int
	remoteTyping bool
	width        int
	height       int
}

func InitialModel(client Client) model {
	styles := NewStyles()

	ta := textarea.New()
	ta.Focus()
	ta.Prompt = "❯ "
	ta.Placeholder = "Type a message"
	ta.CharLimit = 1000
	ta.SetWidth(80)
	ta.SetHeight(2)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Focused.Base = lipgloss.NewStyle().Padding(0)
	ta.SetStyles(s)

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(15))
	vp.SetContent(styles.Dim.Render("Connected. Type a message..."))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	return model{
		textarea:     ta,
		viewport:     vp,
		styles:       styles,
		client:       client,
		sessionState: StateActive,
		messages:     []string{},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.client.Connect(),
		m.client.NextEvent(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m, cmd = m.handleWindowResize(msg)
	case tea.KeyPressMsg:
		m, cmd = m.handleKeyPress(msg)
	case cursor.BlinkMsg:
		m, cmd = m.handleCursorBlink(msg)
	case typingTimeoutMsg:
		m, cmd = m.handleTypingTimeout(msg)
	case chatMsg:
		m, cmd = m.handleChatMsg(msg)
	case typingMsg:
		m, cmd = m.handleTypingMsg(msg)
	case connMsg:
		// Connection established hook
	case errMsg:
		return m.handleError(msg)
	}

	if m.sessionState != StateQuitting {
		cmd = tea.Batch(cmd, m.client.NextEvent())
	}

	return m, cmd
}

func (m model) View() tea.View {
	if m.sessionState == StateQuitting {
		return tea.NewView("")
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.buildMainContent(),
		m.buildStatusIndicators(),
		m.buildDivider(),
		m.buildInputArea(),
	)

	v := tea.NewView(content)
	v.AltScreen = true
	if c := m.textarea.Cursor(); c != nil {
		c.Y += lipgloss.Height(content) - lipgloss.Height(m.textarea.View())
		v.Cursor = c
	}

	return v
}

func (m model) buildMainContent() string {
	return m.viewport.View()
}

func (m model) buildStatusIndicators() string {
	typingText := " "
	if m.isTyping {
		typingText = fmt.Sprintf("%s is typing...", m.typingUser)
	}
	return m.styles.Dim.Italic(true).Render(typingText)
}

func (m model) buildDivider() string {
	return m.styles.Dim.Render(strings.Repeat("─", m.viewport.Width()))
}

func (m model) buildInputArea() string {
	return m.textarea.View()
}
