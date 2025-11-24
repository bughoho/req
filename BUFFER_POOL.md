# Buffer Pool Feature

## Overview

The Response type now supports using `github.com/libp2p/go-buffer-pool` for memory allocation, which can significantly reduce memory allocations and GC pressure in high-concurrency scenarios.

## Configuration

Buffer pool is controlled by a global variable:

```go
req.EnableBufferPool = false // Default: disabled
```

To enable buffer pool globally:

```go
req.EnableBufferPool = true
```

## Usage

### Basic Usage with Buffer Pool

```go
// Enable buffer pool
req.EnableBufferPool = true

client := req.C()
resp, err := client.R().Get("https://example.com")
if err != nil {
    log.Fatal(err)
}

// Read response body (allocated from pool)
body, err := resp.ToBytes()
if err != nil {
    log.Fatal(err)
}

// Use the body...
fmt.Println(string(body))

// IMPORTANT: Release buffer back to pool when done
resp.ReleaseBody()
```

### Without Buffer Pool (Default)

```go
// Buffer pool is disabled by default
req.EnableBufferPool = false // or just don't set it

client := req.C()
resp, err := client.R().Get("https://example.com")
if err != nil {
    log.Fatal(err)
}

body, err := resp.ToBytes()
if err != nil {
    log.Fatal(err)
}

// No need to call ReleaseBody() when buffer pool is disabled
```

## API Reference

### Global Variable

- **`req.EnableBufferPool`** (bool): Controls whether to use buffer pool for response body allocation. Default is `false`.

### Response Methods

- **`resp.ToBytes()`**: Returns response body as `[]byte`. When `EnableBufferPool` is true, memory is allocated from the buffer pool.

- **`resp.ReleaseBody()`**: Returns the response body buffer to the pool. Should be called when you're done with the response body to free memory. Only effective when `EnableBufferPool` is true and the body was allocated from the pool. After calling this, `resp.body` will be `nil` and subsequent calls to `Bytes()`, `String()`, etc. will return empty results.

- **`resp.SetBody(body []byte)`**: Sets response body. If the old body was from the buffer pool, it will be automatically released before setting the new body.

- **`resp.SetBodyString(body string)`**: Sets response body from string. If the old body was from the buffer pool, it will be automatically released before setting the new body.

## Memory Management

### Automatic Buffer Release

The buffer pool integration handles several scenarios automatically:

1. **Error during ToBytes()**: If an error occurs while reading the response body, the buffer is automatically returned to the pool.

2. **Retry Logic**: When a request is retried, the old response body buffer is automatically released before the retry attempt.

3. **SetBody/SetBodyString**: When you manually set the response body, any existing buffer from the pool is automatically released.

### Manual Buffer Release

When buffer pool is enabled, you should call `ReleaseBody()` when you're done with the response body:

```go
resp, err := client.R().Get("https://example.com")
if err != nil {
    log.Fatal(err)
}

body, err := resp.ToBytes()
if err != nil {
    log.Fatal(err)
}

// Use body...
processBody(body)

// Release when done
resp.ReleaseBody()
```

### Compatibility Considerations

The implementation ensures compatibility with existing code:

1. **Bodies not from pool**: If you use `SetBody()` or `SetBodyString()`, the body is marked as not from the pool, so `ReleaseBody()` won't affect it.

2. **Disabled buffer pool**: When `EnableBufferPool` is false (default), all buffer pool operations are no-ops, maintaining backward compatibility.

3. **Multiple calls**: Calling `ReleaseBody()` multiple times is safe - it only releases the buffer once.

## Performance Considerations

### When to Enable Buffer Pool

Enable buffer pool when:
- You have high-concurrency scenarios with many simultaneous requests
- Response bodies are relatively small to medium-sized
- You want to reduce GC pressure
- You can ensure proper cleanup with `ReleaseBody()`

### When to Keep Buffer Pool Disabled

Keep buffer pool disabled (default) when:
- You have low-concurrency scenarios
- Response bodies are very large
- You want simpler code without manual cleanup
- You're not experiencing memory pressure issues

## Implementation Details

### Buffer Allocation Strategy

When buffer pool is enabled:

1. If `Content-Length` header is present, allocate a buffer of that exact size
2. If `Content-Length` is unknown, start with a 512-byte buffer
3. If the response is larger than the initial buffer, grow the buffer as needed
4. Return the buffer to the pool when done

### Internal Tracking

The `Response` struct has an internal `fromBufferPool` field (unexported) that tracks whether the body was allocated from the pool. This ensures that:
- Only pool-allocated buffers are returned to the pool
- Manually set bodies (via `SetBody`/`SetBodyString`) are not returned to the pool
- The implementation is transparent to users

## Example

See `examples/buffer_pool_example.go` for a complete working example demonstrating all features.

## Migration Guide

### Existing Code

Your existing code continues to work without any changes:

```go
// This still works exactly as before
resp, err := client.R().Get("https://example.com")
body, _ := resp.ToBytes()
```

### Opt-in to Buffer Pool

To use buffer pool, just enable it and add cleanup:

```go
// Enable buffer pool
req.EnableBufferPool = true

resp, err := client.R().Get("https://example.com")
body, _ := resp.ToBytes()

// Add cleanup
defer resp.ReleaseBody()
```

## Thread Safety

The buffer pool from `github.com/libp2p/go-buffer-pool` is thread-safe and can be used safely in concurrent scenarios.
