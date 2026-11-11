// Package netpipe provides a bounded bidirectional copy between two streams.
package netpipe

import (
	"io"
	"net"
	"sync"
	"time"
)

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

// Pipe copies a<->b until both directions finish or a short grace period after
// the first direction closes. Half-close is used when the destination supports
// it, which keeps protocols such as HTTP/1.1 and many plain TCP protocols
// correct while still guaranteeing teardown.
func Pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	copySide := func(dst, src net.Conn) {
		bufp := bufPool.Get().(*[]byte)
		_, _ = io.CopyBuffer(dst, src, *bufp)
		bufPool.Put(bufp)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go copySide(b, a)
	go copySide(a, b)
	<-done
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	_ = a.Close()
	_ = b.Close()
}
