package gitrepo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v6/plumbing/format/pktline"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
	"golang.org/x/crypto/ssh"
)

// Pure ssh lfs transfer protocol, pkt-line framed commands and responses
func (l *LFS) ServeSSHTransfer(ctx context.Context, ch ssh.Channel, repo *db.GitRepository, user *auth.AuthenticatedUser, operation string) int {
	rd := bufio.NewReader(ch)
	w := ch
	canWrite := operation == "upload"
	status := func(code int, args ...string) error {
		if _, err := pktline.WriteString(w, fmt.Sprintf("status %d\n", code)); err != nil {
			return err
		}
		for _, a := range args {
			if _, err := pktline.WriteString(w, a+"\n"); err != nil {
				return err
			}
		}
		return pktline.WriteFlush(w)
	}
	readPacket := func() (string, bool, error) {
		l, p, err := pktline.ReadLine(rd)
		if err != nil {
			return "", false, err
		}
		if l == pktline.Flush {
			return "", true, nil
		}
		return strings.TrimSuffix(string(p), "\n"), false, nil
	}
	// Reads argument lines until a flush
	readArgs := func() (map[string]string, error) {
		args := map[string]string{}
		for {
			line, flush, err := readPacket()
			if err != nil {
				return nil, err
			}
			if flush {
				return args, nil
			}
			k, v, _ := strings.Cut(line, "=")
			args[k] = v
		}
	}
	readLines := func() ([]string, error) {
		var out []string
		for {
			line, flush, err := readPacket()
			if err != nil {
				return nil, err
			}
			if flush {
				return out, nil
			}
			out = append(out, line)
		}
	}
	for {
		line, flush, err := readPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			return 1
		}
		if flush {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "version":
			if len(fields) < 2 || fields[1] != "1" {
				_ = status(400, "unsupported version")
				continue
			}
			_ = status(200)
		case "quit":
			_ = status(200)
			return 0
		case "batch":
			args, err := readArgs()
			if err != nil {
				return 1
			}
			objects, err := readLines()
			if err != nil {
				return 1
			}
			if algo := args["hash-algo"]; algo != "" && algo != "sha256" {
				_ = status(409, "unsupported hash algorithm")
				continue
			}
			var out []string
			for _, o := range objects {
				parts := strings.Fields(o)
				if len(parts) < 2 || !oidPattern.MatchString(parts[0]) {
					continue
				}
				oid := parts[0]
				size, _ := strconv.ParseInt(parts[1], 10, 64)
				existing, _ := l.manager.Store().GetLFSObject(ctx, repo.ID, oid)
				switch {
				case canWrite && existing == nil:
					out = append(out, fmt.Sprintf("%s %d upload", oid, size))
				case canWrite:
					out = append(out, fmt.Sprintf("%s %d noop", oid, size))
				case existing != nil:
					out = append(out, fmt.Sprintf("%s %d download", oid, existing.Size))
				default:
					out = append(out, fmt.Sprintf("%s %d noop", oid, size))
				}
			}
			if _, err := pktline.WriteString(w, "status 200\n"); err != nil {
				return 1
			}
			if err := pktline.WriteFlush(w); err != nil {
				return 1
			}
			for _, o := range out {
				if _, err := pktline.WriteString(w, o+"\n"); err != nil {
					return 1
				}
			}
			if err := pktline.WriteFlush(w); err != nil {
				return 1
			}
		case "get-object":
			if len(fields) < 2 {
				_ = status(400, "missing oid")
				continue
			}
			if _, err := readArgs(); err != nil {
				return 1
			}
			oid := fields[1]
			obj, _ := l.manager.Store().GetLFSObject(ctx, repo.ID, oid)
			if obj == nil {
				_ = status(404, "object not found")
				continue
			}
			f, info, err := l.manager.Blobs().OpenBlob("sha256:" + oid)
			if err != nil {
				_ = status(404, "object bytes missing")
				continue
			}
			if _, err := pktline.WriteString(w, "status 200\n"); err != nil {
				f.Close()
				return 1
			}
			if _, err := pktline.WriteString(w, fmt.Sprintf("size=%d\n", info.Size())); err != nil {
				f.Close()
				return 1
			}
			if err := pktline.WriteFlush(w); err != nil {
				f.Close()
				return 1
			}
			if err := writePktData(w, f); err != nil {
				f.Close()
				return 1
			}
			f.Close()
			if err := pktline.WriteFlush(w); err != nil {
				return 1
			}
		case "put-object":
			if len(fields) < 2 || !oidPattern.MatchString(fields[1]) {
				_ = status(400, "invalid oid")
				continue
			}
			if !canWrite {
				_ = status(403, "read only transfer")
				continue
			}
			args, err := readArgs()
			if err != nil {
				return 1
			}
			size, _ := strconv.ParseInt(args["size"], 10, 64)
			maxBytes := l.manager.gitSettings(ctx, repo).GetMaxLfsObjectMb() * 1024 * 1024
			tooLarge := fmt.Sprintf("object exceeds the %d MB limit", maxBytes/(1024*1024))
			if maxBytes > 0 && size > maxBytes {
				if err := drainPktData(rd); err != nil {
					return 1
				}
				_ = status(413, tooLarge)
				continue
			}
			blobs := l.manager.Blobs()
			uploadID, err := blobs.InitiateUpload()
			if err != nil {
				_ = status(500, "upload failed")
				continue
			}
			if err := readPktData(rd, uploadID, blobs.AppendChunk, maxBytes); err != nil {
				_ = blobs.CancelUpload(uploadID)
				if errors.Is(err, errLFSTooLarge) {
					_ = status(413, tooLarge)
					continue
				}
				return 1
			}
			digest, gotSize, _, err := blobs.CompleteUpload(uploadID)
			if err != nil || digest != "sha256:"+fields[1] || (size > 0 && gotSize != size) {
				if err == nil {
					l.dropUnreferenced(ctx, digest)
				}
				_ = status(400, "object content does not match")
				continue
			}
			if err := l.manager.Store().UpsertLFSObject(ctx, repo.ID, fields[1], gotSize); err != nil {
				_ = status(500, "could not record object")
				continue
			}
			_ = status(200)
		case "verify-object":
			if len(fields) < 2 {
				_ = status(400, "missing oid")
				continue
			}
			args, err := readArgs()
			if err != nil {
				return 1
			}
			size, _ := strconv.ParseInt(args["size"], 10, 64)
			obj, _ := l.manager.Store().GetLFSObject(ctx, repo.ID, fields[1])
			switch {
			case obj == nil:
				_ = status(404, "object not found")
			case size > 0 && obj.Size != size:
				_ = status(409, "size mismatch")
			default:
				_ = status(200)
			}
		case "lock":
			args, err := readArgs()
			if err != nil {
				return 1
			}
			if !canWrite {
				_ = status(403, "read only transfer")
				continue
			}
			path := args["path"]
			if path == "" {
				_ = status(400, "path is required")
				continue
			}
			if existing, _, _ := l.manager.Store().ListLFSLocks(ctx, repo.ID, path, "", 1, ""); len(existing) > 0 {
				lk := existing[0]
				_ = status(409, "id="+lk.ID, "path="+lk.Path, "locked-at="+lk.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), "ownername="+l.ownerName(ctx, lk.OwnerID), "conflict")
				continue
			}
			lk := &db.LFSLock{RepoID: repo.ID, Path: path, OwnerID: user.ID}
			if err := l.manager.Store().CreateLFSLock(ctx, lk); err != nil {
				_ = status(500, "could not create lock")
				continue
			}
			_ = status(201, "id="+lk.ID, "path="+lk.Path, "locked-at="+lk.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), "ownername="+user.Username)
		case "list-lock":
			args, err := readArgs()
			if err != nil {
				return 1
			}
			limit, _ := strconv.Atoi(args["limit"])
			locks, next, err := l.manager.Store().ListLFSLocks(ctx, repo.ID, args["path"], args["id"], limit, args["cursor"])
			if err != nil {
				_ = status(500, "could not list locks")
				continue
			}
			if _, err := pktline.WriteString(w, "status 200\n"); err != nil {
				return 1
			}
			if next != "" {
				if _, err := pktline.WriteString(w, "next-cursor="+next+"\n"); err != nil {
					return 1
				}
			}
			if err := pktline.WriteFlush(w); err != nil {
				return 1
			}
			for _, lk := range locks {
				who := "theirs"
				if lk.OwnerID == user.ID {
					who = "ours"
				}
				line := fmt.Sprintf("lock %s path=%s locked-at=%s ownername=%s owner=%s\n", lk.ID, lk.Path, lk.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), l.ownerName(ctx, lk.OwnerID), who)
				if _, err := pktline.WriteString(w, line); err != nil {
					return 1
				}
			}
			if err := pktline.WriteFlush(w); err != nil {
				return 1
			}
		case "unlock":
			if len(fields) < 2 {
				_ = status(400, "missing lock id")
				continue
			}
			args, err := readArgs()
			if err != nil {
				return 1
			}
			if !canWrite {
				_ = status(403, "read only transfer")
				continue
			}
			lk, _ := l.manager.Store().GetLFSLock(ctx, repo.ID, fields[1])
			if lk == nil {
				_ = status(404, "lock not found")
				continue
			}
			if lk.OwnerID != user.ID && (args["force"] != "true" || !l.manager.Access().Can(ctx, user, repo, db.GitUnitCode, rbac.ActionUpdate)) {
				_ = status(403, "lock belongs to another user")
				continue
			}
			if err := l.manager.Store().DeleteLFSLock(ctx, repo.ID, lk.ID); err != nil {
				_ = status(500, "could not delete lock")
				continue
			}
			_ = status(200, "id="+lk.ID, "path="+lk.Path, "locked-at="+lk.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), "ownername="+l.ownerName(ctx, lk.OwnerID))
		default:
			_ = status(405, "unknown command")
		}
	}
}

