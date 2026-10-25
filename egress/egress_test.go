package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestAllowlist(t *testing.T) {
	a, err := ParseAllowlist([]string{"github.com", "*.githubusercontent.com", "Registry.NPMJS.org", "example.org:8443"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		host string
		port int
		want bool
	}{
		{"github.com", 443, true},
		{"github.com", 80, true},
		{"GitHub.com.", 443, true},
		{"github.com", 22, false},
		{"github.com", 8443, false},
		{"api.github.com", 443, false}, // a plain entry is not a wildcard
		{"notgithub.com", 443, false},
		{"github.com.evil.example", 443, false},
		{"release-assets.githubusercontent.com", 443, true},
		{"githubusercontent.com", 443, false}, // a wildcard is sub-domains only
		{"evilgithubusercontent.com", 443, false},
		{"registry.npmjs.org", 443, true},
		{"example.org", 8443, true},
		{"example.org", 443, false},
		{"1.1.1.1", 443, false},
		{"127.0.0.1", 443, false},
		{"2130706433", 443, false},
		{"0x7f.1", 443, false},
		{"::1", 443, false},
		{"localhost", 443, false},
		{"", 443, false},
	} {
		if got := a.Allows(c.host, c.port); got != c.want {
			t.Errorf("Allows(%q, %d) = %v, want %v", c.host, c.port, got, c.want)
		}
	}
	if (Allowlist{}).Allows("github.com", 443) {
		t.Error("the zero allowlist must allow nothing")
	}
}

func TestParseAllowlistRejectsWhatIsNotAHostName(t *testing.T) {
	for _, bad := range []string{"1.1.1.1", "10.0.0.1:443", "[::1]", "::1", "localhost", "*", "*.com.", "a b.com", "github.com:0", "github.com:http", "github.com:99999", "", "https://github.com", "github.com/x", "*.1.1"} {
		if _, err := ParseAllowlist([]string{bad}); err == nil {
			t.Errorf("entry %q was accepted", bad)
		}
	}
}

func TestBlockedCoversEveryPrivateRange(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.1.2.3", "10.0.0.5", "172.16.0.1", "172.17.0.2", "172.31.255.255", "192.168.65.254",
		"169.254.169.254", "169.254.0.1", "100.64.0.1", "100.100.100.200", "0.0.0.0", "224.0.0.1", "255.255.255.255",
		"198.18.0.1", "240.0.0.1", "::1", "::", "fe80::1", "fc00::1", "fd00:ec2::254", "::ffff:127.0.0.1",
		"::ffff:10.0.0.1", "::ffff:169.254.169.254", "64:ff9b::7f00:1", "2002:7f00:1::1", "ff02::1",
	} {
		if !Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s is not blocked", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "140.82.112.3", "172.32.0.1", "2606:4700:4700::1111"} {
		if Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s is blocked", s)
		}
	}
}

func TestDialerRefusesPrivateAddressesBeforeConnecting(t *testing.T) {
	// A listener on loopback stands in for the host or the Docker API proxy. The names below are
	// "rebinding" names: they would resolve to it. Dialing the literal through the Dialer must
	// never connect.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	hit := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			hit <- struct{}{}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	// localhost resolves to loopback through the system resolver, with no network needed.
	_, err = Dialer{}.DialContext(context.Background(), "localhost", port)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	select {
	case <-hit:
		t.Fatal("the dialer connected to a loopback listener")
	default:
	}
}

// pipeConnector connects every allowed request to one test server and records the target the
// proxy asked for, so no test needs the network.
type pipeConnector struct {
	addr   string
	dialed []string
}

func (c *pipeConnector) DialContext(ctx context.Context, host string, port int) (net.Conn, error) {
	c.dialed = append(c.dialed, host+":"+strconv.Itoa(port))
	return (&net.Dialer{}).DialContext(ctx, "tcp", c.addr)
}

type blockingConnector struct{}

func (blockingConnector) DialContext(context.Context, string, int) (net.Conn, error) {
	return nil, ErrBlocked
}

func startProxy(t *testing.T, allow []string, d Connector) string {
	t.Helper()
	a, err := ParseAllowlist(allow)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(a, d, nil))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// connect sends a CONNECT by hand and returns the status line, and the connection if it was 200.
func connect(t *testing.T, proxy, authority string) (string, net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line), c, br
}

func TestConnectTunnelsToAnAllowedHostOnly(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	d := &pipeConnector{addr: echo.Addr().String()}
	proxy := startProxy(t, []string{"github.com", "*.pythonhosted.org"}, d)

	status, c, br := connect(t, proxy, "github.com:443")
	if !strings.Contains(status, "200") {
		t.Fatalf("allowed host: %s", status)
	}
	br.ReadString('\n') // the blank line that ends the response head
	_, _ = c.Write([]byte("ping\n"))
	if got, err := br.ReadString('\n'); err != nil || got != "ping\n" {
		t.Errorf("tunnel echo = %q, %v", got, err)
	}
	_ = c.Close()

	for authority, why := range map[string]string{
		"evil.example:443":          "not listed",
		"github.com:22":             "port not allowed",
		"github.com:8080":           "port not allowed",
		"1.1.1.1:443":               "address literal",
		"127.0.0.1:2375":            "loopback literal",
		"169.254.169.254:80":        "metadata literal",
		"[::1]:443":                 "ipv6 literal",
		"localhost:443":             "single label",
		"2130706433:80":             "decimal literal",
		"files.pythonhosted.org:22": "wildcard but wrong port",
	} {
		status, c, _ := connect(t, proxy, authority)
		c.Close()
		if !strings.Contains(status, "403") {
			t.Errorf("CONNECT %s (%s): %s, want 403", authority, why, status)
		}
	}
	if want := []string{"github.com:443"}; strings.Join(d.dialed, ",") != strings.Join(want, ",") {
		t.Errorf("dialed %v, want %v: a refused request must not dial", d.dialed, want)
	}
}

func TestConnectRefusedWhenTheNameResolvesPrivate(t *testing.T) {
	proxy := startProxy(t, []string{"rebind.example.com"}, blockingConnector{})
	status, c, _ := connect(t, proxy, "rebind.example.com:443")
	c.Close()
	if !strings.Contains(status, "403") {
		t.Errorf("status %q, want 403", status)
	}
}

func TestPlainHTTPForwardsToAnAllowedHostOnly(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Proxy-Auth", r.Header.Get("Proxy-Authorization"))
		fmt.Fprint(w, "hello from origin")
	}))
	defer origin.Close()
	d := &pipeConnector{addr: origin.Listener.Addr().String()}
	proxy := startProxy(t, []string{"registry.example.com"}, d)

	get := func(target string) (*http.Response, string) {
		pu, _ := url.Parse("http://" + proxy)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
		resp, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, body := get("http://registry.example.com/x")
	if resp.StatusCode != 200 || body != "hello from origin" {
		t.Fatalf("allowed: %d %q", resp.StatusCode, body)
	}
	for _, u := range []string{"http://evil.example.com/", "http://registry.example.com:8080/", "http://10.0.0.1/", "http://127.0.0.1:2375/version"} {
		if resp, _ := get(u); resp.StatusCode != 403 {
			t.Errorf("GET %s: %d, want 403", u, resp.StatusCode)
		}
	}
	// A request to the proxy itself is refused, not served.
	r, err := http.Get("http://" + proxy + "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Errorf("direct request: %d, want 403", r.StatusCode)
	}
	if len(d.dialed) != 1 {
		t.Errorf("dialed %v: refused requests must not dial", d.dialed)
	}
}
