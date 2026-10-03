package http2

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imroc/req/v3/internal/transport"
)

// Regression tests for the HTTP/2 idle-connection timeout.
//
// Transport embeds *transport.Options, which carries IdleConnTimeout, but the
// struct also declares its own depth-0 IdleConnTimeout field that shadows the
// embedded one. Callers (req.Transport) only ever write the Options field, so
// arming the idle timer from the shadowing field meant the timer was never
// armed: idle h2 ClientConns, together with their readLoop goroutines, lived
// forever. These tests pin the fix: the timer must be armed from Options.

const idleTestTimeout = 50 * time.Millisecond

func waitClosed(t *testing.T, done <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s not closed within %v", what, d)
	}
}

func assertStillOpen(t *testing.T, done <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-done:
		t.Fatalf("%s closed unexpectedly", what)
	case <-time.After(d):
	}
}

// TestIdleConnTimeout_ArmedFromOptions drives newClientConn over a net.Pipe
// and checks that the idle timer is armed from Options.IdleConnTimeout and
// that, once it fires on an idle connection, the readLoop goroutine exits.
func TestIdleConnTimeout_ArmedFromOptions(t *testing.T) {
	tr := &Transport{Options: &transport.Options{IdleConnTimeout: idleTestTimeout}}
	cc := newPipeClientConn(t, tr, false)

	if cc.idleTimer == nil {
		t.Fatal("idle timer not armed although Options.IdleConnTimeout is set")
	}
	if cc.idleTimeout != idleTestTimeout {
		t.Fatalf("cc.idleTimeout = %v, want %v", cc.idleTimeout, idleTestTimeout)
	}
	// No streams are open, so the timer must close the conn and the readLoop
	// must observe the closed conn and exit.
	waitClosed(t, cc.readerDone, 2*time.Second, "readLoop (readerDone)")
	cc.mu.Lock()
	closed := cc.closed
	cc.mu.Unlock()
	if !closed {
		t.Fatal("cc.closed = false after the idle timer fired")
	}
}

// TestIdleConnTimeout_ZeroKeepsConnOpen is the control: with no idle timeout
// nothing is armed and the connection stays open.
func TestIdleConnTimeout_ZeroKeepsConnOpen(t *testing.T) {
	tr := &Transport{Options: &transport.Options{}}
	cc := newPipeClientConn(t, tr, false)
	if cc.idleTimer != nil {
		t.Fatal("idle timer armed although Options.IdleConnTimeout is zero")
	}
	assertStillOpen(t, cc.readerDone, 4*idleTestTimeout, "readLoop (readerDone)")
}

// TestIdleConnTimeout_NilOptions guards the nil-Options path: a bare
// Transport{} (used by other tests in this package and legal for NewClientConn
// callers) must neither panic nor arm the timer.
func TestIdleConnTimeout_NilOptions(t *testing.T) {
	cc := newPipeClientConn(t, &Transport{}, false)
	if cc.idleTimer != nil {
		t.Fatal("idle timer armed on a Transport without Options")
	}
}

// h2TestServer starts a TLS httptest server speaking h2 and reports, through
// the returned channel, every time the server observes a connection closed.
func h2TestServer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	closed := make(chan struct{}, 8)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.EnableHTTP2 = true
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, closed
}

// dialH2 dials the test server with ALPN h2 and wraps the conn through
// NewClientConn, the same constructor the connection pool uses on a dial miss.
func dialH2(t *testing.T, tr *Transport, srv *httptest.Server) *ClientConn {
	t.Helper()
	conn, err := tls.Dial("tcp", srv.Listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{NextProtoTLS},
	})
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if p := conn.ConnectionState().NegotiatedProtocol; p != NextProtoTLS {
		t.Fatalf("negotiated ALPN %q, want %q", p, NextProtoTLS)
	}
	cc, err := tr.NewClientConn(conn)
	if err != nil {
		t.Fatalf("NewClientConn: %v", err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

func doGet(t *testing.T, cc *ClientConn, srv *httptest.Server) {
	t.Helper()
	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cc.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("resp.Proto = %q, want HTTP/2", resp.Proto)
	}
}

// TestIdleConnTimeout_ClosesIdleConnAfterRequest is the end-to-end check
// against a real h2 server: once a request has completed the connection is
// idle, the idle timer fires, the TCP connection is closed (observed by the
// server) and the readLoop goroutine exits.
func TestIdleConnTimeout_ClosesIdleConnAfterRequest(t *testing.T) {
	srv, closed := h2TestServer(t)
	tr := &Transport{Options: &transport.Options{IdleConnTimeout: idleTestTimeout}}
	cc := dialH2(t, tr, srv)
	doGet(t, cc, srv)

	waitClosed(t, cc.readerDone, 2*time.Second, "readLoop (readerDone)")
	waitClosed(t, closed, 2*time.Second, "server-side connection")
}

// TestIdleConnTimeout_ZeroKeepsConnOpenAfterRequest is the end-to-end
// control: with no idle timeout the connection outlives the request.
func TestIdleConnTimeout_ZeroKeepsConnOpenAfterRequest(t *testing.T) {
	srv, closed := h2TestServer(t)
	tr := &Transport{Options: &transport.Options{}}
	cc := dialH2(t, tr, srv)
	doGet(t, cc, srv)

	assertStillOpen(t, cc.readerDone, 4*idleTestTimeout, "readLoop (readerDone)")
	assertStillOpen(t, closed, 10*time.Millisecond, "server-side connection")
}
