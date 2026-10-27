package main

import (
	"context"
	"encoding/json/v2"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"
)

type Client interface {
	Connect() tea.Cmd
	SendChat(text string) tea.Cmd
	SendTyping(isTyping bool) tea.Cmd
	NextEvent() tea.Cmd
	Close()
}

type wsClient struct {
	url    string
	conn   *websocket.Conn
	events chan tea.Msg
}

func NewWSClient(url string) Client {
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
			if err := json.Unmarshal(resp.Payload, &msg); err == nil {
				c.events <- msg
			} else {
				c.events <- errMsg(err)
			}
		case typingAction:
			var msg typingMsg
			if err := json.Unmarshal(resp.Payload, &msg); err == nil {
				c.events <- msg
			} else {
				c.events <- errMsg(err)
			}
		default:
			c.events <- errMsg(fmt.Errorf("unknown action type %s", resp.Type))
		}
	}
}

func (c *wsClient) NextEvent() tea.Cmd {
	return func() tea.Msg { return <-c.events }
}

func (c *wsClient) SendChat(text string) tea.Cmd {
	return func() tea.Msg {
		if c.conn == nil {
			return errMsg(fmt.Errorf("websocket not connected"))
		}
		payload := struct{ Message string `json:"message"` }{Message: text}
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
		payload := struct{ IsTyping bool `json:"is_typing"` }{IsTyping: isTyping}
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