func (l *LFS) ownerName(ctx context.Context, id string) string {
	if u, err := l.manager.Store().GetUserByID(ctx, id); err == nil && u != nil {
		return u.Username
	}
	return id
}

func (l *LFS) dropUnreferenced(ctx context.Context, digest string) {
	oid := strings.TrimPrefix(digest, "sha256:")
	if count, _ := l.manager.Store().CountArtifactsByDigest(ctx, digest); count > 0 {
		return
	}
	if links, _ := l.manager.Store().CountLFSLinksByOID(ctx, oid); links > 0 {
		return
	}
	_ = l.manager.Blobs().DeleteBlob(digest)
}

// Streams reader bytes as data pkt-lines
func writePktData(w io.Writer, r io.Reader) error {
	buf := make([]byte, 65516)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := pktline.Write(w, buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Reads data pkt-lines until a flush, appending to an upload session
// Object data passed the configured cap
var errLFSTooLarge = errors.New("lfs object too large")

// Streams data packets into the upload, draining the rest once the cap is passed
func readPktData(rd *bufio.Reader, uploadID string, appendChunk func(string, io.Reader) (int64, error), maxBytes int64) error {
	var total int64
	over := false
	for {
		l, p, err := pktline.ReadLine(rd)
		if err != nil {
			return err
		}
		if l == pktline.Flush {
			if over {
				return errLFSTooLarge
			}
			return nil
		}
		if over {
			continue
		}
		total += int64(len(p))
		if maxBytes > 0 && total > maxBytes {
			over = true
			continue
		}
		if _, err := appendChunk(uploadID, strings.NewReader(string(p))); err != nil {
			return err
		}
	}
}

// Discards data packets up to the flush that ends them
func drainPktData(rd *bufio.Reader) error {
	for {
		l, _, err := pktline.ReadLine(rd)
		if err != nil {
			return err
		}
		if l == pktline.Flush {
			return nil
		}
	}
}
