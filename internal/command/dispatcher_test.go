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

// 에러 메시지에 CR/LF 가 섞여도 응답은 정확히 한 줄이어야 한다(writer 가 치환).
func TestDispatcher_UnknownCommandWithCRLFStaysOneReply(t *testing.T) {
	got := run(newDispatcher(), "FOO\r\n+OK", "a\r\nb")
	if strings.Count(got, "\r\n") != 1 {
		t.Errorf("reply %q contains %d CRLF, want exactly 1 (the terminator)", got, strings.Count(got, "\r\n"))
	}
}
