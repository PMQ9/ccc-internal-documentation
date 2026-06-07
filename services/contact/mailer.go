package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Message is a transport-agnostic email. Mailers turn it into wire format.
type Message struct {
	FromName string
	FromAddr string
	To       string
	ReplyTo  string
	Subject  string
	Body     string            // plain text
	Headers  map[string]string // extra headers, e.g. X-CCC-Contact-Type
}

// Mailer sends a Message. Two implementations: smtpMailer (Brevo/Gmail/SES/
// Proton-Bridge — anything speaking SMTP) and graphMailer (Microsoft 365
// send-as). The choice is config, so swapping relays never touches calling code.
type Mailer interface {
	Send(ctx context.Context, m *Message) error
}

func newMailer(c *Config) (Mailer, error) {
	switch c.Transport {
	case "smtp":
		return &smtpMailer{
			host:       c.SMTPHost,
			port:       c.SMTPPort,
			username:   c.SMTPUsername,
			password:   c.SMTPPassword,
			encryption: c.SMTPEncryption,
		}, nil
	case "graph":
		sender := c.GraphSenderUPN
		if sender == "" {
			sender = c.FromAddress
		}
		return &graphMailer{
			tenantID:     c.GraphTenantID,
			clientID:     c.GraphClientID,
			clientSecret: c.GraphClientSecret,
			sender:       sender,
			httpc:        &http.Client{Timeout: 15 * time.Second},
		}, nil
	case "agentmail":
		return &agentMailer{
			apiKey:  c.AgentMailAPIKey,
			inbox:   c.AgentMailInbox,
			apiBase: strings.TrimRight(c.AgentMailAPIBase, "/"),
			httpc:   &http.Client{Timeout: 15 * time.Second},
		}, nil
	}
	return nil, fmt.Errorf("unknown transport %q", c.Transport)
}

// stripCRLF removes CR/LF so a value can't smuggle extra headers. Subjects are
// additionally Q-encoded; the email field is already validated CRLF-free.
func stripCRLF(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// rfc822 serializes the message to an RFC 5322 byte stream (CRLF line endings,
// UTF-8 body, headers sorted for deterministic output that tests can assert on).
func (m *Message) rfc822(now time.Time) []byte {
	var b bytes.Buffer
	enc := func(s string) string { return mime.QEncoding.Encode("utf-8", s) }

	from := stripCRLF(m.FromAddr)
	if m.FromName != "" {
		from = fmt.Sprintf("%s <%s>", enc(m.FromName), stripCRLF(m.FromAddr))
	}
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", stripCRLF(m.To))
	if m.ReplyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", stripCRLF(m.ReplyTo))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", enc(m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%d.%s>\r\n", now.UnixNano(), stripCRLF(m.FromAddr))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	for _, k := range sortedKeys(m.Headers) {
		fmt.Fprintf(&b, "%s: %s\r\n", stripCRLF(k), stripCRLF(m.Headers[k]))
	}
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n"))
	return b.Bytes()
}

// ---- SMTP -----------------------------------------------------------------

type smtpMailer struct {
	host       string
	port       int
	username   string
	password   string
	encryption string // "starttls" | "tls" | "none"
}

func (s *smtpMailer) Send(ctx context.Context, m *Message) error {
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	raw := m.rfc822(time.Now())
	tlsConf := &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}

	d := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if s.encryption == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, tlsConf)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer conn.Close()

	cl, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer cl.Close()

	if s.encryption == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp server %s does not offer STARTTLS", s.host)
		}
		if err := cl.StartTLS(tlsConf); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.username != "" {
		if err := cl.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := cl.Mail(m.FromAddr); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := cl.Rcpt(m.To); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	wc, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := wc.Write(raw); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return cl.Quit()
}

// ---- Microsoft Graph (optional; kept for a future IT-provisioned app reg) ---

type graphMailer struct {
	tenantID     string
	clientID     string
	clientSecret string
	sender       string // mailbox to send as (POST /users/{sender}/sendMail)
	httpc        *http.Client
}

func (g *graphMailer) token(ctx context.Context) (string, error) {
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
		"grant_type":    {"client_credentials"},
	}
	endpoint := "https://login.microsoftonline.com/" + url.PathEscape(g.tenantID) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("graph token: %s: %s", resp.Status, string(body))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("graph token decode: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("graph token: empty access_token")
	}
	return tr.AccessToken, nil
}

func (g *graphMailer) Send(ctx context.Context, m *Message) error {
	tok, err := g.token(ctx)
	if err != nil {
		return err
	}

	type addr struct {
		EmailAddress struct {
			Address string `json:"address"`
			Name    string `json:"name,omitempty"`
		} `json:"emailAddress"`
	}
	mkAddr := func(a, name string) addr {
		var x addr
		x.EmailAddress.Address = a
		x.EmailAddress.Name = name
		return x
	}
	type header struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	var hdrs []header
	for _, k := range sortedKeys(m.Headers) {
		hdrs = append(hdrs, header{Name: k, Value: m.Headers[k]}) // Graph requires X- prefixed names
	}

	payload := map[string]any{
		"message": map[string]any{
			"subject": m.Subject,
			"body": map[string]any{
				"contentType": "Text",
				"content":     m.Body,
			},
			"from":                   mkAddr(m.FromAddr, m.FromName),
			"toRecipients":           []addr{mkAddr(m.To, "")},
			"replyTo":                []addr{mkAddr(m.ReplyTo, "")},
			"internetMessageHeaders": hdrs,
		},
		"saveToSentItems": false,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := "https://graph.microsoft.com/v1.0/users/" + url.PathEscape(g.sender) + "/sendMail"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("graph sendMail: %s: %s", resp.Status, string(body))
	}
	return nil
}

// ---- AgentMail (https://agentmail.to) — REST email API for agents ----------
// No SMTP: a single POST to the inbox's send endpoint. From is fixed to the inbox
// (set the inbox display name in the AgentMail console); we set to/reply_to/
// subject/text/headers. agentmail.to is sender-authenticated (SPF/DKIM/DMARC), so
// this avoids the DMARC rejection that blocks free-webmail senders via relays.

type agentMailer struct {
	apiKey  string
	inbox   string // e.g. ccc-3278@agentmail.to
	apiBase string
	httpc   *http.Client
}

func (a *agentMailer) Send(ctx context.Context, m *Message) error {
	payload := map[string]any{
		"to":      m.To,
		"subject": m.Subject,
		"text":    m.Body,
	}
	if m.ReplyTo != "" {
		payload["reply_to"] = m.ReplyTo
	}
	if len(m.Headers) > 0 {
		payload["headers"] = m.Headers
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// The inbox address is the path id; encode "@" as %40 (per the AgentMail docs).
	inbox := strings.ReplaceAll(url.PathEscape(a.inbox), "@", "%40")
	endpoint := a.apiBase + "/v0/inboxes/" + inbox + "/messages/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("agentmail send: %s: %s", resp.Status, string(body))
	}
	return nil
}
