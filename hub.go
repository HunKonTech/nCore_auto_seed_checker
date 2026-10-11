package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/net/websocket"

	"ncore_automation/api"
)

// Hub keeps track of the connected web interfaces and pushes updates to them.
type Hub struct {
	mu        sync.Mutex
	clients   map[*wsClient]struct{}
	OnConnect func(c *wsClient)
	OnMessage func(c *wsClient, msg api.Envelope)
}

type wsClient struct {
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
	done      chan struct{}
}

func NewHub() *Hub {
	return &Hub{clients: map[*wsClient]struct{}{}}
}

// Handler serves the websocket endpoint. Only pages served by this server may
// connect, so other websites open in the same browser cannot drive it.
func (h *Hub) Handler() http.Handler {
	return websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) (err error) {
			// Browsers always send Origin; other clients may leave it out.
			if cfg.Origin, err = websocket.Origin(cfg, r); err != nil {
				return err
			}
			if cfg.Origin != nil && !strings.EqualFold(cfg.Origin.Host, r.Host) {
				return fmt.Errorf("origin %s not allowed", cfg.Origin)
			}
			return nil
		},
		Handler: h.serve,
	}
}

func (h *Hub) serve(conn *websocket.Conn) {
	c := &wsClient{conn: conn, send: make(chan []byte, 256), done: make(chan struct{})}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		c.close()
	}()

	go c.writeLoop()
	if h.OnConnect != nil {
		h.OnConnect(c)
	}
	for {
		var msg api.Envelope
		if err := websocket.JSON.Receive(conn, &msg); err != nil {
			return
		}
		if h.OnMessage != nil {
			h.OnMessage(c, msg)
		}
	}
}

// Broadcast sends a message to every connected client. Frames are dropped
// for clients that cannot keep up; anything else disconnects them so they
// reconnect and receive a fresh state.
func (h *Hub) Broadcast(typ string, data any) {
	if h == nil {
		return
	}
	b, err := encode(typ, data)
	if err != nil {
		log.Println("websocket encode:", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.enqueue(b, typ == api.MsgFrame)
	}
}

// Send sends a message to one client.
func (c *wsClient) Send(typ string, data any) {
	b, err := encode(typ, data)
	if err != nil {
		log.Println("websocket encode:", err)
		return
	}
	c.enqueue(b, typ == api.MsgFrame)
}

func (c *wsClient) enqueue(b []byte, droppable bool) {
	select {
	case <-c.done:
	case c.send <- b:
	default:
		if !droppable {
			c.close()
		}
	}
}

func (c *wsClient) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case b := <-c.send:
			if err := websocket.Message.Send(c.conn, string(b)); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *wsClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

func encode(typ string, data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(api.Envelope{Type: typ, Data: raw})
}
