package compress

import (
	"compress/gzip"
	"io"
	"io/fs"
	"sync"
)

// gzipReaderPool is a pool of gzip.Reader to reduce GC pressure
var gzipReaderPool sync.Pool

// GzipReader wraps a response body so it can lazily
// call gzip.NewReader on the first call to Read
type GzipReader struct {
	Body io.ReadCloser // underlying Response.Body
	zr   *gzip.Reader  // lazily-initialized gzip reader
	zerr error         // sticky error
}

func NewGzipReader(body io.ReadCloser) *GzipReader {
	return &GzipReader{Body: body}
}

func (gz *GzipReader) Read(p []byte) (n int, err error) {
	if gz.zerr != nil {
		return 0, gz.zerr
	}
	if gz.zr == nil {
		// Try to get a gzip.Reader from the pool
		if v := gzipReaderPool.Get(); v != nil {
			gz.zr = v.(*gzip.Reader)
			err = gz.zr.Reset(gz.Body)
		} else {
			gz.zr, err = gzip.NewReader(gz.Body)
		}
		if err != nil {
			gz.zerr = err
			return 0, err
		}
	}
	return gz.zr.Read(p)
}

func (gz *GzipReader) Close() error {
	// Return the gzip.Reader to the pool for reuse
	if gz.zr != nil {
		gz.zr.Close()
		gzipReaderPool.Put(gz.zr)
		gz.zr = nil
	}
	if err := gz.Body.Close(); err != nil {
		return err
	}
	gz.zerr = fs.ErrClosed
	return nil
}

func (gz *GzipReader) GetUnderlyingBody() io.ReadCloser {
	return gz.Body
}

func (gz *GzipReader) SetUnderlyingBody(body io.ReadCloser) {
	gz.Body = body
}
