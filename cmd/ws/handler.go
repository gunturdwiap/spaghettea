package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	tea "charm.land/bubbletea/v2"
)

func (m model) handleWindowResize(msg tea.WindowSizeMsg) (model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	vpHeight := max(2, msg.Height-4)

	m.viewport.SetWidth(msg.Width)
	m.viewport.SetHeight(vpHeight)
	m.textarea.SetWidth(msg.Width)

	if len(m.messages) > 0 {
		m.viewport.SetContent(strings.Join(m.messages, "\n"))
	}
	m.viewport.GotoBottom()
	return m, nil
}

func (m model) handleKeyPress(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.sessionState = StateQuitting
		return m, tea.Quit

	case "ctrl+l":
		m.messages = []string{}
		m.viewport.SetContent("")
		return m, nil

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
		}
		return m, nil

	case "down":
		if m.textarea.Line() == m.textarea.LineCount()-1 {
			m.viewport.ScrollDown(1)
		}
		return m, nil

	default:
		var taCmd tea.Cmd
		m.textarea, taCmd = m.textarea.Update(msg)

		m.typingTag++
		var sendTypingCmd tea.Cmd
		if !m.remoteTyping {
			m.remoteTyping = true
			sendTypingCmd = m.client.SendTyping(true)
		}

		tickCmd := tea.Tick(typingDebounceDuration, func(_ time.Time) tea.Msg {
			return typingTimeoutMsg(m.typingTag)
		})

		return m, tea.Batch(taCmd, sendTypingCmd, tickCmd)
	}
}

func (m model) handleCursorBlink(msg cursor.BlinkMsg) (model, tea.Cmd) {
	var taCmd tea.Cmd
	m.textarea, taCmd = m.textarea.Update(msg)
	return m, taCmd
}

func (m model) handleTypingTimeout(msg typingTimeoutMsg) (model, tea.Cmd) {
	if int(msg) == m.typingTag {
		m.remoteTyping = false
		return m, m.client.SendTyping(false)
	}
	return m, nil
}

func (m model) handleChatMsg(msg chatMsg) (model, tea.Cmd) {
	line := fmt.Sprintf("%s: %s", m.styles.Sender.Render(msg.Sender), m.styles.MessageText.Render(msg.Message))
	m.messages = append(m.messages, line)
	m.viewport.SetContent(strings.Join(m.messages, "\n"))
	m.viewport.GotoBottom()
	return m, nil
}

func (m model) handleTypingMsg(msg typingMsg) (model, tea.Cmd) {
	m.isTyping = msg.IsTyping
	m.typingUser = msg.Sender
	return m, nil
}

func (m model) handleError(msg errMsg) (model, tea.Cmd) {
	m.err = msg
	m.client.Close()
	m.sessionState = StateQuitting
	return m, tea.Quit
}
