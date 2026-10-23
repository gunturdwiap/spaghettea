package main

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/coder/websocket"
)

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

func (c *wsClient) Close() {
	if c.conn != nil {
		c.conn.CloseNow()
	}
}

type model struct {
	viewport    viewport.Model
	messages    []string
	textarea    textarea.Model
	senderStyle lipgloss.Style
	err         error
	quitting    bool
	client      *wsClient
}

func initialModel(client *wsClient) model {
	ta := textarea.New()
	ta.Placeholder = "Send a message..."
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 280
	ta.SetWidth(30)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(s)
	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(viewport.WithWidth(30), viewport.WithHeight(5))
	vp.SetContent(`Welcome to the chat room!
Type a message and press Enter to send.`)
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	return model{
		textarea:    ta,
		messages:    []string{},
		viewport:    vp,
		senderStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		client:      client,
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
		m.viewport.SetWidth(msg.Width)
		m.textarea.SetWidth(msg.Width)
		m.viewport.SetHeight(msg.Height - m.textarea.Height())

		if len(m.messages) > 0 {
			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width()).Render(strings.Join(m.messages, "\n")))
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
			return m, m.client.SendChat(text)

		default:
			var taCmd tea.Cmd
			m.textarea, taCmd = m.textarea.Update(msg)
			cmd = taCmd
		}

	case cursor.BlinkMsg:
		var taCmd tea.Cmd
		m.textarea, taCmd = m.textarea.Update(msg)
		cmd = taCmd

	case connMsg:
		// Connection established

	case chatMsg:
		m.messages = append(m.messages, m.senderStyle.Render(msg.Sender+": ")+msg.Message)
		m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width()).Render(strings.Join(m.messages, "\n")))
		m.viewport.GotoBottom()

	case typingMsg:
		// TODO: handle typing indicator

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
	viewportView := m.viewport.View()
	v := tea.NewView(viewportView + "\n" + m.textarea.View())
	c := m.textarea.Cursor()
	if c != nil {
		c.Y += lipgloss.Height(viewportView)
	}
	v.Cursor = c
	v.AltScreen = true
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
