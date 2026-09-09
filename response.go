package req

import (
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"strings"
	"time"

	"github.com/imroc/req/v3/internal/header"
	"github.com/imroc/req/v3/internal/util"
	pool "github.com/libp2p/go-buffer-pool"
)

// ErrResponseBodyTooLarge is returned when a response body exceeds the limit
// configured via Client.SetMaxResponseSize or Request.SetMaxResponseSize.
// Use errors.Is(err, ErrResponseBodyTooLarge) or errors.As with
// *ResponseBodyTooLargeError for inspection.
var ErrResponseBodyTooLarge = errors.New("req: response body too large")

// ResponseBodyTooLargeError is returned when a response body exceeds the
// configured maximum size. Limit is the configured max in bytes. ContentLength
// is the response's Content-Length when the body was rejected based on headers
// without reading; it is -1 when the limit was exceeded while reading.
type ResponseBodyTooLargeError struct {
	Limit         int64
	ContentLength int64
}

func (e *ResponseBodyTooLargeError) Error() string {
	if e.ContentLength >= 0 {
		return fmt.Sprintf("req: response body too large: Content-Length %d exceeds limit %d", e.ContentLength, e.Limit)
	}
	return fmt.Sprintf("req: response body too large: exceeds limit of %d bytes", e.Limit)
}

// Is reports whether target is ErrResponseBodyTooLarge.
func (e *ResponseBodyTooLargeError) Is(target error) bool {
	return target == ErrResponseBodyTooLarge
}

// maxResponseBodyReader limits how many bytes can be read from a response body.
// It is similar in spirit to http.MaxBytesReader but for client response bodies:
// when the limit is exceeded it returns *ResponseBodyTooLargeError and subsequent
// reads return the same sticky error. Close closes the underlying body.
type maxResponseBodyReader struct {
	r     io.ReadCloser
	n     int64 // bytes remaining
	limit int64 // original limit, for the error
	err   error // sticky error
}

