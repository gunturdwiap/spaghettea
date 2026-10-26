package main

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/coder/websocket"
)

const typingDebounceDuration = 2 * time.Second

type (
	errMsg  error
	connMsg *websocket.Conn
	chatMsg struct {
		Sender  string `json:"sender"`
		Message string `json:"message"`
	}
	typingMsg struct {
		Sender   string `json:"sender"`
		IsTyping bool   `json:"is_typing"`
	}
	typingTimeoutMsg int
)

type wsClient struct {
	url    string
	conn   *websocket.Conn
	events chan tea.Msg
}

func NewWSClient(url string) *wsClient {
	return &wsClient{
		url:    url,
		events: make(chan tea.Msg, 100),
	}
}

func (c *wsClient) Connect() tea.Cmd {
	return func() tea.Msg {
		conn, _, err := websocket.Dial(context.Background(), c.url, nil)
		if err != nil {
			return errMsg(err)
		}
		c.conn = conn
		go c.readLoop(conn)
		return connMsg(conn)
	}
}

func (c *wsClient) readLoop(conn *websocket.Conn) {
	for {
		_, b, err := conn.Read(context.Background())
		if err != nil {
			c.events <- errMsg(err)
			break
		}

		var resp action
		if err := json.Unmarshal(b, &resp); err != nil {
			c.events <- errMsg(err)
			continue
		}

		switch resp.Type {
		case chatAction:
			var msg chatMsg
			if err := json.Unmarshal(resp.Payload, &msg); err != nil {
				c.events <- errMsg(err)
				continue
			}
			c.events <- msg

		case typingAction:
			var msg typingMsg
			if err := json.Unmarshal(resp.Payload, &msg); err != nil {
				c.events <- errMsg(err)
				continue
			}
			c.events <- msg

		default:
			c.events <- errMsg(fmt.Errorf("unknown action type %s", resp.Type))
		}
	}
}

func (c *wsClient) NextEvent() tea.Cmd {
	return func() tea.Msg {
		return <-c.events
	}
}

func (c *wsClient) SendChat(text string) tea.Cmd {
	return func() tea.Msg {
		if c.conn == nil {
			return errMsg(fmt.Errorf("websocket not connected"))
		}

		payload := struct {
			Message string `json:"message"`
		}{Message: text}

		b, err := marshalAction(chatAction, payload)
		if err != nil {
			return errMsg(err)
		}

		if err := c.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
			return errMsg(err)
		}

		return nil
	}
}

func (c *wsClient) SendTyping(isTyping bool) tea.Cmd {
	return func() tea.Msg {
		if c.conn == nil {
			return nil
		}

		payload := struct {
			IsTyping bool `json:"is_typing"`
		}{IsTyping: isTyping}

		b, err := marshalAction(typingAction, payload)
		if err != nil {
			return errMsg(err)
		}

		if err := c.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
			return errMsg(err)
		}
		return nil
	}
}

func (c *wsClient) Close() {
	if c.conn != nil {
		c.conn.CloseNow()
	}
}

type Styles struct {
	Dim    lipgloss.Style
	Prompt lipgloss.Style
	User   lipgloss.Style
	Agent  lipgloss.Style
}

func NewStyles() Styles {
	return Styles{
		Dim:    lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Prompt: lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true),
		User:   lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Agent:  lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
	}
}

type model struct {
	viewport     viewport.Model
	messages     []string
	textarea     textarea.Model
	styles       Styles
	err          error
	quitting     bool
	client       *wsClient
	isTyping     bool
	typingUser   string
	typingTag    int  // Tracks typing debounce session
	remoteTyping bool // Tracks if we have notified the server we are typing
}

