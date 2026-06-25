package wsutil

import (
	"bytes"
	"errors"
	"io"
	"net"
	"time"

	"golang.org/x/net/websocket"
)

const binaryFrame = 2

func CopyWebSocketToWriter(dst io.Writer, src *websocket.Conn) error {
	var payload []byte
	for {
		if err := websocket.Message.Receive(src, &payload); err != nil {
			if isCloseErr(err) {
				return nil
			}
			return err
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := dst.Write(payload); err != nil {
			if isCloseErr(err) {
				return nil
			}
			return err
		}
	}
}

func CopyReaderToWebSocket(dst *websocket.Conn, src io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if sendErr := websocket.Message.Send(dst, append([]byte(nil), buf[:n]...)); sendErr != nil {
				if isCloseErr(sendErr) {
					return nil
				}
				return sendErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || isCloseErr(err) {
				return nil
			}
			return err
		}
	}
}

func CopyWebSocket(dst *websocket.Conn, src *websocket.Conn) error {
	return CopyWebSocketWithTransform(dst, src, nil)
}

func CopyWebSocketWithTransform(dst *websocket.Conn, src *websocket.Conn, transform func([]byte) []byte) error {
	var payload []byte
	for {
		if err := websocket.Message.Receive(src, &payload); err != nil {
			if isCloseErr(err) {
				return nil
			}
			return err
		}
		if transform != nil {
			payload = transform(payload)
		}
		if err := websocket.Message.Send(dst, payload); err != nil {
			if isCloseErr(err) {
				return nil
			}
			return err
		}
	}
}

func AppendNewline(payload []byte) []byte {
	if len(payload) == 0 || bytes.HasSuffix(payload, []byte("\n")) || bytes.HasSuffix(payload, []byte("\r")) {
		return payload
	}
	out := make([]byte, 0, len(payload)+1)
	out = append(out, payload...)
	out = append(out, '\n')
	return out
}

func CloseWebSocket(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
	_ = conn.Close()
}

func IsCloseErr(err error) bool {
	return isCloseErr(err)
}

func isCloseErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var closeErr *websocket.ProtocolError
	return errors.As(err, &closeErr)
}
