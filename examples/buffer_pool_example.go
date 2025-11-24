package main

import (
	"fmt"
	"log"

	"github.com/imroc/req/v3"
)

func main() {
	// Example 1: Using buffer pool (memory efficient for high-concurrency scenarios)
	fmt.Println("=== Example 1: With Buffer Pool ===")

	// Enable buffer pool at client level
	client := req.C().SetEnableBufferPool(true)
	resp, err := client.R().Get("https://httpbin.org/get")
	if err != nil {
		log.Fatal(err)
	}

	// Use the response body
	body, err := resp.ToBytes()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Response length: %d bytes\n", len(body))
	fmt.Println("Buffer allocated from pool")

	// IMPORTANT: Release the buffer back to pool when done
	resp.ReleaseBody()
	fmt.Println("Buffer released back to pool")

	// Example 2: Without buffer pool (default behavior)
	fmt.Println("\n=== Example 2: Without Buffer Pool (Default) ===")

	// Create a new client without buffer pool (default)
	client2 := req.C()
	resp2, err := client2.R().Get("https://httpbin.org/get")
	if err != nil {
		log.Fatal(err)
	}

	body2, err := resp2.ToBytes()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Response length: %d bytes\n", len(body2))
	fmt.Println("Buffer allocated normally (not from pool)")
	// No need to release when buffer pool is disabled

	// Example 3: SetBody/SetBodyString compatibility
	fmt.Println("\n=== Example 3: SetBody/SetBodyString Compatibility ===")

	// Use client with buffer pool enabled
	resp3, err := client.R().Get("https://httpbin.org/get")
	if err != nil {
		log.Fatal(err)
	}

	// Read body from pool
	_, err = resp3.ToBytes()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Body read from pool")

	// SetBodyString will automatically release the old buffer from pool
	resp3.SetBodyString("custom body")
	fmt.Println("SetBodyString called - old buffer automatically released")
	fmt.Printf("New body: %s\n", resp3.String())

	// Example 4: Automatic cleanup on retry
	fmt.Println("\n=== Example 4: Retry with Buffer Pool ===")

	// Use client with buffer pool enabled
	_, err = client.R().
		SetRetryCount(2).
		Get("https://httpbin.org/status/500") // Will trigger retry
	if err != nil {
		fmt.Printf("Request failed after retries (expected): %v\n", err)
	}
	// Buffer is automatically released during retry cleanup
	fmt.Println("Buffer automatically managed during retries")
}
