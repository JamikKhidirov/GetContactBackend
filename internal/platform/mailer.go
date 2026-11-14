package platform

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// MailMessage is a rendered transactional email with both HTML and text bodies.
type MailMessage struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Mailer delivers transactional email. Implementations must be safe for
// concurrent use.
type Mailer interface {
	Send(ctx context.Context, msg MailMessage) error
	Provider() string
}

// MailerOptions configures the built-in log and SMTP transports.
type MailerOptions struct {
	Provider    string
	FromName    string
	FromAddress string
	ReplyTo     string
	Host        string
	Port        int
	Username    string
	Password    string
	TLS         bool
	Timeout     time.Duration
}

// NewMailer builds the provider selected by EMAIL_PROVIDER. The log provider is
// intentionally dependency-free and is the safe default for local development.
func NewMailer(opts MailerOptions, log *slog.Logger) (Mailer, error) {
	switch strings.ToLower(strings.TrimSpace(opts.Provider)) {
	case "", "log":
		return &LogMailer{log: log}, nil
	case "smtp":
		if opts.Host == "" {
			return nil, fmt.Errorf("EMAIL_PROVIDER=smtp requires SMTP_HOST")
		}
		if opts.FromAddress == "" {
			return nil, fmt.Errorf("EMAIL_PROVIDER=smtp requires EMAIL_FROM_ADDRESS")
		}
		if opts.Timeout <= 0 {
			opts.Timeout = 10 * time.Second
		}
		if opts.Port == 0 {
			opts.Port = 587
		}
		return &SMTPMailer{opts: opts, log: log}, nil
	default:
		return nil, fmt.Errorf("unsupported EMAIL_PROVIDER %q (use log or smtp)", opts.Provider)
	}
}

// LogMailer writes the message to the structured log instead of sending it.
type LogMailer struct{ log *slog.Logger }

// Provider identifies the transport.
func (m *LogMailer) Provider() string { return "log" }

// Send logs the plain-text rendering so a developer can copy the code locally.
func (m *LogMailer) Send(ctx context.Context, msg MailMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.log == nil {
		return nil
	}
	m.log.Info("email delivered by log provider",
		"to", msg.To, "subject", msg.Subject, "body", msg.Text)
	return nil
}

// SMTPMailer delivers messages over SMTP with STARTTLS (587) or implicit TLS
// (465). It deliberately supports free providers such as Gmail, Mailtrap and
// Resend: any standards-compliant SMTP endpoint works.
type SMTPMailer struct {
	opts MailerOptions
	log  *slog.Logger
}

// Provider identifies the transport.
func (m *SMTPMailer) Provider() string { return "smtp" }

// Send builds a multipart/alternative MIME message and submits it.
func (m *SMTPMailer) Send(ctx context.Context, msg MailMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	to, err := mail.ParseAddress(strings.TrimSpace(msg.To))
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	from := mail.Address{Name: sanitizeHeader(m.opts.FromName), Address: m.opts.FromAddress}
	host := m.opts.Host
	addr := net.JoinHostPort(host, strconv.Itoa(m.opts.Port))

	dialer := &net.Dialer{Timeout: m.opts.Timeout}
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	implicitTLS := m.opts.Port == 465

	var conn net.Conn
	if implicitTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(m.opts.Timeout * 3))
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if !implicitTLS && m.opts.TLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		} else if m.opts.Port != 25 {
			return fmt.Errorf("smtp server %s does not offer STARTTLS", addr)
		}
	}
	if m.opts.Username != "" {
		auth := smtp.PlainAuth("", m.opts.Username, m.opts.Password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	body, err := m.buildMIME(from, *to, msg)
	if err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	// The server has accepted the message. A failed QUIT must not turn a
	// delivered email into an error, otherwise the caller retries and sends a
	// duplicate. The deferred Close tears the connection down anyway.
	_ = client.Quit()
	return nil
}

func (m *SMTPMailer) buildMIME(from mail.Address, to mail.Address, msg MailMessage) ([]byte, error) {
	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	domain := "getcontact.local"
	if at := strings.LastIndexByte(m.opts.FromAddress, '@'); at >= 0 && at+1 < len(m.opts.FromAddress) {
		domain = m.opts.FromAddress[at+1:]
	}
	messageID, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	headers := []string{
		"From: " + from.String(),
		"To: " + to.String(),
		"Subject: " + mime.QEncoding.Encode("UTF-8", sanitizeHeader(msg.Subject)),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID: <" + messageID + "@" + domain + ">",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=\"" + boundary + "\"",
	}
	if m.opts.ReplyTo != "" {
		if reply, err := mail.ParseAddress(m.opts.ReplyTo); err == nil {
			headers = append(headers, "Reply-To: "+reply.String())
		}
	}
	var b strings.Builder
	for _, h := range headers {
		b.WriteString(h)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64(msg.Text))
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64(msg.HTML))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

func wrapBase64(s string) string {
	raw := base64.StdEncoding.EncodeToString([]byte(s))
	var b strings.Builder
	for len(raw) > 76 {
		b.WriteString(raw[:76])
		b.WriteString("\r\n")
		raw = raw[76:]
	}
	b.WriteString(raw)
	return b.String()
}

func randomBoundary() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate mime boundary: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
