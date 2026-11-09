package command

import (
	"testing"

	"redis-from-scratch/internal/store"
)

func TestList_Table(t *testing.T) {
	const (
		nullBulk  = "$-1\r\n"
		nullArray = "*-1\r\n"
		empty     = "*0\r\n"
		notInt    = "-ERR value is not an integer or out of range\r\n"
		negCount  = "-ERR value is out of range, must be positive\r\n"
	)
	tests := []struct {
		name string
		cmds [][]string // 마지막 명령의 응답만 검증한다
		want string
	}{
		// LPUSH / RPUSH / LLEN
		{"RPUSH creates and returns length", [][]string{{"RPUSH", "l", "a", "b"}}, ":2\r\n"},
		{"RPUSH appends to existing", [][]string{{"RPUSH", "l", "a"}, {"RPUSH", "l", "b"}}, ":2\r\n"},
		{"LPUSH multiple args pushes one by one", [][]string{{"LPUSH", "l", "a", "b", "c"}, {"LRANGE", "l", "0", "-1"}}, "*3\r\n$1\r\nc\r\n$1\r\nb\r\n$1\r\na\r\n"},
		{"LPUSH prepends to existing", [][]string{{"RPUSH", "l", "a"}, {"LPUSH", "l", "b"}, {"LRANGE", "l", "0", "-1"}}, "*2\r\n$1\r\nb\r\n$1\r\na\r\n"},
		{"LLEN missing", [][]string{{"LLEN", "l"}}, ":0\r\n"},
		{"LLEN", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"LLEN", "l"}}, ":3\r\n"},
		{"push empty element", [][]string{{"RPUSH", "l", ""}, {"LRANGE", "l", "0", "-1"}}, "*1\r\n$0\r\n\r\n"},

		// LRANGE
		{"LRANGE 0 -1 is whole list", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"LRANGE", "l", "0", "-1"}}, "*3\r\n$1\r\na\r\n$1\r\nb\r\n$1\r\nc\r\n"},
		{"LRANGE negative start", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"LRANGE", "l", "-2", "-1"}}, "*2\r\n$1\r\nb\r\n$1\r\nc\r\n"},
		{"LRANGE negative stop", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"LRANGE", "l", "0", "-2"}}, "*2\r\n$1\r\na\r\n$1\r\nb\r\n"},
		{"LRANGE inner", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"LRANGE", "l", "1", "1"}}, "*1\r\n$1\r\nb\r\n"},
		{"LRANGE stop past end is clamped", [][]string{{"RPUSH", "l", "a", "b"}, {"LRANGE", "l", "0", "100"}}, "*2\r\n$1\r\na\r\n$1\r\nb\r\n"},
		{"LRANGE start before head is clamped", [][]string{{"RPUSH", "l", "a", "b"}, {"LRANGE", "l", "-100", "0"}}, "*1\r\n$1\r\na\r\n"},
		{"LRANGE start past end", [][]string{{"RPUSH", "l", "a", "b"}, {"LRANGE", "l", "5", "10"}}, empty},
		{"LRANGE start > stop", [][]string{{"RPUSH", "l", "a", "b"}, {"LRANGE", "l", "1", "0"}}, empty},
		{"LRANGE stop before head", [][]string{{"RPUSH", "l", "a", "b"}, {"LRANGE", "l", "0", "-100"}}, empty},
		{"LRANGE missing key", [][]string{{"LRANGE", "l", "0", "-1"}}, empty},
		{"LRANGE not an integer", [][]string{{"RPUSH", "l", "a"}, {"LRANGE", "l", "x", "1"}}, notInt},
		{"LRANGE not an integer checked before key lookup", [][]string{{"LRANGE", "nope", "0", "x"}}, notInt},
		{"LRANGE extreme bounds", [][]string{{"RPUSH", "l", "a"}, {"LRANGE", "l", "-9223372036854775808", "9223372036854775807"}}, "*1\r\n$1\r\na\r\n"},

		// LPOP / RPOP
		{"LPOP", [][]string{{"RPUSH", "l", "a", "b"}, {"LPOP", "l"}}, "$1\r\na\r\n"},
		{"RPOP", [][]string{{"RPUSH", "l", "a", "b"}, {"RPOP", "l"}}, "$1\r\nb\r\n"},
		{"LPOP leaves rest", [][]string{{"RPUSH", "l", "a", "b"}, {"LPOP", "l"}, {"LRANGE", "l", "0", "-1"}}, "*1\r\n$1\r\nb\r\n"},
		{"RPOP leaves rest", [][]string{{"RPUSH", "l", "a", "b"}, {"RPOP", "l"}, {"LRANGE", "l", "0", "-1"}}, "*1\r\n$1\r\na\r\n"},
		{"LPOP missing", [][]string{{"LPOP", "l"}}, nullBulk},
		{"RPOP missing", [][]string{{"RPOP", "l"}}, nullBulk},
		{"LPOP count", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"LPOP", "l", "2"}}, "*2\r\n$1\r\na\r\n$1\r\nb\r\n"},
		{"RPOP count pops from tail first", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"RPOP", "l", "2"}}, "*2\r\n$1\r\nc\r\n$1\r\nb\r\n"},
		{"LPOP count leaves rest", [][]string{{"RPUSH", "l", "a", "b", "c"}, {"LPOP", "l", "2"}, {"LRANGE", "l", "0", "-1"}}, "*1\r\n$1\r\nc\r\n"},
		{"LPOP count greater than length pops all", [][]string{{"RPUSH", "l", "a", "b"}, {"LPOP", "l", "10"}}, "*2\r\n$1\r\na\r\n$1\r\nb\r\n"},
		{"LPOP count zero on existing", [][]string{{"RPUSH", "l", "a"}, {"LPOP", "l", "0"}}, empty},
		{"LPOP count zero leaves list", [][]string{{"RPUSH", "l", "a"}, {"LPOP", "l", "0"}, {"LLEN", "l"}}, ":1\r\n"},
		{"LPOP count on missing is null array", [][]string{{"LPOP", "l", "2"}}, nullArray},
		{"LPOP count zero on missing is null array", [][]string{{"LPOP", "l", "0"}}, nullArray},
		{"LPOP negative count", [][]string{{"RPUSH", "l", "a"}, {"LPOP", "l", "-1"}}, negCount},
		{"LPOP non-integer count", [][]string{{"RPUSH", "l", "a"}, {"LPOP", "l", "x"}}, negCount},
		{"RPOP negative count", [][]string{{"RPUSH", "l", "a"}, {"RPOP", "l", "-1"}}, negCount},

		// 빈 리스트는 키가 삭제된다
		{"LPOP last element deletes key", [][]string{{"RPUSH", "l", "a"}, {"LPOP", "l"}, {"EXISTS", "l"}}, ":0\r\n"},
		{"RPOP last element deletes key", [][]string{{"RPUSH", "l", "a"}, {"RPOP", "l"}, {"EXISTS", "l"}}, ":0\r\n"},
		{"LPOP count draining deletes key", [][]string{{"RPUSH", "l", "a", "b"}, {"LPOP", "l", "2"}, {"EXISTS", "l"}}, ":0\r\n"},
		{"key is reusable as string after list drained", [][]string{{"RPUSH", "l", "a"}, {"LPOP", "l"}, {"SET", "l", "v"}, {"GET", "l"}}, "$1\r\nv\r\n"},
		{"push after drain starts fresh", [][]string{{"RPUSH", "l", "a"}, {"LPOP", "l"}, {"RPUSH", "l", "b"}, {"LRANGE", "l", "0", "-1"}}, "*1\r\n$1\r\nb\r\n"},
		{"interleaved head and tail pops", [][]string{{"RPUSH", "l", "a", "b", "c", "d"}, {"LPOP", "l"}, {"RPOP", "l"}, {"LPUSH", "l", "x"}, {"RPUSH", "l", "y"}, {"LRANGE", "l", "0", "-1"}}, "*4\r\n$1\r\nx\r\n$1\r\nb\r\n$1\r\nc\r\n$1\r\ny\r\n"},
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

