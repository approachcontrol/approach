package launchcontrol

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ClientTimeout bounds one proxied call end to end.
const ClientTimeout = 30 * time.Second

// ErrUnreachable reports that the control endpoint did not answer: dial
// failed, the connection dropped, or the peer closed before a full response
// line. The request may or may not have been logged by the controller; the
// caller decides by verb class what to do next.
var ErrUnreachable = errors.New("control endpoint unreachable")

// ErrResponseLost is the ErrUnreachable case where the whole request was
// written and the endpoint then closed, timed out, or dropped before a full
// response: the controller may have logged and applied the request. It
// wraps ErrUnreachable, so callers that only care about "no answer" see that;
// callers of non-idempotent verbs can tell this case apart and not guess.
var ErrResponseLost = fmt.Errorf("%w: response lost after the request was sent", ErrUnreachable)

// Client is the agent side of the protocol. Identity fields are stamped onto
// every request; the controller refuses a request whose identity does not
// match the registration for its LaunchID.
type Client struct {
	Endpoint string
	Token    string
	LaunchID string
	FlowID   string
	PhaseID  string
	// Timeout overrides ClientTimeout when positive.
	Timeout time.Duration
}

// Call sends req and returns the controller's response. A returned Response
// (OK or Refused) is final. Any transport failure is ErrUnreachable.
func (c Client) Call(req Request) (Response, error) {
	if c.Endpoint == "" {
		return Response{}, fmt.Errorf("%w: no endpoint", ErrUnreachable)
	}
	req.SchemaVersion = ProtocolSchemaVersion
	if req.RequestID == "" {
		req.RequestID = NewRequestID()
	}
	req.Token = c.Token
	req.LaunchID = c.LaunchID
	if req.FlowID == "" {
		req.FlowID = c.FlowID
	}
	if req.PhaseID == "" {
		req.PhaseID = c.PhaseID
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = ClientTimeout
	}
	conn, err := net.DialTimeout("unix", c.Endpoint, timeout)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := writeFrame(conn, req); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	var resp Response
	if err := readFrame(conn, &resp); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Response{}, fmt.Errorf("%w: connection closed before a response", ErrResponseLost)
		}
		return Response{}, fmt.Errorf("%w: %v", ErrResponseLost, err)
	}
	return resp, nil
}
