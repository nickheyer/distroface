package mailer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomail "github.com/emersion/go-message/mail"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Mailbox that collects replies that could not be posted
const failedMailbox = "Failed"

// Polls the configured mailbox for replies and posts them as comments
func (s *Service) runInbound(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			in := s.res.System(ctx).GetMail().GetIncoming()
			if !in.GetEnabled() || in.GetImapHost() == "" || s.inbound == nil {
				continue
			}
			if err := s.pollOnce(ctx, in); err != nil {
				s.log.Error("mail: inbound poll: %v", err)
			}
		}
	}
}

// Connects, processes unseen messages, and disconnects
func (s *Service) pollOnce(ctx context.Context, in *v1.MailIncomingSettings) error {
	port := int(in.GetImapPort())
	if port <= 0 {
		port = 993
	}
	addr := fmt.Sprintf("%s:%d", in.GetImapHost(), port)
	opts := &imapclient.Options{TLSConfig: &tls.Config{ServerName: in.GetImapHost(), MinVersion: tls.VersionTLS12}}
	var c *imapclient.Client
	var err error
	if in.GetTls() {
		c, err = imapclient.DialTLS(addr, opts)
	} else {
		c, err = imapclient.DialStartTLS(addr, opts)
		if err != nil {
			c, err = imapclient.DialInsecure(addr, opts)
		}
	}
	if err != nil {
		return fmt.Errorf("connect %s: %w", addr, err)
	}
	defer c.Close()
	if err := c.Login(in.GetUsername(), in.GetPassword()).Wait(); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer func() { _ = c.Logout().Wait() }()
	mailbox := in.GetMailbox()
	if mailbox == "" {
		mailbox = "INBOX"
	}
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		return fmt.Errorf("select %s: %w", mailbox, err)
	}
	found, err := c.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	uids := found.AllUIDs()
	if len(uids) == 0 {
		return nil
	}
	section := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, Envelope: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	var failed []imap.UID
	seen := []imap.UID{}
	for _, m := range msgs {
		seen = append(seen, m.UID)
		if err := s.handleMessage(ctx, in, m, section); err != nil {
			s.log.Warn("mail: inbound message %d: %v", m.UID, err)
			failed = append(failed, m.UID)
		}
	}
	if len(seen) > 0 {
		_ = c.Store(imap.UIDSetNum(seen...), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}, Silent: true}, nil).Close()
	}
	if len(failed) > 0 {
		_ = c.Create(failedMailbox, nil).Wait()
		if _, err := c.Move(imap.UIDSetNum(failed...), failedMailbox).Wait(); err != nil {
			s.log.Warn("mail: moving %d failed replies: %v", len(failed), err)
		}
	}
	return nil
}

// Posts one mailed reply through the installed handler
func (s *Service) handleMessage(ctx context.Context, in *v1.MailIncomingSettings, m *imapclient.FetchMessageBuffer, section *imap.FetchItemBodySection) error {
	raw := m.FindBodySection(section)
	if len(raw) == 0 {
		return fmt.Errorf("empty body")
	}
	token := ""
	if m.Envelope != nil {
		for _, a := range m.Envelope.To {
			if t := replyToken(a.Mailbox+"@"+a.Host, in.GetReplyAddress()); t != "" {
				token = t
				break
			}
		}
	}
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if token == "" {
		for _, h := range []string{"Delivered-To", "To", "X-Original-To", "Envelope-To"} {
			for _, v := range mr.Header.Values(h) {
				for _, part := range strings.Split(v, ",") {
					if t := replyToken(extractAddress(part), in.GetReplyAddress()); t != "" {
						token = t
					}
				}
			}
		}
	}
	if token == "" {
		return fmt.Errorf("no reply token in recipients")
	}
	sum := sha256.Sum256([]byte(token))
	row, err := s.store.GetMailInboundToken(ctx, hex.EncodeToString(sum[:]))
	if err != nil {
		return err
	}
	if row == nil || row.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("unknown or expired reply token")
	}
	from := ""
	if m.Envelope != nil && len(m.Envelope.From) > 0 {
		from = m.Envelope.From[0].Mailbox + "@" + m.Envelope.From[0].Host
	}
	body := textPart(mr)
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("no text part")
	}
	return s.inbound(ctx, row, from, body)
}

// Token carried by a plus addressed reply address
func replyToken(addr, replyAddress string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	local, domain, ok := strings.Cut(strings.ToLower(replyAddress), "@")
	if !ok || addr == "" {
		return ""
	}
	prefix := local + "+"
	suffix := "@" + domain
	if !strings.HasPrefix(addr, prefix) || !strings.HasSuffix(addr, suffix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(addr, prefix), suffix)
}

// Bare address out of a header value such as Name <addr>
func extractAddress(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.LastIndex(v, "<"); i >= 0 {
		if j := strings.Index(v[i:], ">"); j > 0 {
			return v[i+1 : i+j]
		}
	}
	return v
}

// First text/plain part, falling back to stripped html
func textPart(mr *gomail.Reader) string {
	html := ""
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		h, ok := p.Header.(*gomail.InlineHeader)
		if !ok {
			continue
		}
		ct, _, _ := h.ContentType()
		b, _ := io.ReadAll(io.LimitReader(p.Body, 1<<20))
		switch ct {
		case "text/plain":
			return string(b)
		case "text/html":
			if html == "" {
				html = stripTags(string(b))
			}
		}
	}
	return html
}

// Drops html tags keeping line breaks for paragraphs and breaks
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '<':
			inTag = true
			rest := strings.ToLower(s[i:min(i+4, len(s))])
			if strings.HasPrefix(rest, "<br") || strings.HasPrefix(rest, "<p") || strings.HasPrefix(rest, "<div") {
				b.WriteByte('\n')
			}
		case c == '>':
			inTag = false
		case !inTag:
			b.WriteByte(c)
		}
	}
	return b.String()
}
