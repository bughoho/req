package req

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/imroc/req/v3/internal/header"
	"github.com/imroc/req/v3/internal/util"
	pool "github.com/libp2p/go-buffer-pool"
)

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

// ReadAll reads all data from the response body.
// It handles both buffer pool and standard io.ReadAll approaches.
// Returns the body bytes, whether the buffer is from pool, and any error.
func (r *Response) ReadAll() (body []byte, fromPool bool, err error) {
	if r.Request.client.EnableBufferPool {
		// Use buffer pool to allocate memory
		// First, try to get content length
		contentLength := r.ContentLength
		if contentLength > 0 {
			body = pool.Get(int(contentLength))
		} else {
			// Default initial size if content length is unknown
			body = pool.Get(512)
		}

		var n int
		n, err = io.ReadFull(r.Body, body)
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			// Body is smaller than allocated buffer
			body = body[:n]
			err = nil
		} else if err == nil && n == len(body) {
			// Buffer might be too small, need to read more
			var extra []byte
			extra, err = io.ReadAll(r.Body)
			if err == nil && len(extra) > 0 {
				// Need to allocate larger buffer
				newBody := pool.Get(len(body) + len(extra))
				copy(newBody, body)
				copy(newBody[len(body):], extra)
				pool.Put(body) // Return old buffer
				body = newBody
			}
		}

		if err == nil {
			fromPool = true
		} else {
			// On error, return buffer to pool
			pool.Put(body)
			body = nil
		}
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
