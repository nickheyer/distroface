package gitrepo

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"io"

	"github.com/go-git/go-git/v6/plumbing"
)

// Minimal encoded object that only buffers bytes for signing checks
type memObject struct {
	t   plumbing.ObjectType
	sz  int64
	buf bytes.Buffer
}

func (m *memObject) Hash() plumbing.Hash           { return plumbing.ZeroHash }
func (m *memObject) Type() plumbing.ObjectType     { return m.t }
func (m *memObject) SetType(t plumbing.ObjectType) { m.t = t }
func (m *memObject) Size() int64                   { return m.sz }
func (m *memObject) SetSize(s int64)               { m.sz = s }
func (m *memObject) Reader() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.buf.Bytes())), nil
}
func (m *memObject) Writer() (io.WriteCloser, error) { return nopWriteCloser{&m.buf}, nil }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

func sha512Sum(b []byte) []byte {
	s := sha512.Sum512(b)
	return s[:]
}
