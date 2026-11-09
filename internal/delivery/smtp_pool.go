package delivery

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"sync"
	"time"
)

type smtpConnection struct {
	client *smtp.Client
	conn   net.Conn
}

type SMTPPool struct {
	addr        string
	host        string
	username    string
	password    string
	tlsInsecure bool
	dialTimeout time.Duration
	idle        chan *smtpConnection
	slots       chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
}

func NewSMTPPool(addr, host, username, password string, tlsInsecure bool, size int) *SMTPPool {
	if size < 1 {
		size = 1
	}
	return &SMTPPool{
		addr:        addr,
		host:        host,
		username:    username,
		password:    password,
		tlsInsecure: tlsInsecure,
		dialTimeout: 15 * time.Second,
		idle:        make(chan *smtpConnection, size),
		slots:       make(chan struct{}, size),
		closed:      make(chan struct{}),
	}
}

func (p *SMTPPool) Send(ctx context.Context, message Message) (*Result, error) {
	raw, err := marshalMessage(message)
	if err != nil {
		return nil, fmt.Errorf("prepare smtp message: %w", err)
	}
	connection, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer p.releaseSlot()
	deadline := time.Now().Add(p.dialTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := connection.conn.SetDeadline(deadline); err != nil {
		p.discard(connection)
		return nil, fmt.Errorf("set smtp deadline: %w", err)
	}

	envelopeSender := message.EnvelopeFrom
	if envelopeSender == "" {
		envelopeSender = message.From
	}
	from, err := parseEnvelopeAddress(envelopeSender)
	if err != nil {
		p.discard(connection)
		return nil, classifySMTP(fmt.Errorf("smtp mail from: invalid sender address: %w", err))
	}
	if err := connection.client.Mail(from); err != nil {
		p.discard(connection)
		return nil, classifySMTP(fmt.Errorf("smtp mail from: %w", err))
	}
	to, err := parseEnvelopeAddress(message.To)
	if err != nil {
		p.discard(connection)
		return nil, classifySMTP(fmt.Errorf("smtp rcpt to: invalid recipient address: %w", err))
	}
	if err := connection.client.Rcpt(to); err != nil {
		p.discard(connection)
		return nil, classifySMTP(fmt.Errorf("smtp rcpt to: %w", err))
	}
	writer, err := connection.client.Data()
	if err != nil {
		p.discard(connection)
		return nil, classifySMTP(fmt.Errorf("smtp data: %w", err))
	}
	if _, err := io.Copy(writer, bytes.NewReader(raw)); err != nil {
		_ = writer.Close()
		p.discard(connection)
		return nil, fmt.Errorf("write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		p.discard(connection)
		return nil, classifySMTP(fmt.Errorf("finish message: %w", err))
	}
	if err := connection.client.Reset(); err != nil {
		p.discard(connection)
		return nil, classifySMTP(fmt.Errorf("reset smtp transaction: %w", err))
	}
	p.release(connection)
	return &Result{MessageID: formatMessageID(message.ID), Accepted: true}, nil
}

func (p *SMTPPool) acquire(ctx context.Context) (*smtpConnection, error) {
	select {
	case <-p.closed:
		return nil, errors.New("smtp pool is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	case p.slots <- struct{}{}:
	}

	select {
	case <-p.closed:
		p.releaseSlot()
		return nil, errors.New("smtp pool is closed")
	default:
	}
	select {
	case connection := <-p.idle:
		return connection, nil
	default:
	}

	connection, err := p.dial(ctx)
	if err != nil {
		p.releaseSlot()
		return nil, err
	}
	return connection, nil
}

func (p *SMTPPool) dial(ctx context.Context) (*smtpConnection, error) {
	dialer := &net.Dialer{Timeout: p.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return nil, fmt.Errorf("dial smtp: %w", err)
	}
	deadline := time.Now().Add(p.dialTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set smtp handshake deadline: %w", err)
	}
	client, err := smtp.NewClient(conn, p.host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create smtp client: %w", err)
	}
	hasSTARTTLS, _ := client.Extension("STARTTLS")
	if err := ensureSTARTTLS(hasSTARTTLS, p.tlsInsecure); err != nil {
		_ = client.Close()
		return nil, err
	}
	if hasSTARTTLS {
		if err := client.StartTLS(&tls.Config{
			ServerName:         p.host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: p.tlsInsecure,
		}); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	if p.username != "" {
		if err := client.Auth(smtp.PlainAuth("", p.username, p.password, p.host)); err != nil {
			_ = conn.Close()
			if isPermanentSMTPError(err) {
				return nil, &PermanentError{Err: &ProviderError{
					Temporary: true,
					Message:   fmt.Sprintf("smtp auth: %v", err),
				}}
			}
			return nil, fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clear smtp handshake deadline: %w", err)
	}
	return &smtpConnection{client: client, conn: conn}, nil
}

func (p *SMTPPool) release(connection *smtpConnection) {
	if err := connection.conn.SetDeadline(time.Time{}); err != nil {
		p.discard(connection)
		return
	}
	select {
	case <-p.closed:
		p.discard(connection)
	case p.idle <- connection:
	}
}

func (p *SMTPPool) discard(connection *smtpConnection) {
	_ = connection.conn.SetDeadline(time.Now().Add(time.Second))
	_ = connection.client.Quit()
	_ = connection.conn.Close()
}

func (p *SMTPPool) releaseSlot() {
	select {
	case <-p.slots:
	default:
	}
}

func (p *SMTPPool) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)
		for {
			select {
			case connection := <-p.idle:
				p.discard(connection)
			default:
				return
			}
		}
	})
	return nil
}