func TestList_WrongType(t *testing.T) {
	for _, cmd := range [][]string{
		{"LPUSH", "s", "x"}, {"RPUSH", "s", "x"}, {"LLEN", "s"},
		{"LRANGE", "s", "0", "-1"}, {"LPOP", "s"}, {"RPOP", "s"}, {"LPOP", "s", "1"},
	} {
		t.Run(cmd[0], func(t *testing.T) {
			d := newDispatcher()
			run(d, "SET", "s", "v")
			if got := run(d, cmd...); got != wrongTypeReply {
				t.Errorf("got %q, want WRONGTYPE", got)
			}
			if got := run(d, "GET", "s"); got != "$1\r\nv\r\n" {
				t.Errorf("string was modified: %q", got)
			}
		})
	}
}

func TestList_WrongArity(t *testing.T) {
	for _, cmd := range [][]string{
		{"LPUSH"}, {"LPUSH", "l"}, {"RPUSH", "l"},
		{"LLEN"}, {"LLEN", "l", "x"},
		{"LRANGE", "l", "0"}, {"LRANGE", "l", "0", "1", "2"},
		{"LPOP"}, {"RPOP"}, {"LPOP", "l", "1", "2"}, {"RPOP", "l", "1", "2"},
	} {
		got := run(newDispatcher(), cmd...)
		want := "-ERR wrong number of arguments for '" + lower(cmd[0]) + "' command\r\n"
		if got != want {
			t.Errorf("%v: got %q, want %q", cmd, got, want)
		}
	}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// 응답에 실린 원소가 이후 명령으로 바뀌지 않아야 한다 (executor 밖에서 복사되므로).
func TestList_ReplyUnaffectedByLaterCommands(t *testing.T) {
	db := store.New()
	d := New(db)
	run(d, "RPUSH", "l", "a", "b", "c")
	reply := d.Handle([][]byte{[]byte("LRANGE"), []byte("l"), []byte("0"), []byte("-1")})
	run(d, "LPOP", "l")
	run(d, "LPUSH", "l", "x", "y")
	run(d, "RPUSH", "l", "z")
	if len(reply.Array) != 3 || string(reply.Array[0].Str) != "a" || string(reply.Array[2].Str) != "c" {
		t.Errorf("reply changed after later commands: %+v", reply.Array)
	}
}
