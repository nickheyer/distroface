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

func TestReadPktDataSingleAppend(t *testing.T) {
	rd := pktStream(t, "hello", " ", "world")
	calls, got := 0, []byte(nil)
	appendChunk := func(_ string, r io.Reader) (int64, error) {
		calls++
		b, err := io.ReadAll(r)
		got = append(got, b...)
		return int64(len(b)), err
	}
	if err := readPktData(rd, "u", appendChunk, 0); err != nil {
		t.Fatalf("readPktData: %v", err)
	}
	if calls != 1 || string(got) != "hello world" {
		t.Fatalf("calls %d data %q", calls, got)
	}
	if rest, _ := io.ReadAll(rd); string(rest) != "tail" {
		t.Fatalf("stream position wrong, rest %q", rest)
	}
}

func TestReadPktDataTooLargeDrains(t *testing.T) {
	rd := pktStream(t, "abc", "def", "ghi")
	var got []byte
	appendChunk := func(_ string, r io.Reader) (int64, error) {
		b, err := io.ReadAll(r)
		got = b
		return int64(len(b)), err
	}
	err := readPktData(rd, "u", appendChunk, 4)
	if !errors.Is(err, errLFSTooLarge) {
		t.Fatalf("want errLFSTooLarge, got %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("bytes past the cap were appended: %q", got)
	}
	if rest, _ := io.ReadAll(rd); string(rest) != "tail" {
		t.Fatalf("oversized object not drained to the flush, rest %q", rest)
	}
}

func TestReadPktDataTruncated(t *testing.T) {
	var buf bytes.Buffer
	_, _ = pktline.Write(&buf, []byte("abc"))
	rd := bufio.NewReader(&buf)
	appendChunk := func(_ string, r io.Reader) (int64, error) {
		b, err := io.ReadAll(r)
		return int64(len(b)), err
	}
	if err := readPktData(rd, "u", appendChunk, 0); err == nil {
		t.Fatal("truncated stream must fail")
	}
}