func initialModel(client *wsClient) model {
	styles := NewStyles()

	ta := textarea.New()
	ta.Placeholder = ""
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.Prompt = "❯ "
	ta.Placeholder = "Type a message"
	ta.CharLimit = 1000
	ta.SetWidth(80)
	ta.SetHeight(2)
	ta.ShowLineNumbers = false

	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Focused.Base = lipgloss.NewStyle().Padding(0)
	ta.SetStyles(s)
	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(15))
	vp.SetContent(styles.Dim.Render("Connected. Type a message..."))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	return model{
		textarea: ta,
		messages: []string{},
		viewport: vp,
		styles:   styles,
		client:   client,
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
		vpHeight := msg.Height - 4
		if vpHeight < 2 {
			vpHeight = 2
		}

		m.viewport.SetWidth(msg.Width)
		m.viewport.SetHeight(vpHeight)
		m.textarea.SetWidth(msg.Width)

		if len(m.messages) > 0 {
			m.viewport.SetContent(strings.Join(m.messages, "\n"))
		}
		m.viewport.GotoBottom()

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit

		case "ctrl+l":
			m.messages = []string{}
			m.viewport.SetContent("")

		case "enter":
			text := m.textarea.Value()
			if strings.TrimSpace(text) == "" {
				return m, nil
			}
			m.textarea.Reset()
			m.typingTag++
			m.remoteTyping = false
			return m, tea.Batch(m.client.SendChat(text), m.client.SendTyping(false))

		case "pgup", "ctrl+u":
			m.viewport.ScrollUp(m.viewport.Height() / 2)
			return m, nil

		case "pgdown", "ctrl+d":
			m.viewport.ScrollDown(m.viewport.Height() / 2)
			return m, nil

		case "up":
			if m.textarea.Line() == 0 {
				m.viewport.ScrollUp(1)
				return m, nil
			}
		case "down":
			if m.textarea.Line() == m.textarea.LineCount()-1 {
				m.viewport.ScrollDown(1)
				return m, nil
			}
		default:
			// Update textarea with the keystroke
			var taCmd tea.Cmd
			m.textarea, taCmd = m.textarea.Update(msg)

			// Debounce typing indicator
			m.typingTag++
			var sendTypingCmd tea.Cmd
			if !m.remoteTyping {
				m.remoteTyping = true
				sendTypingCmd = m.client.SendTyping(true)
			}

			tickCmd := tea.Tick(typingDebounceDuration, func(_ time.Time) tea.Msg {
				return typingTimeoutMsg(m.typingTag)
			})

			cmd = tea.Batch(taCmd, sendTypingCmd, tickCmd)
		}

	case typingTimeoutMsg:
		// If the tag matches, no new keys were pressed during the debounce duration
		if int(msg) == m.typingTag {
			m.remoteTyping = false
			return m, m.client.SendTyping(false)
		}

	case cursor.BlinkMsg:
		var taCmd tea.Cmd
		m.textarea, taCmd = m.textarea.Update(msg)
		cmd = taCmd

	case connMsg:
		// TODO

	case chatMsg:
		line := fmt.Sprintf("%s: %s", m.styles.Agent.Render(msg.Sender), m.styles.User.Render(msg.Message))
		m.messages = append(m.messages, line)
		m.viewport.SetContent(strings.Join(m.messages, "\n"))
		m.viewport.GotoBottom()

	case typingMsg:
		m.isTyping = msg.IsTyping
		m.typingUser = msg.Sender

	case errMsg:
		m.err = msg
		m.client.Close()
		m.quitting = true
		return m, tea.Quit
	}

	if !m.quitting {
		cmd = tea.Batch(cmd, m.client.NextEvent())
	}

	return m, cmd
}

func (m model) View() tea.View {
	divider := m.styles.Dim.Render(strings.Repeat("─", m.viewport.Width()))

	// Always reserve 1 line for the typing status so the layout never shifts
	typingText := " "
	if m.isTyping {
		typingText = fmt.Sprintf("%s is typing...", m.typingUser)
	}
	typingLine := m.styles.Dim.Italic(true).Render(typingText)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.viewport.View(),
		typingLine,
		divider,
		m.textarea.View(),
	)
	v := tea.NewView(content)
	v.AltScreen = true
	if c := m.textarea.Cursor(); c != nil {
		c.Y += lipgloss.Height(content) - lipgloss.Height(m.textarea.View())
		v.Cursor = c
	}

	return v
}

func main() {
	f, err := tea.LogToFile("debug.log", "debug")
	if err != nil {
		fmt.Println("fatal:", err)
		os.Exit(1)
	}
	defer f.Close()

	logger := slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	client := NewWSClient("ws://localhost:8080/room/general/ws")
	defer client.Close()

	p := tea.NewProgram(initialModel(client))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Oof: %v\n", err)
	}
}