func (l *maxResponseBodyReader) Read(p []byte) (n int, err error) {
	if l.err != nil {
		return 0, l.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	// Read at most remaining+1 so we can detect crossing the limit.
	if int64(len(p))-1 > l.n {
		p = p[:l.n+1]
	}
	n, err = l.r.Read(p)

	if int64(n) <= l.n {
		l.n -= int64(n)
		l.err = err
		return n, err
	}

	n = int(l.n)
	l.n = 0
	l.err = &ResponseBodyTooLargeError{Limit: l.limit, ContentLength: -1}
	return n, l.err
}

func (l *maxResponseBodyReader) Close() error {
	return l.r.Close()
}

// Response is the http response.
type Response struct {
	// The underlying http.Response is embed into Response.
	*http.Response
	// Err is the underlying error, not nil if some error occurs.
	// Usually used in the ResponseMiddleware, you can skip logic in
	// ResponseMiddleware that doesn't need to be executed when err occurs.
	Err error
	// Request is the Response's related Request.
	Request        *Request
	body           []byte
	receivedAt     time.Time
	error          any
	result         any
	fromBufferPool bool // tracks if body was allocated from buffer pool
}

// IsSuccess method returns true if no error occurs and HTTP status `code >= 200 and <= 299`
// by default, you can also use Client.SetResultStateCheckFunc to customize the result
// state check logic.
//
// Deprecated: Use IsSuccessState instead.
func (r *Response) IsSuccess() bool {
	return r.IsSuccessState()
}

// IsSuccessState method returns true if no error occurs and HTTP status `code >= 200 and <= 299`
// by default, you can also use Client.SetResultStateCheckFunc to customize the result state
// check logic.
func (r *Response) IsSuccessState() bool {
	if r.Response == nil {
		return false
	}
	return r.ResultState() == SuccessState
}

// IsError method returns true if no error occurs and HTTP status `code >= 400`
// by default, you can also use Client.SetResultStateCheckFunc to customize the result
// state check logic.
//
// Deprecated: Use IsErrorState instead.
func (r *Response) IsError() bool {
	return r.IsErrorState()
}

// IsErrorState method returns true if no error occurs and HTTP status `code >= 400`
// by default, you can also use Client.SetResultStateCheckFunc to customize the result
// state check logic.
func (r *Response) IsErrorState() bool {
	if r.Response == nil {
		return false
	}
	return r.ResultState() == ErrorState
}

// GetContentType return the `Content-Type` header value.
func (r *Response) GetContentType() string {
	if r.Response == nil {
		return ""
	}
	return r.Header.Get(header.ContentType)
}

// ResultState returns the result state.
// By default, it returns SuccessState if HTTP status `code >= 200 && code <= 299`, and returns
// ErrorState if HTTP status `code >= 400`, otherwise returns UnknownState.
// You can also use Client.SetResultStateCheckFunc to customize the result
// state check logic.
func (r *Response) ResultState() ResultState {
	if r.Response == nil {
		return UnknownState
	}
	var resultStateCheckFunc func(resp *Response) ResultState
	if r.Request.client.resultStateCheckFunc != nil {
		resultStateCheckFunc = r.Request.client.resultStateCheckFunc
	} else {
		resultStateCheckFunc = defaultResultStateChecker
	}
	return resultStateCheckFunc(r)
}

// Result returns the automatically unmarshalled object if Request.SetSuccessResult
// is called and ResultState returns SuccessState.
// Otherwise, return nil.
//
// Deprecated: Use SuccessResult instead.
func (r *Response) Result() any {
	return r.SuccessResult()
}

// SuccessResult returns the automatically unmarshalled object if Request.SetSuccessResult
// is called and ResultState returns SuccessState.
// Otherwise, return nil.
func (r *Response) SuccessResult() any {
	return r.result
}

// Error returns the automatically unmarshalled object when Request.SetErrorResult
// or Client.SetCommonErrorResult is called, and ResultState returns ErrorState.
// Otherwise, return nil.
//
// Deprecated: Use ErrorResult instead.
func (r *Response) Error() any {
	return r.error
}

// ErrorResult returns the automatically unmarshalled object when Request.SetErrorResult
// or Client.SetCommonErrorResult is called, and ResultState returns ErrorState.
// Otherwise, return nil.
func (r *Response) ErrorResult() any {
	return r.error
}

// TraceInfo returns the TraceInfo from Request.
func (r *Response) TraceInfo() TraceInfo {
	return r.Request.TraceInfo()
}

// TotalTime returns the total time of the request, from request we sent to response we received.
func (r *Response) TotalTime() time.Duration {
	if r.Request.trace != nil {
		return r.Request.TraceInfo().TotalTime
	}
	if !r.receivedAt.IsZero() {
		return r.receivedAt.Sub(r.Request.StartTime)
	}
	return r.Request.responseReturnTime.Sub(r.Request.StartTime)
}

// ReceivedAt returns the timestamp that response we received.
func (r *Response) ReceivedAt() time.Time {
	return r.receivedAt
}

func (r *Response) setReceivedAt() {
	r.receivedAt = time.Now()
	if r.Request.trace != nil {
		r.Request.trace.endTime = r.receivedAt
	}
}

// UnmarshalJson unmarshalls JSON response body into the specified object.
func (r *Response) UnmarshalJson(v any) error {
	if r.Err != nil {
		return r.Err
	}
	b, err := r.ToBytes()
	if err != nil {
		return err
	}
	return r.Request.client.jsonUnmarshal(b, v)
}

// UnmarshalXml unmarshalls XML response body into the specified object.
func (r *Response) UnmarshalXml(v any) error {
	if r.Err != nil {
		return r.Err
	}
	b, err := r.ToBytes()
	if err != nil {
		return err
	}
	return r.Request.client.xmlUnmarshal(b, v)
}

// Unmarshal unmarshalls response body into the specified object according
// to response `Content-Type`.
func (r *Response) Unmarshal(v any) error {
	if r.Err != nil {
		return r.Err
	}
	// error by status-code
	if r.IsErrorState() {
		return errors.New(r.Status)
	}
	v = util.GetPointer(v)
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "json") {
		return r.UnmarshalJson(v)
	} else if strings.Contains(contentType, "xml") {
		return r.UnmarshalXml(v)
	}
	return r.UnmarshalJson(v)
}

// Into unmarshalls response body into the specified object according
// to response `Content-Type`.
func (r *Response) Into(v any) error {
	return r.Unmarshal(v)
}

// Set response body with byte array content
func (r *Response) SetBody(body []byte) {
	// Release old buffer if it was from pool
	if r.fromBufferPool && r.body != nil {
		pool.Put(r.body)
	}
	r.body = body
	r.fromBufferPool = false // New body is not from pool
}

