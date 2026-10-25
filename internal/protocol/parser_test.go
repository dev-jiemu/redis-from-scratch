package protocol

import (
	"errors"
	"reflect"
	"testing"
)

func TestParse_Valid(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     Value
		consumed int
	}{
		{"simple string", "+OK\r\n", Value{Kind: SimpleString, Str: []byte("OK")}, 5},
		{"error", "-ERR boom\r\n", Value{Kind: Error, Str: []byte("ERR boom")}, 11},
		{"integer", ":1000\r\n", Value{Kind: Integer, Int: 1000}, 7},
		{"negative integer", ":-42\r\n", Value{Kind: Integer, Int: -42}, 6},
		{"bulk string", "$5\r\nhello\r\n", Value{Kind: BulkString, Str: []byte("hello")}, 11},
		{"empty bulk string", "$0\r\n\r\n", Value{Kind: BulkString, Str: []byte{}}, 6},
		{"bulk containing CRLF", "$4\r\na\r\nb\r\n", Value{Kind: BulkString, Str: []byte("a\r\nb")}, 10},
		{"null bulk string", "$-1\r\n", Value{Kind: BulkString, Null: true}, 5},
		{"empty array", "*0\r\n", Value{Kind: Array, Array: []Value{}}, 4},
		{"null array", "*-1\r\n", Value{Kind: Array, Null: true}, 5},
		{
			"command array",
			"*2\r\n$3\r\nGET\r\n$1\r\nk\r\n",
			Value{Kind: Array, Array: []Value{
				{Kind: BulkString, Str: []byte("GET")},
				{Kind: BulkString, Str: []byte("k")},
			}},
			20,
		},
		{
			"nested array",
			"*2\r\n*1\r\n:1\r\n+a\r\n",
			Value{Kind: Array, Array: []Value{
				{Kind: Array, Array: []Value{{Kind: Integer, Int: 1}}},
				{Kind: SimpleString, Str: []byte("a")},
			}},
			16,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, n, err := Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tt.consumed {
				t.Errorf("consumed = %d, want %d", n, tt.consumed)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("value = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TCP는 스트림이므로 메시지가 어디서든 잘려 도착할 수 있다.
// 완전한 메시지의 모든 진짜 접두(prefix)는 ErrIncomplete + consumed 0 이어야 한다.
func TestParse_IncompleteAtEveryPrefix(t *testing.T) {
	inputs := []string{
		"+OK\r\n",
		":1000\r\n",
		"$5\r\nhello\r\n",
		"$-1\r\n",
		"*2\r\n$3\r\nGET\r\n$1\r\nk\r\n",
		"*2\r\n*1\r\n:1\r\n+a\r\n",
	}
	for _, in := range inputs {
		for i := 0; i < len(in); i++ {
			_, n, err := Parse([]byte(in[:i]))
			if !errors.Is(err, ErrIncomplete) {
				t.Errorf("Parse(%q): err = %v, want ErrIncomplete", in[:i], err)
			}
			if n != 0 {
				t.Errorf("Parse(%q): consumed = %d, want 0", in[:i], n)
			}
		}
	}
}

func TestParse_OnlyFirstMessageConsumed(t *testing.T) {
	buf := []byte("+OK\r\n:1\r\n")
	got, n, err := Parse(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 || string(got.Str) != "OK" {
		t.Fatalf("got %+v consumed %d", got, n)
	}
	got, n, err = Parse(buf[n:])
	if err != nil || n != 4 || got.Int != 1 {
		t.Fatalf("second: got %+v consumed %d err %v", got, n, err)
	}
}

func TestParse_ProtocolError(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"unknown type byte", "?foo\r\n"},
		{"integer not a number", ":abc\r\n"},
		{"integer overflow", ":99999999999999999999\r\n"},
		{"empty integer", ":\r\n"},
		{"bulk length not a number", "$x\r\n"},
		{"bulk length below -1", "$-2\r\n"},
		{"bulk too large", "$536870913\r\n"},
		{"bulk missing trailing CRLF", "$3\r\nabcXY"},
		{"array length not a number", "*x\r\n"},
		{"array length below -1", "*-2\r\n"},
		{"array too large", "*1048577\r\n"},
		{"error inside array element", "*1\r\n?x\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tt.input))
			var pe *ProtocolError
			if !errors.As(err, &pe) {
				t.Fatalf("err = %v, want *ProtocolError", err)
			}
		})
	}
}
