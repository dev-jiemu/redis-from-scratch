package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

const (
	maxBulkLen  = 512 * 1024 * 1024 // Redis proto-max-bulk-len 기본값
	maxArrayLen = 1024 * 1024       // Redis multibulk 상한과 동일
	maxPrealloc = 1024              // 악의적 길이 헤더로 인한 과대 할당 방지
)

// Kind is the RESP2 data type of a Value.
type Kind byte

const (
	SimpleString Kind = '+'
	Error        Kind = '-'
	Integer      Kind = ':'
	BulkString   Kind = '$'
	Array        Kind = '*'
)

// Value is a parsed RESP2 message. Null is set for the null bulk string ($-1)
// and the null array (*-1).
type Value struct {
	Kind  Kind
	Str   []byte  // SimpleString, Error, BulkString
	Int   int64   // Integer
	Array []Value // Array
	Null  bool
}

// ErrIncomplete means buf holds only part of a message; read more bytes and retry.
var ErrIncomplete = errors.New("incomplete message")

// ProtocolError means the input can never become a valid message.
type ProtocolError struct {
	Msg string
}

func (e *ProtocolError) Error() string {
	return "protocol error: " + e.Msg
}

func protoErrf(format string, args ...any) error {
	return &ProtocolError{Msg: fmt.Sprintf(format, args...)}
}

// Parse parses the first message in buf and returns it with the number of
// bytes consumed. If buf does not yet hold a whole message it returns
// ErrIncomplete and consumed 0, so the caller keeps buf untouched.
func Parse(buf []byte) (Value, int, error) {
	if len(buf) == 0 {
		return Value{}, 0, ErrIncomplete
	}
	kind := Kind(buf[0])
	switch kind {
	case SimpleString, Error, Integer, BulkString, Array:
	default:
		return Value{}, 0, protoErrf("unknown type byte %q", buf[0])
	}

	line, next, ok := readLine(buf[1:])
	if !ok {
		return Value{}, 0, ErrIncomplete
	}
	next++ // type byte

	switch kind {
	case SimpleString, Error:
		return Value{Kind: kind, Str: append([]byte(nil), line...)}, next, nil
	case Integer:
		n, err := strconv.ParseInt(string(line), 10, 64)
		if err != nil {
			return Value{}, 0, protoErrf("invalid integer %q", line)
		}
		return Value{Kind: Integer, Int: n}, next, nil
	case BulkString:
		return parseBulk(buf, line, next)
	default:
		return parseArray(buf, line, next)
	}
}

// readLine returns the bytes before the first CRLF and the offset just past it.
func readLine(buf []byte) (line []byte, next int, ok bool) {
	i := bytes.Index(buf, []byte("\r\n"))
	if i < 0 {
		return nil, 0, false
	}
	return buf[:i], i + 2, true
}

func parseBulk(buf, header []byte, next int) (Value, int, error) {
	n, err := strconv.ParseInt(string(header), 10, 64)
	if err != nil || n < -1 || n > maxBulkLen {
		return Value{}, 0, protoErrf("invalid bulk length %q", header)
	}
	if n == -1 {
		return Value{Kind: BulkString, Null: true}, next, nil
	}
	end := next + int(n)
	if len(buf) < end+2 {
		return Value{}, 0, ErrIncomplete
	}
	if buf[end] != '\r' || buf[end+1] != '\n' {
		return Value{}, 0, protoErrf("bulk string not terminated by CRLF")
	}
	return Value{Kind: BulkString, Str: append([]byte{}, buf[next:end]...)}, end + 2, nil
}

func parseArray(buf, header []byte, next int) (Value, int, error) {
	n, err := strconv.ParseInt(string(header), 10, 64)
	if err != nil || n < -1 || n > maxArrayLen {
		return Value{}, 0, protoErrf("invalid array length %q", header)
	}
	if n == -1 {
		return Value{Kind: Array, Null: true}, next, nil
	}
	elems := make([]Value, 0, min(int(n), maxPrealloc))
	for i := int64(0); i < n; i++ {
		elem, used, err := Parse(buf[next:])
		if err != nil {
			return Value{}, 0, err
		}
		elems = append(elems, elem)
		next += used
	}
	return Value{Kind: Array, Array: elems}, next, nil
}
