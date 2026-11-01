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

// CR/LF 가 섞인 문자열이 그대로 나가면 한 응답이 여러 개로 쪼개져 응답 스트림이 깨진다
// (예: 사용자 입력이 에러 메시지에 들어가는 경우). 줄 기반 타입은 공백으로 치환한다.
func TestAppend_LineTypesNeverEmbedCRLF(t *testing.T) {
	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{"error with CRLF", Value{Kind: Error, Str: []byte("ERR a\r\n+OK")}, "-ERR a  +OK\r\n"},
		{"error with lone LF", Value{Kind: Error, Str: []byte("ERR a\nb")}, "-ERR a b\r\n"},
		{"error with lone CR", Value{Kind: Error, Str: []byte("ERR a\rb")}, "-ERR a b\r\n"},
		{"simple string with CRLF", Value{Kind: SimpleString, Str: []byte("a\r\nb")}, "+a  b\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Append(nil, tt.value)
			if string(got) != tt.want {
				t.Errorf("Append = %q, want %q", got, tt.want)
			}
			// 결과는 정확히 하나의 메시지여야 한다.
			if _, n, err := Parse(got); err != nil || n != len(got) {
				t.Errorf("Parse(%q): consumed %d of %d, err %v", got, n, len(got), err)
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
