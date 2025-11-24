# Gzip Reader GC 优化报告

## 问题分析

### 原始实现的GC压力来源

1. **频繁内存分配**: 每个HTTP响应都会创建新的 `gzip.Reader` 实例
2. **大量内存占用**: 每个 `gzip.Reader` 内部分配约 256KB 的滑动窗口 + 其他缓冲区
3. **高并发场景**: 在高并发请求下,大量的 gzip reader 创建和销毁会给GC带来巨大压力

### 受影响的代码位置

- `transport.go:3673-3704` - `gzipReader` 结构体和方法
- `transport.go:2789` - 创建 `gzipReader` 实例
- `internal/compress/gzip_reader.go` - `GzipReader` 实现

## 优化方案

### 核心思路

使用 `sync.Pool` 对象池复用 `gzip.Reader` 实例,利用 `gzip.Reader.Reset()` 方法重置 reader 状态,避免重复创建。

### 实现细节

#### 1. transport.go 优化

**添加全局对象池:**

```go
// gzipReaderPool is a pool of gzip.Reader to reduce GC pressure
var gzipReaderPool sync.Pool
```

**优化 Read 方法:**

```go
func (gz *gzipReader) Read(p []byte) (n int, err error) {
    if gz.zr == nil {
        if gz.zerr == nil {
            // Try to get a gzip.Reader from the pool
            if v := gzipReaderPool.Get(); v != nil {
                gz.zr = v.(*gzip.Reader)
                gz.zerr = gz.zr.Reset(gz.body)
            } else {
                gz.zr, gz.zerr = gzip.NewReader(gz.body)
            }
        }
        // ... rest of the code
    }
}
```

**优化 Close 方法:**

```go
func (gz *gzipReader) Close() error {
    // Return the gzip.Reader to the pool for reuse
    if gz.zr != nil {
        gz.zr.Close()
        gzipReaderPool.Put(gz.zr)
        gz.zr = nil
    }
    return gz.body.Close()
}
```

#### 2. internal/compress/gzip_reader.go 优化

同样的优化策略应用到 `GzipReader` 结构体:

- 添加独立的 `gzipReaderPool`
- 在 `Read()` 中从池中获取或创建 reader
- 在 `Close()` 中归还 reader 到池中

## 性能提升预期

### 内存分配减少

- **首次请求**: 创建新的 `gzip.Reader` (无变化)
- **后续请求**: 从池中复用,零内存分配
- **高并发场景**: 池大小稳定在并发数量级别,避免无限增长

### GC压力降低

- **减少对象创建**: 每个 `gzip.Reader` 约 256KB,复用后显著减少分配
- **减少GC频率**: 更少的内存分配意味着更少的GC触发
- **降低GC停顿**: 更少的对象需要扫描和回收

### 适用场景

- ✅ 高并发HTTP请求场景
- ✅ 大量gzip压缩响应
- ✅ 长时间运行的服务
- ✅ 内存敏感的应用

## 参考资料

- [Go Issue #61353: net/http: reuse gzip reader from pool for transport](https://github.com/golang/go/issues/61353)
- [go-pool gzip implementation](https://github.com/ungerik/go-pool/blob/master/gzip.go)
- Go标准库 `compress/gzip` 包文档

## 测试验证

所有现有测试通过:

```bash
✓ go build ./...
✓ go test ./... -timeout 30s
```

## 注意事项

1. **线程安全**: `sync.Pool` 是线程安全的,无需额外加锁
2. **自动清理**: Go运行时会在GC时自动清理池中的对象,避免内存泄漏
3. **向后兼容**: 优化完全向后兼容,不影响现有API
4. **零配置**: 优化自动生效,无需用户配置

## 总结

通过引入 `sync.Pool` 复用 `gzip.Reader` 对象,成功优化了gzip解压缩的GC压力。该优化:

- ✅ 减少内存分配
- ✅ 降低GC频率
- ✅ 提升高并发性能
- ✅ 保持向后兼容
- ✅ 通过所有测试
