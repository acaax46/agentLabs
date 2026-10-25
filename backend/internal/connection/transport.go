package connection

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type boundTransport struct {
	base   *http.Transport
	origin string
}

func (t boundTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if origin(r.URL.String()) != t.origin {
		return nil, fmt.Errorf("unapproved model origin")
	}
	return t.base.RoundTrip(r)
}
func (t boundTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

func normalizeBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(u.Host, "\\%") {
		return "", invalid("请输入不含账号、查询参数的模型服务地址。")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", invalid("模型服务地址必须使用 HTTPS。")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(u.Path, "/responses") {
		return "", invalid("当前连接支持 Chat Completions，请填写对应服务地址。")
	}
	u.Path = strings.TrimSuffix(u.Path, "/chat/completions")
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	u.RawPath = ""
	return u.String(), nil
}
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}
func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, network := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "2001::/32", "2002::/16"} {
		_, block, _ := net.ParseCIDR(network)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// HTTPClient pins the approved origin and validates the resolved IP on every new connection.
// Explicit host-side origins may authorize internal gateways; browser input cannot grant exceptions.
func HTTPClient(base string, allowedOrigins []string) (*http.Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, invalid("模型服务地址无效。")
	}
	trusted := false
	for _, allowed := range allowedOrigins {
		if origin(allowed) == origin(base) {
			trusted = true
		}
	}
	if u.Scheme != "https" && (!trusted || u.Scheme != "http") {
		return nil, invalid("外部模型服务需要 HTTPS；内部测试地址须由服务端显式允许。")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, IdleConnTimeout: time.Minute, MaxIdleConnsPerHost: 2}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(host, u.Hostname()) {
			return nil, fmt.Errorf("unapproved model destination")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("model destination unavailable")
		}
		for _, ip := range ips {
			if !trusted && !publicIP(ip.IP) {
				return nil, fmt.Errorf("model destination is not a public address")
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: boundTransport{base: transport, origin: origin(base)}, Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