// Set response body with string content
func (r *Response) SetBodyString(body string) {
	// Release old buffer if it was from pool
	if r.fromBufferPool && r.body != nil {
		pool.Put(r.body)
	}
	r.body = []byte(body)
	r.fromBufferPool = false // New body is not from pool
}

// Bytes return the response body as []bytes that have already been read, could be
// nil if not read, the following cases are already read:
//  1. `Request.SetResult` or `Request.SetError` is called.
//  2. `Client.DisableAutoReadResponse` and `Request.DisableAutoReadResponse` is not
//     called, and also `Request.SetOutput` and `Request.SetOutputFile` is not called.
func (r *Response) Bytes() []byte {
	return r.body
}

// String returns the response body as string that have already been read, could be
// nil if not read, the following cases are already read:
//  1. `Request.SetResult` or `Request.SetError` is called.
//  2. `Client.DisableAutoReadResponse` and `Request.DisableAutoReadResponse` is not
//     called, and also `Request.SetOutput` and `Request.SetOutputFile` is not called.
func (r *Response) String() string {
	return string(r.body)
}

// ToString returns the response body as string, read body if not have been read.
func (r *Response) ToString() (string, error) {
	b, err := r.ToBytes()
	return string(b), err
}

// maxBufferSize is the maximum buffer size we'll allocate from the pool.
// This prevents unbounded memory growth and pool pollution.
// Set to 16MB which is a reasonable limit for most HTTP responses.
const maxBufferSize = 32 * 1024 * 1024 // 16MB

// growSliceFromPool grows a slice to the next power-of-2 bucket size.
// It allocates a new slice from the pool, copies data, and returns the old slice to the pool.
// Returns the new slice and whether it's from the pool.
// This prevents pool bloat by aligning with go-buffer-pool's bucket sizes (powers of 2).
func growSliceFromPool(old []byte, minCap int, oldFromPool bool) ([]byte, bool) {
	oldCap := cap(old)
	// Calculate next power of 2 that fits minCap
	// go-buffer-pool uses buckets: 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048...
	newCap := oldCap
	if newCap == 0 {
		newCap = 64 // Start with a reasonable minimum
	}
	// Double capacity until it's large enough
	for newCap < minCap {
		newCap *= 2
	}

	// Limit maximum size to prevent pool pollution
	if newCap > maxBufferSize {
		// Fall back to standard allocation for very large buffers
		newSlice := make([]byte, len(old), newCap)
		copy(newSlice, old)
		// Return old buffer to pool ONLY if it was from pool
		if oldFromPool && oldCap > 0 && oldCap <= maxBufferSize {
			// Restore to full capacity before returning to pool
			pool.Put(old[:cap(old)])
		}
		// Return with explicit length to maintain invariant
		return newSlice[:len(old)], false // New slice is not from pool
	}
	// Allocate new buffer from pool
	newSlice := pool.Get(newCap)
	// Copy existing data
	copy(newSlice, old)
	// Return old buffer to pool ONLY if it was from pool
	if oldFromPool && oldCap > 0 {
		pool.Put(old[:cap(old)])
	}
	return newSlice[:len(old)], true // New slice is from pool
}

