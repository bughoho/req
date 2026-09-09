package req

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSocksDialTimeoutWedgedProxy 验证：SOCKS5 代理接受了 TCP 连接但从不回应握手时，
// 拨号被 SocksDialTimeout 中断、请求在超时内返回错误，而不是永久阻塞。
//
// 护栏语义：未接 dialConn socks5 分支时（光有字段），SOCKS 握手在无 deadline 的
// detached context 上跑 → io.ReadFull 永久阻塞 → 请求靠 30s 的 client.Timeout 才返回
// → 落在 3s 断言窗之外 → 本测试失败（正确地在 bug 上变红）。
//
// 断言加强（不只看"3s 内返回且有错"，而是钉住"确实是被 SocksDialTimeout 切断"）：
//   - 耗时落在 [400ms, 2s]：SocksDialTimeout 设的是 500ms，太快（<400ms）说明可能是别的
//     错误提前返回，太慢（>2s）说明护栏没生效、只是撞到别的更松的超时。
//   - 错误是超时类（含 "deadline exceeded" 或 "i/o timeout"），而不是任意错误。
func TestSocksDialTimeoutWedgedProxy(t *testing.T) {
	// 黑洞 SOCKS5 代理：Accept 后什么都不做（不读不写不关），模拟"接了 TCP 不回握手"。
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
			conns = append(conns, c) // 故意持有不处理，测试结束后统一 Close，避免悬留
			mu.Unlock()
		}
	}()

	client := tc().
		SetTimeout(30 * time.Second). // 显式设长，确保未修复时不是 client.Timeout 提前救场
		SetProxyURL("socks5://" + ln.Addr().String()).
		SetSocksDialTimeout(500 * time.Millisecond)

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := client.R().Get("http://example.com/")
		done <- err
	}()

	var reqErr error
	select {
	case reqErr = <-done:
		if reqErr == nil {
			t.Fatal("expected error from wedged socks proxy, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not return within 3s: SOCKS handshake has no timeout guard")
	}
	elapsed := time.Since(start)

	if elapsed < 400*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want in [400ms, 2s] (SocksDialTimeout=500ms; elapsed far outside this window means the request returned for some other reason than the SOCKS handshake guard)", elapsed)
	}

	errText := reqErr.Error()
	if !strings.Contains(errText, "deadline exceeded") && !strings.Contains(errText, "i/o timeout") {
		t.Fatalf("error = %q, want a timeout-class error containing %q or %q", errText, "deadline exceeded", "i/o timeout")
	}
}

// TestTransportDefaultSocksDialTimeout 钉死 fork 的 SocksDialTimeout 默认值（30s）。
//
// 理由：非 reqx 的直接 req.C() 消费者只靠这个 fork 默认兜底；没有测试钉住它，一旦有人
// 改回 0 或把 req/v3 换回上游（无此字段），会静默失护且无报警。
func TestTransportDefaultSocksDialTimeout(t *testing.T) {
	got := T().SocksDialTimeout
	want := 30 * time.Second
	if got != want {
		t.Fatalf("T().SocksDialTimeout = %v, want %v (fork default must stay non-zero so plain req.C() consumers without explicit SetSocksDialTimeout still get a dial guard)", got, want)
	}
}
