package command

import (
	"strings"
	"testing"

	"redis-from-scratch/internal/protocol"
	"redis-from-scratch/internal/store"
)

// run executes one command and returns the reply as RESP wire bytes, so tests
// check both the value and its encoding.
func run(d *Dispatcher, args ...string) string {
	argv := make([][]byte, len(args))
	for i, a := range args {
		argv[i] = []byte(a)
	}
	return string(protocol.Append(nil, d.Handle(argv)))
}

func newDispatcher() *Dispatcher { return New(store.New()) }

func TestDispatcher_Table(t *testing.T) {
	tests := []struct {
		name string
		cmds [][]string // 마지막 명령의 응답만 검증한다
		want string
	}{
		{"PING", [][]string{{"PING"}}, "+PONG\r\n"},
		{"PING with message", [][]string{{"PING", "hi"}}, "$2\r\nhi\r\n"},
		{"ECHO", [][]string{{"ECHO", "hello"}}, "$5\r\nhello\r\n"},

		{"SET", [][]string{{"SET", "k", "v"}}, "+OK\r\n"},
		{"GET existing", [][]string{{"SET", "k", "v"}, {"GET", "k"}}, "$1\r\nv\r\n"},
		{"GET missing", [][]string{{"GET", "nope"}}, "$-1\r\n"},
		{"SET overwrites", [][]string{{"SET", "k", "1"}, {"SET", "k", "2"}, {"GET", "k"}}, "$1\r\n2\r\n"},
		{"SET empty value", [][]string{{"SET", "k", ""}, {"GET", "k"}}, "$0\r\n\r\n"},
		{"binary-safe value", [][]string{{"SET", "k", "a\r\nb\x00"}, {"GET", "k"}}, "$5\r\na\r\nb\x00\r\n"},

		{"DEL one", [][]string{{"SET", "a", "1"}, {"DEL", "a"}}, ":1\r\n"},
		{"DEL missing", [][]string{{"DEL", "a"}}, ":0\r\n"},
		{"DEL many counts only existing", [][]string{{"SET", "a", "1"}, {"SET", "b", "2"}, {"DEL", "a", "x", "b"}}, ":2\r\n"},
		{"DEL same key twice counts once", [][]string{{"SET", "a", "1"}, {"DEL", "a", "a"}}, ":1\r\n"},
		{"GET after DEL", [][]string{{"SET", "a", "1"}, {"DEL", "a"}, {"GET", "a"}}, "$-1\r\n"},

		{"EXISTS present", [][]string{{"SET", "a", "1"}, {"EXISTS", "a"}}, ":1\r\n"},
		{"EXISTS missing", [][]string{{"EXISTS", "a"}}, ":0\r\n"},
		{"EXISTS counts duplicates", [][]string{{"SET", "a", "1"}, {"EXISTS", "a", "a", "x"}}, ":2\r\n"},

		{"command name is case-insensitive", [][]string{{"sEt", "k", "v"}, {"gEt", "k"}}, "$1\r\nv\r\n"},

		// INCR
		{"INCR missing key starts at 1", [][]string{{"INCR", "k"}}, ":1\r\n"},
		{"INCR increments existing", [][]string{{"SET", "k", "5"}, {"INCR", "k"}}, ":6\r\n"},
		{"INCR stores result as string", [][]string{{"INCR", "k"}, {"GET", "k"}}, "$1\r\n1\r\n"},
		{"INCR negative value", [][]string{{"SET", "k", "-3"}, {"INCR", "k"}}, ":-2\r\n"},

		// DECR
		{"DECR missing key starts at -1", [][]string{{"DECR", "k"}}, ":-1\r\n"},
		{"DECR decrements existing", [][]string{{"SET", "k", "5"}, {"DECR", "k"}}, ":4\r\n"},

		// APPEND
		{"APPEND missing key creates value", [][]string{{"APPEND", "k", "hello"}}, ":5\r\n"},
		{"APPEND extends existing value", [][]string{{"SET", "k", "foo"}, {"APPEND", "k", "bar"}}, ":6\r\n"},
		{"APPEND GET after", [][]string{{"SET", "k", "foo"}, {"APPEND", "k", "bar"}, {"GET", "k"}}, "$6\r\nfoobar\r\n"},
		{"APPEND empty suffix", [][]string{{"SET", "k", "hi"}, {"APPEND", "k", ""}}, ":2\r\n"},

		// MGET
		{"MGET all present", [][]string{{"SET", "a", "1"}, {"SET", "b", "2"}, {"MGET", "a", "b"}}, "*2\r\n$1\r\n1\r\n$1\r\n2\r\n"},
		{"MGET missing is null", [][]string{{"MGET", "nope"}}, "*1\r\n$-1\r\n"},
		{"MGET mix present and missing", [][]string{{"SET", "a", "1"}, {"MGET", "a", "x"}}, "*2\r\n$1\r\n1\r\n$-1\r\n"},

		// MSET
		{"MSET returns OK", [][]string{{"MSET", "a", "1"}}, "+OK\r\n"},
		{"MSET sets first key", [][]string{{"MSET", "a", "1", "b", "2"}, {"GET", "a"}}, "$1\r\n1\r\n"},
		{"MSET sets second key", [][]string{{"MSET", "a", "1", "b", "2"}, {"GET", "b"}}, "$1\r\n2\r\n"},
		{"MSET overwrites existing", [][]string{{"SET", "k", "old"}, {"MSET", "k", "new"}, {"GET", "k"}}, "$3\r\nnew\r\n"},

		// SETNX
		{"SETNX missing key sets and returns 1", [][]string{{"SETNX", "k", "v"}}, ":1\r\n"},
		{"SETNX existing key returns 0", [][]string{{"SET", "k", "v"}, {"SETNX", "k", "new"}}, ":0\r\n"},
		{"SETNX existing key preserves original value", [][]string{{"SET", "k", "original"}, {"SETNX", "k", "new"}, {"GET", "k"}}, "$8\r\noriginal\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDispatcher()
			var got string
			for _, c := range tt.cmds {
				got = run(d, c...)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDispatcher_WrongArity(t *testing.T) {
	tests := []struct {
		args []string
		cmd  string // 에러 메시지에 들어가는 소문자 명령 이름
	}{
		{[]string{"PING", "a", "b"}, "ping"},
		{[]string{"ECHO"}, "echo"},
		{[]string{"ECHO", "a", "b"}, "echo"},
		{[]string{"GET"}, "get"},
		{[]string{"GET", "a", "b"}, "get"},
		{[]string{"SET", "k"}, "set"},
		{[]string{"SET", "k", "v", "extra"}, "set"},
		{[]string{"DEL"}, "del"},
		{[]string{"EXISTS"}, "exists"},
		{[]string{"INCR"}, "incr"},
		{[]string{"INCR", "a", "b"}, "incr"},
		{[]string{"DECR"}, "decr"},
		{[]string{"DECR", "a", "b"}, "decr"},
		{[]string{"APPEND", "k"}, "append"},
		{[]string{"APPEND", "k", "v", "extra"}, "append"},
		{[]string{"MGET"}, "mget"},
		{[]string{"MSET"}, "mset"},
		{[]string{"MSET", "k"}, "mset"},
		{[]string{"SETNX", "k"}, "setnx"},
		{[]string{"SETNX", "k", "v", "extra"}, "setnx"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			got := run(newDispatcher(), tt.args...)
			want := "-ERR wrong number of arguments for '" + tt.cmd + "' command\r\n"
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// 인자 수 오류가 난 명령은 상태를 바꾸지 않아야 한다(검증 후 변경).
func TestDispatcher_WrongArityDoesNotMutate(t *testing.T) {
	d := newDispatcher()
	run(d, "SET", "k", "v", "extra")
	if got := run(d, "EXISTS", "k"); got != ":0\r\n" {
		t.Errorf("EXISTS after rejected SET = %q, want :0", got)
	}
}

func TestDispatcher_UnknownCommand(t *testing.T) {
	d := newDispatcher()

	got := run(d, "FOO", "a", "b")
	want := "-ERR unknown command 'FOO', with args beginning with: 'a' 'b' \r\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// 인자가 없으면 ", with args beginning with:" 부분 자체를 붙이지 않는다(redis-server 8.10.3 확인).
	got = run(d, "FOO")
	want = "-ERR unknown command 'FOO'\r\n"
	if got != want {
		t.Errorf("no args: got %q, want %q", got, want)
	}

	got = run(d, "FOO", "")
	want = "-ERR unknown command 'FOO', with args beginning with: '' \r\n"
	if got != want {
		t.Errorf("empty arg: got %q, want %q", got, want)
	}
}

// 사용자 입력이 에러 메시지에 그대로 되돌아가면 큰 인자 하나로 응답이 증폭된다.
func TestDispatcher_UnknownCommandBoundsEchoedInput(t *testing.T) {
	d := newDispatcher()
	huge := strings.Repeat("x", 1<<20)

	for _, got := range []string{
		run(d, "FOO", huge, huge, huge),
		run(d, huge, "a"),
	} {
		if len(got) > 512 {
			t.Errorf("reply is %d bytes, want it bounded", len(got))
		}
		if !strings.HasPrefix(got, "-ERR unknown command '") || !strings.HasSuffix(got, "\r\n") {
			t.Errorf("malformed reply %q", got)
		}
	}
}

func TestDispatcher_IncrDecrErrors(t *testing.T) {
	notInt := "-ERR value is not an integer or out of range\r\n"
	overflow := "-ERR increment or decrement would overflow\r\n"

	tests := []struct {
		name string
		cmds [][]string
		want string
	}{
		{"INCR on non-integer string", [][]string{{"SET", "k", "not-a-number"}, {"INCR", "k"}}, notInt},
		{"INCR on float string", [][]string{{"SET", "k", "3.14"}, {"INCR", "k"}}, notInt},
		{"DECR on non-integer string", [][]string{{"SET", "k", "abc"}, {"DECR", "k"}}, notInt},
		{"INCR overflow at MaxInt64", [][]string{{"SET", "k", "9223372036854775807"}, {"INCR", "k"}}, overflow},
		{"DECR underflow at MinInt64", [][]string{{"SET", "k", "-9223372036854775808"}, {"DECR", "k"}}, overflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDispatcher()
			var got string
			for _, c := range tt.cmds {
				got = run(d, c...)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// INCR/DECR 에러 시 기존 값을 보존해야 한다(검증 후 변경).
func TestDispatcher_IncrDecrErrorDoesNotMutate(t *testing.T) {
	d := newDispatcher()
	run(d, "SET", "k", "not-a-number")
	run(d, "INCR", "k")
	if got := run(d, "GET", "k"); got != "$12\r\nnot-a-number\r\n" {
		t.Errorf("GET after failed INCR = %q, want original value", got)
	}
}

// MSET 의 key-value 쌍이 홀수이면 아무 키도 설정하지 않아야 한다(검증 후 변경).
func TestDispatcher_MsetOddPairsDoesNotMutate(t *testing.T) {
	d := newDispatcher()
	run(d, "MSET", "a", "1", "b") // key-value 쌍 불완전 → 거부
	if got := run(d, "EXISTS", "a"); got != ":0\r\n" {
		t.Errorf("EXISTS after rejected MSET = %q, want :0", got)
	}
}

// 에러 메시지에 CR/LF 가 섞여도 응답은 정확히 한 줄이어야 한다(writer 가 치환).
func TestDispatcher_UnknownCommandWithCRLFStaysOneReply(t *testing.T) {
	got := run(newDispatcher(), "FOO\r\n+OK", "a\r\nb")
	if strings.Count(got, "\r\n") != 1 {
		t.Errorf("reply %q contains %d CRLF, want exactly 1 (the terminator)", got, strings.Count(got, "\r\n"))
	}
}
