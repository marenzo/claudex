package launcher

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Log keeps a bounded private log shared by foreground and automatic runs.
type Log struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func OpenLog(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Log{path: path, file: f, size: info.Size()}, nil
}
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(p)) > 2<<20 {
		if err := l.file.Close(); err != nil {
			return 0, err
		}
		for i := 2; i >= 1; i-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
		}
		_ = os.Rename(l.path, l.path+".1")
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return 0, err
		}
		l.file = f
		l.size = 0
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}
func (l *Log) Close() error { l.mu.Lock(); defer l.mu.Unlock(); return l.file.Close() }

var _ io.WriteCloser = (*Log)(nil)
