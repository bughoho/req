package req

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTransportDefaultHTTPProxyConnectTimeout 钉死 fork 的 HTTPProxyConnectTimeout 默认值
// （1min），对称 TestTransportDefaultSocksDialTimeout。
//
// 理由：非 reqx 的直接 req.C() 消费者只靠这个 fork 默认（经 <=0 时的回落逻辑）兜底；
// 没有测试钉住它，一旦有人改动回落逻辑或把默认值改成别的，会静默改变既有行为且无报警。
func TestTransportDefaultHTTPProxyConnectTimeout(t *testing.T) {
	got := T().HTTPProxyConnectTimeout
	want := 1 * time.Minute
	if got != want {
		t.Fatalf("T().HTTPProxyConnectTimeout = %v, want %v", got, want)
	}
}

// TestHTTPProxyConnectTimeoutWedgedProxy 验证：HTTP 代理接受了到自己的 TCP 连接，但对
// CONNECT 请求不回任何响应时，CONNECT 握手被 HTTPProxyConnectTimeout 中断、请求在超时内
// 返回错误，而不是永久阻塞。对称 TestSocksDialTimeoutWedgedProxy。
//
// 关键：必须让请求真正走到 dialConn 的 `case cm.targetScheme == "https"` 分支——用 https
// 目标 + http 代理触发。目标地址不必真实可达：CONNECT 会先卡在代理这一步，客户端根本不需要
// 解析/连接目标（CONNECT 前 DNS 都用不上，目标主机名只出现在 CONNECT 请求行里）。
//
// 断言和 SOCKS 那条一样加强：耗时落在 [400ms,2s]、错误是超时类。
func TestHTTPProxyConnectTimeoutWedgedProxy(t *testing.T) {
	// 黑洞 HTTP 代理：Accept 后什么都不做（不读不写不关），模拟"接了 TCP 不回 CONNECT 响应"。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var mu sync.Mutex
	var conns []net.Conn
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	}()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c) // 故意持有不处理，测试结束后统一 Close
			mu.Unlock()
		}
	}()

	client := tc().
		SetTimeout(30 * time.Second). // 显式设长，确保未修复时不是 client.Timeout 提前救场
		SetProxyURL("http://" + ln.Addr().String()).
		SetHTTPProxyConnectTimeout(500 * time.Millisecond)

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := client.R().Get("https://example.com/")
		done <- err
	}()

	var reqErr error
	select {
	case reqErr = <-done:
		if reqErr == nil {
			t.Fatal("expected error from wedged http-connect proxy, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not return within 3s: HTTPS CONNECT handshake has no timeout guard")
	}
	elapsed := time.Since(start)

	if elapsed < 400*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want in [400ms, 2s] (HTTPProxyConnectTimeout=500ms; elapsed far outside this window means the request returned for some other reason than the CONNECT guard)", elapsed)
	}

	errText := reqErr.Error()
	if !strings.Contains(errText, "deadline exceeded") && !strings.Contains(errText, "i/o timeout") {
		t.Fatalf("error = %q, want a timeout-class error containing %q or %q", errText, "deadline exceeded", "i/o timeout")
	}
}
