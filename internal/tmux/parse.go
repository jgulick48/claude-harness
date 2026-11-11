package tmux

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Reply is a %begin ... %end|%error block.
type Reply struct {
	Time  int64
	Num   int
	Flags int // bit 0 set: reply to a command sent by this client
	Lines []string
	Err   bool // block ended with %error
}

// Notification is any other %-line. Name has no leading '%'; Args is the rest
// of the line verbatim.
type Notification struct {
	Name string
	Args string
	// Pane and Data are set for "output": %output %<pane> <escaped data>.
	Pane string
	Data []byte
	// Seq numbers notifications in arrival order (set by ControlClient).
	Seq uint64
}

// Message is a Reply or a Notification (exactly one is non-nil).
type Message struct {
	Reply *Reply
	Note  *Notification
}

// Reader parses a control-mode stream.
type Reader struct{ br *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{br: bufio.NewReaderSize(r, 64<<10)} }

func (r *Reader) readLine() ([]byte, error) {
	line, err := r.br.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(line, []byte("\n")), nil
}

// Next returns the next message, or an error (io.EOF at a clean end).
func (r *Reader) Next() (Message, error) {
	for {
		line, err := r.readLine()
		if err != nil {
			return Message{}, err
		}
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("%begin ")) {
			rep, err := r.readBlock(string(line))
			if err != nil {
				return Message{}, err
			}
			return Message{Reply: rep}, nil
		}
		return Message{Note: parseNotification(line)}, nil
	}
}

func parseHeader(s string) (t int64, num, flags int, err error) {
	f := strings.Fields(s)
	if len(f) < 4 {
		return 0, 0, 0, fmt.Errorf("tmux: malformed block header %q", s)
	}
	t, err1 := strconv.ParseInt(f[1], 10, 64)
	num, err2 := strconv.Atoi(f[2])
	flags, err3 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("tmux: malformed block header %q", s)
	}
	return t, num, flags, nil
}

func (r *Reader) readBlock(header string) (*Reply, error) {
	t, num, flags, err := parseHeader(header)
	if err != nil {
		return nil, err
	}
	rep := &Reply{Time: t, Num: num, Flags: flags}
	for {
		line, err := r.readLine()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		s := string(line)
		if strings.HasPrefix(s, "%end ") || strings.HasPrefix(s, "%error ") {
			if _, n, _, perr := parseHeader(s); perr == nil && n == num {
				rep.Err = strings.HasPrefix(s, "%error ")
				return rep, nil
			}
		}
		rep.Lines = append(rep.Lines, s)
	}
}

func parseNotification(line []byte) *Notification {
	s := string(line)
	n := &Notification{}
	if !strings.HasPrefix(s, "%") {
		n.Args = s
		return n
	}
	name, args, _ := strings.Cut(s[1:], " ")
	n.Name, n.Args = name, args
	switch name {
	case "output":
		pane, data, _ := strings.Cut(args, " ")
		n.Pane, n.Data = pane, Unescape([]byte(data))
	case "extended-output": // %extended-output %pane age ... : data
		pane, rest, _ := strings.Cut(args, " ")
		if _, data, ok := strings.Cut(rest, ": "); ok {
			n.Pane, n.Data = pane, Unescape([]byte(data))
		}
	}
	return n
}

// Unescape decodes tmux's %output encoding: bytes below 0x20 and '\' are sent
// as \ooo (three octal digits). Malformed escapes are kept literally.
func Unescape(b []byte) []byte {
	if bytes.IndexByte(b, '\\') < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+3 < len(b) && isOct(b[i+1]) && isOct(b[i+2]) && isOct(b[i+3]) {
			out = append(out, (b[i+1]-'0')<<6|(b[i+2]-'0')<<3|(b[i+3]-'0'))
			i += 3
			continue
		}
		out = append(out, b[i])
	}
	return out
}

func isOct(c byte) bool { return c >= '0' && c <= '7' }
