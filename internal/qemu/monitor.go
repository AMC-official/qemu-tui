package qemu

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Client is a minimal QMP (QEMU Machine Protocol) client.
type Client struct {
	conn    net.Conn
	r       *bufio.Reader
	Timeout time.Duration
}

type qmpReply struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
	Event string `json:"event"`
}

// Dial connects to a QMP endpoint and negotiates capabilities.
func Dial(network, addr string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout(network, addr, timeout)
	if err != nil {
		return nil, err
	}
	c, err := NewClient(conn, timeout)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// NewClient performs the QMP handshake on an existing connection.
func NewClient(conn net.Conn, timeout time.Duration) (*Client, error) {
	c := &Client{conn: conn, r: bufio.NewReader(conn), Timeout: timeout}
	conn.SetDeadline(time.Now().Add(timeout))
	line, err := c.r.ReadBytes('\n') // greeting
	if err != nil {
		return nil, fmt.Errorf("qmp greeting: %w", err)
	}
	var greet map[string]json.RawMessage
	if err := json.Unmarshal(line, &greet); err != nil || greet["QMP"] == nil {
		return nil, errors.New("not a QMP endpoint")
	}
	if _, err := c.Execute("qmp_capabilities", nil); err != nil {
		return nil, err
	}
	return c, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Execute runs a QMP command and returns its "return" payload.
func (c *Client) Execute(cmd string, args map[string]any) (json.RawMessage, error) {
	c.conn.SetDeadline(time.Now().Add(c.Timeout))
	req := map[string]any{"execute": cmd}
	if args != nil {
		req["arguments"] = args
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	for {
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var rep qmpReply
		if err := json.Unmarshal(line, &rep); err != nil {
			return nil, fmt.Errorf("bad QMP reply: %w", err)
		}
		if rep.Event != "" {
			continue // asynchronous event
		}
		if rep.Error != nil {
			return nil, fmt.Errorf("qmp %s: %s", rep.Error.Class, rep.Error.Desc)
		}
		return rep.Return, nil
	}
}

// HumanCommand runs a human-monitor command such as "info status".
func (c *Client) HumanCommand(line string) (string, error) {
	raw, err := c.Execute("human-monitor-command", map[string]any{"command-line": line})
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// Status returns the QMP run state and whether the guest is running.
func (c *Client) Status() (string, bool, error) {
	raw, err := c.Execute("query-status", nil)
	if err != nil {
		return "", false, err
	}
	var st struct {
		Status  string `json:"status"`
		Running bool   `json:"running"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return "", false, err
	}
	return st.Status, st.Running, nil
}