// ReadAll reads all data from the response body.
// It handles both buffer pool and standard io.ReadAll approaches.
// Returns the body bytes, whether the buffer is from pool, and any error.
func (r *Response) ReadAll() (body []byte, fromPool bool, err error) {
	if r.Request.client.EnableBufferPool {
		// Use buffer pool to allocate memory
		// Calculate initial capacity based on Content-Length
		initialCap := 512 // Default minimum
		if r.ContentLength > 0 {
			// Round up to next power of 2 to align with pool buckets
			initialCap = int(r.ContentLength)
			// Find next power of 2 using bits.Len
			// Handle edge case: if initialCap is 0, skip power-of-2 calculation
			if initialCap > 0 {
				// Calculate the position of the highest bit
				highBit := bits.Len(uint(initialCap)) - 1
				powerOf2 := 1 << highBit
				// If not exact power of 2, round up
				if powerOf2 < initialCap {
					initialCap = powerOf2 << 1
				} else {
					initialCap = powerOf2
				}
				// Ensure initialCap doesn't exceed maxBufferSize to prevent pool pollution
				if initialCap > maxBufferSize {
					initialCap = maxBufferSize
				}
			} else {
				// Content-Length is 0, use default
				initialCap = 512
			}
		}

		// For very large responses beyond pool limit, use standard allocation
		if r.ContentLength > maxBufferSize {
			// For very large responses, use standard allocation
			body, err = io.ReadAll(r.Body)
			return
		}

		// Ensure initialCap is at least the minimum
		if initialCap < 64 {
			initialCap = 64
		}

		body = pool.Get(initialCap)
		body = body[:0] // Set length to 0, keep capacity
		fromPool = true // Track that initial buffer is from pool

		// Read data in chunks
		for {
			if len(body) == cap(body) {
				// Buffer is full, need to grow
				// Check if we've hit the absolute limit
				body, fromPool = growSliceFromPool(body, cap(body)+1, fromPool)
				// Note: Once fromPool becomes false (after exceeding maxBufferSize),
				// it stays false for the rest of the operation. This ensures we never
				// try to Put a non-pool slice back to the pool.
			}
			// Extend slice to full capacity for reading
			tmp := body[len(body):cap(body)]
			n, readErr := r.Body.Read(tmp)
			body = body[:len(body)+n]
			if readErr != nil {
				if readErr == io.EOF {
					err = nil
				} else {
					err = readErr
				}
				break
			}
		}

		if err != nil {
			// On error, return buffer to pool ONLY if it's from pool
			if fromPool && cap(body) > 0 && cap(body) <= maxBufferSize {
				pool.Put(body[:cap(body)])
			}
			body = nil
			fromPool = false
		}
		// If successful, fromPool already has the correct value
	} else {
		// Original behavior: use io.ReadAll
		body, err = io.ReadAll(r.Body)
	}
	return
}

// ToBytes returns the response body as []byte, read body if not have been read.
func (r *Response) ToBytes() (body []byte, err error) {
	if r.Err != nil {
		return nil, r.Err
	}
	if r.body != nil {
		return r.body, nil
	}
	if r.Response == nil || r.Response.Body == nil {
		return []byte{}, nil
	}
	defer func() {
		r.Body.Close()
		if err != nil {
			r.Err = err
			// Release buffer on error if it was allocated from pool
			if r.fromBufferPool && body != nil {
				pool.Put(body)
				body = nil
				r.fromBufferPool = false
			}
		}
		r.body = body
	}()

	body, r.fromBufferPool, err = r.ReadAll()

	r.setReceivedAt()
	if err == nil && r.Request.client.responseBodyTransformer != nil {
		if r.fromBufferPool {
			// Copy the buffer pool slice before passing to transformer
			bodyCopy := make([]byte, len(body))
			copy(bodyCopy, body)
			// Return original buffer to pool
			pool.Put(body)
			r.fromBufferPool = false
			// Pass the copy to transformer
			body, err = r.Request.client.responseBodyTransformer(bodyCopy, r.Request, r)
		} else {
			body, err = r.Request.client.responseBodyTransformer(body, r.Request, r)
		}
	}
	return
}

// Dump return the string content that have been dumped for the request.
// `Request.Dump` or `Request.DumpXXX` MUST have been called.
func (r *Response) Dump() string {
	return r.Request.getDumpBuffer().String()
}

// GetStatus returns the response status.
func (r *Response) GetStatus() string {
	if r.Response == nil {
		return ""
	}
	return r.Status
}

// GetStatusCode returns the response status code.
func (r *Response) GetStatusCode() int {
	if r.Response == nil {
		return 0
	}
	return r.StatusCode
}

// GetHeader returns the response header value by key.
func (r *Response) GetHeader(key string) string {
	if r.Response == nil {
		return ""
	}
	return r.Header.Get(key)
}

// GetHeaderValues returns the response header values by key.
func (r *Response) GetHeaderValues(key string) []string {
	if r.Response == nil {
		return nil
	}
	return r.Header.Values(key)
}

// HeaderToString get all header as string.
func (r *Response) HeaderToString() string {
	if r.Response == nil {
		return ""
	}
	return convertHeaderToString(r.Header)
}

// ReleaseBody returns the response body buffer to the buffer pool.
// This should be called when you're done with the response body to free memory.
// Only effective when EnableBufferPool is true and the body was allocated from the pool.
// After calling this, r.body will be nil and subsequent calls to Bytes(), String(), etc.
// will return empty results.
func (r *Response) ReleaseBody() {
	if r.fromBufferPool && r.body != nil {
		pool.Put(r.body)
		r.body = nil
		r.fromBufferPool = false
	} else {
		r.body = nil
	}
}
