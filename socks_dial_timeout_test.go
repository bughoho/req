package req

import (
	"net"
	"testing"
	"time"
)

// TestSocksDialTimeoutWedgedProxy 验证：SOCKS5 代理接受了 TCP 连接但从不回应握手时，
// 拨号被 SocksDialTimeout 中断、请求在超时内返回错误，而不是永久阻塞。
//
// 护栏语义：未接 dialConn socks5 分支时（光有字段），SOCKS 握手在无 deadline 的
// detached context 上跑 → io.ReadFull 永久阻塞 → 请求靠 30s 的 client.Timeout 才返回
// → 落在 3s 断言窗之外 → 本测试失败（正确地在 bug 上变红）。
func TestSocksDialTimeoutWedgedProxy(t *testing.T) {
	// 黑洞 SOCKS5 代理：Accept 后什么都不做（不读不写不关），模拟"接了 TCP 不回握手"。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c // 故意持有不处理
		}
	}()

	client := tc().
		SetTimeout(30 * time.Second). // 显式设长，确保未修复时不是 client.Timeout 提前救场
		SetProxyURL("socks5://" + ln.Addr().String()).
		SetSocksDialTimeout(500 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := client.R().Get("http://example.com/")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from wedged socks proxy, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not return within 3s: SOCKS handshake has no timeout guard")
	}
}
