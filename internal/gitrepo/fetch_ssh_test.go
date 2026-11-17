package gitrepo

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testSSHKeyPEM(t *testing.T) ([]byte, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), signer
}

// Hosts without any known_hosts file must still connect
func TestSSHAuthNeedsNoKnownHostsFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SSH_KNOWN_HOSTS", "")

	_, hostSigner := testSSHKeyPEM(t)
	unknown, _ := testSSHKeyPEM(t)
	trusted, trustedSigner := testSSHKeyPEM(t)
	want := string(trustedSigner.PublicKey().Marshal())

	accepted := make(chan string, 1)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) != want {
				return nil, ssh.ErrNoAuth
			}
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc, _, _, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			accepted <- "handshake failed: " + err.Error()
			return
		}
		accepted <- sc.User()
		sc.Close()
	}()

	var pinned string
	auth := RemoteAuth{SSHKeys: [][]byte{unknown, trusted}, OnHostKey: func(fp string) { pinned = fp }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = listRemote(ctx, "ssh://deploy@"+ln.Addr().String()+"/owner/repo.git", auth.Options(newTestEngine(t)), 10*time.Second)
	if err != nil && strings.Contains(err.Error(), "known_hosts") {
		t.Fatalf("known_hosts lookup still required: %v", err)
	}
	select {
	case user := <-accepted:
		if user != "deploy" {
			t.Fatalf("server saw %q, want user deploy authenticated by the second key", user)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server never saw a connection")
	}
	if pinned != ssh.FingerprintSHA256(hostSigner.PublicKey()) {
		t.Fatalf("host key not reported for pinning, got %q", pinned)
	}
}
