package protocol

import (
	"reflect"
	"testing"
)

func TestAppend_Bytes(t *testing.T) {
	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{"simple string", Value{Kind: SimpleString, Str: []byte("OK")}, "+OK\r\n"},
		{"error", Value{Kind: Error, Str: []byte("ERR boom")}, "-ERR boom\r\n"},
		{"integer", Value{Kind: Integer, Int: 1000}, ":1000\r\n"},
		{"negative integer", Value{Kind: Integer, Int: -42}, ":-42\r\n"},
		{"bulk string", Value{Kind: BulkString, Str: []byte("hello")}, "$5\r\nhello\r\n"},
		{"empty bulk string", Value{Kind: BulkString, Str: []byte{}}, "$0\r\n\r\n"},
		{"bulk containing CRLF", Value{Kind: BulkString, Str: []byte("a\r\nb")}, "$4\r\na\r\nb\r\n"},
		{"null bulk string", Value{Kind: BulkString, Null: true}, "$-1\r\n"},
		{"empty array", Value{Kind: Array, Array: []Value{}}, "*0\r\n"},
		{"null array", Value{Kind: Array, Null: true}, "*-1\r\n"},
		{
			"command array",
			Value{Kind: Array, Array: []Value{
				{Kind: BulkString, Str: []byte("GET")},
				{Kind: BulkString, Str: []byte("k")},
			}},
			"*2\r\n$3\r\nGET\r\n$1\r\nk\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Append(nil, tt.value)); got != tt.want {
				t.Errorf("Append = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppend_AppendsToExistingBuffer(t *testing.T) {
	buf := Append([]byte("+OK\r\n"), Value{Kind: Integer, Int: 1})
	if got, want := string(buf), "+OK\r\n:1\r\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Append 결과를 Parse 하면 원래 값이 나오고, 전체 바이트를 소비해야 한다.
func TestAppend_RoundTrip(t *testing.T) {
	values := []Value{
		{Kind: SimpleString, Str: []byte("OK")},
		{Kind: Error, Str: []byte("WRONGTYPE bad")},
		{Kind: Integer, Int: 0},
		{Kind: Integer, Int: -9223372036854775808},
		{Kind: Integer, Int: 9223372036854775807},
		{Kind: BulkString, Str: []byte{}},
		{Kind: BulkString, Str: []byte("a\r\nb\x00")},
		{Kind: BulkString, Null: true},
		{Kind: Array, Array: []Value{}},
		{Kind: Array, Null: true},
		{Kind: Array, Array: []Value{
			{Kind: Array, Array: []Value{{Kind: Integer, Int: 1}, {Kind: BulkString, Null: true}}},
			{Kind: SimpleString, Str: []byte("a")},
			{Kind: BulkString, Str: []byte("x")},
		}},
	}
	for _, v := range values {
		buf := Append(nil, v)
		got, n, err := Parse(buf)
		if err != nil {
			t.Fatalf("Parse(%q): %v", buf, err)
		}
		if n != len(buf) {
			t.Errorf("Parse(%q): consumed %d, want %d", buf, n, len(buf))
		}
		if !reflect.DeepEqual(got, v) {
			t.Errorf("round trip of %q: got %+v, want %+v", buf, got, v)
		}
	}
}
