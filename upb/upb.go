// See "UPB Powerline Interface Module (PIM) Description".
// http://www.simply-automated.com/tech_specs/

package upb

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/tarm/serial"
)

type Conn struct {
	port io.ReadWriteCloser
	wr   chan *req
	net  byte
	logf func(fmt string, args ...interface{})
}

type Config struct {
	Network byte
	Logf    func(fmt string, args ...interface{})
}

func Open(name string, cfg *Config) (*Conn, error) {
	s, err := serial.OpenPort(&serial.Config{
		Name: name,
		Baud: 4800,
	})
	if err != nil {
		return nil, err
	}
	return Client(s, cfg), nil
}

func Client(s io.ReadWriteCloser, cfg *Config) *Conn {
	c := &Conn{
		port: s,
		wr:   make(chan *req),
		net:  cfg.Network,
		logf: cfg.Logf,
	}
	if c.logf == nil {
		c.logf = func(string, ...interface{}) {}
	}
	go c.serve()
	// TODO(mdempsky): This finalizer won't actually work as intended,
	// because the serve goroutine will keep c alive.
	runtime.SetFinalizer(c, (*Conn).Close)
	return c
}

const (
	// 6.3. "Host-To-PIM Commands"
	TXUPB     = 0x14 // "Transmit UPB Message"
	ReadRegs  = 0x12 // "Read PIM Registers"
	WriteRegs = 0x17 // "Write PIM Registers"
)

func (c *Conn) serve() {
	rd := make(chan string)
	go func() {
		scan := bufio.NewScanner(c.port)
		scan.Split(scanMessage)
		for scan.Scan() {
			rd <- scan.Text()
		}
		close(rd)
	}()

	var rq *req
	respond := func(err error) {
		c.logf("response: %v", err)
		select {
		case rq.resp <- err:
		default:
			c.logf("failed to send response")
		}
		rq = nil
	}

	for {
		var wr chan *req
		if rq == nil {
			wr = c.wr
		}

		select {
		case s := <-rd:
			c.logf("rx %q", s)
			switch s {
			case "PA": // PIM Accept; expect PK or PN next
			case "PB": // PIM Busy
				respond(errPIMBusy)
			case "PE": // PIM Error
				respond(errPIMError)
			case "PK": // Ack Response
				respond(nil)
			case "PN": // Nak Response
				var err error
				if rq.msg[1]&0x10 != 0 {
					err = errMissingAck
				}
				respond(err)
			}
		case r := <-wr:
			rq = r
			const cmd byte = TXUPB
			c.logf("tx %02x %q", cmd, hex.EncodeToString(rq.msg))
			_, err := fmt.Fprintf(c.port, "%c%02X%02X\r", cmd, rq.msg, Checksum(rq.msg))
			if err != nil {
				respond(err)
			}
		}
	}
}

var (
	errPIMBusy    = errors.New("PIM busy")
	errPIMError   = errors.New("PIM error")
	errMissingAck = errors.New("missing Ack Pulse")
)

type req struct {
	resp chan error
	msg  []byte
}

func (c *Conn) Close() error {
	close(c.wr)
	return nil
}

func (c *Conn) Send(msg []byte) error {
	ch := make(chan error)
	c.wr <- &req{ch, msg}
	return <-ch
}

func (c *Conn) Message(addr, cmd byte, args []byte) []byte {
	return append([]byte{
		// Packet header.
		byte(6 + len(args) + 1), // LEN
		0x10,  // "Acknowledge with an ACK Pulse"
		c.net, // Network ID
		addr,  // Destination ID
		0xFF,  // Source ID
		cmd,   // Message Data ID
	}, args...)
}

func (c *Conn) Goto(id, val byte) error {
	// 11.1.3. "The Goto Command"
	return c.Send(c.Message(id, 0x22, []byte{val}))
}

func (c *Conn) ReportState(id byte) error {
	// 11.1.9. "The Report State Command"
	return c.Send(c.Message(id, 0x30, nil))
}

// Checksum computes a UPB Packet Checksum.
func Checksum(msg []byte) byte {
	// "Sum all of the bytes of the Packet Header and UPB Message fields
	// together. Then take the 2's complement of the sum and truncate the
	// result to 8-bits."
	var x byte
	for _, c := range msg {
		x += c
	}
	return -x
}

var errTruncated = errors.New("truncated message")

func scanMessage(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, '\r'); i >= 0 {
		// We have a full message.
		return i + 1, data[:i], nil
	}
	// If we're at EOF, we're missing a message.
	if atEOF && len(data) > 0 {
		return len(data), nil, errTruncated
	}
	// Request more data.
	return 0, nil, nil
}
