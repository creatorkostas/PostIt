package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type WSMessage struct {
	Type      string    `json:"type"` // "sent", "received", "error"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type WSClient struct {
	Conn     *websocket.Conn
	Messages []WSMessage
	mu       sync.Mutex
	connMu   sync.Mutex
}

func NewWSClient() *WSClient {
	return &WSClient{
		Messages: make([]WSMessage, 0),
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Same-origin or no Origin header (e.g., from a tool)
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		
		uHost := u.Hostname()
		return uHost == "localhost" || uHost == "127.0.0.1" || uHost == host
	},
}

func (c *WSClient) Connect(url string) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}
	
	c.Conn = conn
	
	// Start listener
	go func() {
		currentConn := conn
		defer func() {
			c.connMu.Lock()
			currentConn.Close()
			if c.Conn == currentConn {
				c.Conn = nil
			}
			c.connMu.Unlock()
		}()
		for {
			_, message, err := currentConn.ReadMessage()
			if err != nil {
				c.addMessage("error", err.Error())
				break
			}
			c.addMessage("received", string(message))
		}
	}()
	
	return nil
}

func (c *WSClient) Send(message string) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.Conn == nil {
		return fmt.Errorf("not connected")
	}
	err := c.Conn.WriteMessage(websocket.TextMessage, []byte(message))
	if err != nil {
		c.addMessage("error", err.Error())
		return err
	}
	c.addMessage("sent", message)
	return nil
}

func (c *WSClient) addMessage(msgType, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Messages = append(c.Messages, WSMessage{
		Type:      msgType,
		Content:   content,
		Timestamp: time.Now(),
	})
}

func (c *WSClient) GetMessages() []WSMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Messages
}

func (c *WSClient) Close() {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.Conn != nil {
		c.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.Conn.Close()
		c.Conn = nil
	}
}
