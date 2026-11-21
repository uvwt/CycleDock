package channels

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
)

// wsConn 封装 gorilla/websocket，提供并发安全写入与 context 取消自动断开能力。
type wsConn struct {
	conn *websocket.Conn
	wmu  sync.Mutex
}

func dialWS(ctx context.Context, endpoint string) (*wsConn, error) {
	c, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	w := &wsConn{conn: c}
	go func() {
		<-ctx.Done()
		_ = c.Close()
	}()
	return w, nil
}

func (w *wsConn) Read() (int, []byte, error) {
	return w.conn.ReadMessage()
}

func (w *wsConn) WriteJSON(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	return w.conn.WriteMessage(websocket.TextMessage, raw)
}

func (w *wsConn) Close() error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	return w.conn.Close()
}
