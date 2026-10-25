package egress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Connector opens the outbound connection. The real one is Dialer; tests substitute their own.
type Connector interface {
	DialContext(ctx context.Context, host string, port int) (net.Conn, error)
}

// Proxy is an http.Handler for a forward proxy: CONNECT tunnels for HTTPS, and absolute-URI
// requests for plain HTTP.
type Proxy struct {
	Allow Allowlist
	Dial  Connector
	Log   *slog.Logger

	transport *http.Transport
}

// New returns a Proxy that connects through d.
func New(allow Allowlist, d Connector, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	p := &Proxy{Allow: allow, Dial: d, Log: log}
	p.transport = &http.Transport{
		// The proxy has already checked the host and port; the transport only has to connect,
		// through the same checked dialer.
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			n, _ := strconv.Atoi(port)
			return d.DialContext(ctx, host, n)
		},
		// Never use an environment proxy: the proxy must not chain through another.
		Proxy:                 nil,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
	}
	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	p.forward(w, r)
}

// target splits an authority into a host and a port, with defPort when it has none.
func target(authority string, defPort int) (string, int, bool) {
	host, port := authority, defPort
	if h, ps, err := net.SplitHostPort(authority); err == nil {
		n, err := strconv.Atoi(ps)
		if err != nil {
			return "", 0, false
		}
		host, port = h, n
	} else if strings.Contains(authority, ":") || strings.ContainsAny(authority, "[]") {
		// An IPv6 literal or a malformed authority.
		return "", 0, false
	}
	return strings.TrimSuffix(strings.ToLower(host), "."), port, true
}

func (p *Proxy) refuse(w http.ResponseWriter, r *http.Request, host string, port int, why string) {
	p.Log.Info("deny", "method", r.Method, "host", host, "port", port, "why", why)
	http.Error(w, "satat-egress: "+why+"\n", http.StatusForbidden)
}

// check applies the allowlist, and answers the client itself when the target is refused.
func (p *Proxy) check(w http.ResponseWriter, r *http.Request, host string, port int) bool {
	switch {
	case !IsName(host):
		p.refuse(w, r, host, port, "only host names are allowed, not addresses")
	case !p.Allow.Allows(host, port):
		p.refuse(w, r, host, port, "host and port are not on this lane's allowlist")
	default:
		return true
	}
	return false
}

func (p *Proxy) dialFailed(w http.ResponseWriter, r *http.Request, host string, port int, err error) {
	if errors.Is(err, ErrBlocked) {
		p.refuse(w, r, host, port, "host resolves to a private or reserved address")
		return
	}
	p.Log.Info("dial failed", "host", host, "port", port, "err", err)
	http.Error(w, "satat-egress: cannot reach the host\n", http.StatusBadGateway)
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port, ok := target(r.Host, 443)
	if !ok {
		p.refuse(w, r, r.Host, 0, "bad target")
		return
	}
	if !p.check(w, r, host, port) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	up, err := p.Dial.DialContext(ctx, host, port)
	cancel()
	if err != nil {
		p.dialFailed(w, r, host, port, err)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "satat-egress: cannot tunnel\n", http.StatusInternalServerError)
		return
	}
	down, buf, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	p.Log.Info("allow", "method", "CONNECT", "host", host, "port", port)
	_, _ = down.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	// Bytes the client sent after its CONNECT, before the tunnel was up.
	if n := buf.Reader.Buffered(); n > 0 {
		b, _ := buf.Reader.Peek(n)
		_, _ = up.Write(b)
	}
	pipe(down, up)
}

// pipe copies both ways until each side is done, then closes both.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

// hopHeaders are the headers a proxy must not pass on (RFC 9110 section 7.6.1).
var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" || r.URL.Scheme != "http" {
		// A request to the proxy itself, or for a scheme only a CONNECT tunnel may carry.
		p.refuse(w, r, r.Host, 0, "this is a forward proxy: send an absolute http:// URL or CONNECT")
		return
	}
	host, port, ok := target(r.URL.Host, 80)
	if !ok {
		p.refuse(w, r, r.URL.Host, 0, "bad target")
		return
	}
	if !p.check(w, r, host, port) {
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range strings.Split(r.Header.Get("Connection"), ",") {
		out.Header.Del(strings.TrimSpace(h))
	}
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	resp, err := p.transport.RoundTrip(out)
	if err != nil {
		p.dialFailed(w, r, host, port, err)
		return
	}
	defer resp.Body.Close()
	p.Log.Info("allow", "method", r.Method, "host", host, "port", port)
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
