package gitrepo

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/go-git/go-git/v6/plumbing/format/pktline"
)

func pktStream(t *testing.T, chunks ...string) *bufio.Reader {
	t.Helper()
	var buf bytes.Buffer
	for _, c := range chunks {
		if _, err := pktline.Write(&buf, []byte(c)); err != nil {
			t.Fatalf("pktline.Write: %v", err)
		}
	}
	if err := pktline.WriteFlush(&buf); err != nil {
		t.Fatalf("pktline.WriteFlush: %v", err)
	}
	buf.WriteString("tail")
	return bufio.NewReader(&buf)
}

func TestReadPktDataStreams(t *testing.T) {
	rd := pktStream(t, "hello", " ", "world")
	var got bytes.Buffer
	if err := readPktData(rd, &got, 0); err != nil {
		t.Fatalf("readPktData: %v", err)
	}
	if got.String() != "hello world" {
		t.Fatalf("data %q", got.String())
	}
	if rest, _ := io.ReadAll(rd); string(rest) != "tail" {
		t.Fatalf("stream position wrong, rest %q", rest)
	}
}

func TestReadPktDataTooLargeDrains(t *testing.T) {
	rd := pktStream(t, "abc", "def", "ghi")
	var got bytes.Buffer
	err := readPktData(rd, &got, 4)
	if !errors.Is(err, errLFSTooLarge) {
		t.Fatalf("want errLFSTooLarge, got %v", err)
	}
	if got.String() != "abc" {
		t.Fatalf("bytes past the cap were appended: %q", got.String())
	}
	if rest, _ := io.ReadAll(rd); string(rest) != "tail" {
		t.Fatalf("oversized object not drained to the flush, rest %q", rest)
	}
}

func TestReadPktDataTruncated(t *testing.T) {
	var buf bytes.Buffer
	_, _ = pktline.Write(&buf, []byte("abc"))
	rd := bufio.NewReader(&buf)
	if err := readPktData(rd, io.Discard, 0); err == nil {
		t.Fatal("truncated stream must fail")
	}
}
