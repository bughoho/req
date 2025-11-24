package req

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBufferPoolEnabled(t *testing.T) {
	// Create test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test response body"))
	}))
	defer ts.Close()

	// Enable buffer pool at client level
	client := C().SetEnableBufferPool(true)
	resp, err := client.R().Get(ts.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Read body
	body, err := resp.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	if string(body) != "test response body" {
		t.Errorf("Expected 'test response body', got '%s'", string(body))
	}

	// Verify buffer is from pool
	if !resp.fromBufferPool {
		t.Error("Expected body to be from buffer pool")
	}

	// Release buffer
	resp.ReleaseBody()

	// Verify buffer is released
	if resp.body != nil {
		t.Error("Expected body to be nil after ReleaseBody")
	}
	if resp.fromBufferPool {
		t.Error("Expected fromBufferPool to be false after ReleaseBody")
	}
}

func TestBufferPoolDisabled(t *testing.T) {
	// Create test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test response body"))
	}))
	defer ts.Close()

	// Buffer pool disabled by default
	client := C()
	resp, err := client.R().Get(ts.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Read body
	body, err := resp.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	if string(body) != "test response body" {
		t.Errorf("Expected 'test response body', got '%s'", string(body))
	}

	// Verify buffer is NOT from pool
	if resp.fromBufferPool {
		t.Error("Expected body to NOT be from buffer pool")
	}

	// ReleaseBody should be safe to call
	resp.ReleaseBody()
}

func TestSetBodyReleasesPoolBuffer(t *testing.T) {
	// Create test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("original body"))
	}))
	defer ts.Close()

	// Enable buffer pool at client level
	client := C().SetEnableBufferPool(true)
	resp, err := client.R().Get(ts.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Read body from pool
	_, err = resp.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	if !resp.fromBufferPool {
		t.Error("Expected body to be from buffer pool")
	}

	// SetBody should release the pool buffer
	resp.SetBody([]byte("new body"))

	if resp.fromBufferPool {
		t.Error("Expected fromBufferPool to be false after SetBody")
	}

	if string(resp.body) != "new body" {
		t.Errorf("Expected 'new body', got '%s'", string(resp.body))
	}
}

func TestSetBodyStringReleasesPoolBuffer(t *testing.T) {
	// Create test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("original body"))
	}))
	defer ts.Close()

	// Enable buffer pool at client level
	client := C().SetEnableBufferPool(true)
	resp, err := client.R().Get(ts.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Read body from pool
	_, err = resp.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	if !resp.fromBufferPool {
		t.Error("Expected body to be from buffer pool")
	}

	// SetBodyString should release the pool buffer
	resp.SetBodyString("new body string")

	if resp.fromBufferPool {
		t.Error("Expected fromBufferPool to be false after SetBodyString")
	}

	if string(resp.body) != "new body string" {
		t.Errorf("Expected 'new body string', got '%s'", string(resp.body))
	}
}

func TestBufferPoolWithLargeBody(t *testing.T) {
	// Create large body (larger than initial 512 bytes)
	largeBody := strings.Repeat("x", 2048)

	// Create test server without Content-Length
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write without Content-Length to test buffer growth
		w.Header().Del("Content-Length")
		io.WriteString(w, largeBody)
	}))
	defer ts.Close()

	// Enable buffer pool at client level
	client := C().SetEnableBufferPool(true)
	resp, err := client.R().Get(ts.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Read body
	body, err := resp.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	if string(body) != largeBody {
		t.Errorf("Body length mismatch: expected %d, got %d", len(largeBody), len(body))
	}

	// Verify buffer is from pool
	if !resp.fromBufferPool {
		t.Error("Expected body to be from buffer pool")
	}

	// Release buffer
	resp.ReleaseBody()
}

func TestBufferPoolWithRetry(t *testing.T) {
	attempts := 0
	// Create test server that fails first time
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error"))
		} else {
			w.Write([]byte("success"))
		}
	}))
	defer ts.Close()

	// Enable buffer pool at client level
	client := C().SetEnableBufferPool(true)
	resp, err := client.R().
		SetRetryCount(1).
		AddRetryCondition(func(resp *Response, err error) bool {
			return resp != nil && resp.StatusCode >= 500
		}).
		Get(ts.URL)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Read body
	body, err := resp.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	if string(body) != "success" {
		t.Errorf("Expected 'success', got '%s'", string(body))
	}

	// Verify retry happened
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}
