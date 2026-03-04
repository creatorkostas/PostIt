package api

import (
	"fmt"
	"net/http"
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
}

func NewWSClient() *WSClient {
	return &WSClient{
		Messages: make([]WSMessage, 0),
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (c *WSClient) Connect(url string) error {
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
		defer c.Conn.Close()
		for {
			_, message, err := c.Conn.ReadMessage()
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
	if c.Conn != nil {
		c.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.Conn.Close()
	}
}
