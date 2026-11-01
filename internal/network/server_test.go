package network

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"redis-from-scratch/internal/protocol"
)

type handlerFunc func(args [][]byte) protocol.Value

func (f handlerFunc) Handle(args [][]byte) protocol.Value { return f(args) }

// echoHandler replies with the space-joined args as a bulk string.
var echoHandler = handlerFunc(func(args [][]byte) protocol.Value {
	return bulk(strings.Join(argsToStrings(args), " "))
})

func argsToStrings(args [][]byte) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = string(a)
	}
	return out
}

func bulk(s string) protocol.Value {
	return protocol.Value{Kind: protocol.BulkString, Str: []byte(s)}
}

func command(args ...string) []byte {
	elems := make([]protocol.Value, len(args))
	for i, a := range args {
		elems[i] = bulk(a)
	}
	return protocol.Append(nil, protocol.Value{Kind: protocol.Array, Array: elems})
}

func reply(s string) []byte {
	return protocol.Append(nil, bulk(s))
}

// startServer runs a server on a random port and stops it when the test ends.
func startServer(t *testing.T, h Handler) (*Server, string) {
	t.Helper()
	return startServerWithLimit(t, h, defaultQueryLimit)
}

func startServerWithLimit(t *testing.T, h Handler, queryLimit int) (*Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(h, queryLimit)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	t.Cleanup(func() {
		srv.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve returned %v, want nil after Close", err)
		}
	})
	return srv, ln.Addr().String()
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func expectRead(t *testing.T, conn net.Conn, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v (want %q)", err, want)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestServer_RoundTrip(t *testing.T) {
	_, addr := startServer(t, echoHandler)
	conn := dial(t, addr)

	conn.Write(command("SET", "k", "v"))
	expectRead(t, conn, reply("SET k v"))
}

// TCP는 스트림이므로 한 명령이 여러 조각으로 도착해도 처리되어야 한다.
func TestServer_FragmentedCommand(t *testing.T) {
	_, addr := startServer(t, echoHandler)
	conn := dial(t, addr)

	msg := command("SET", "key", "value")
	for i := range msg {
		conn.Write(msg[i : i+1])
		time.Sleep(time.Millisecond)
	}
	expectRead(t, conn, reply("SET key value"))
}

func TestServer_PipelinedCommands(t *testing.T) {
	_, addr := startServer(t, echoHandler)
	conn := dial(t, addr)

	var batch []byte
	var want []byte
	for i := 0; i < 3; i++ {
		batch = append(batch, command("CMD", fmt.Sprint(i))...)
		want = append(want, reply(fmt.Sprintf("CMD %d", i))...)
	}
	conn.Write(batch)
	expectRead(t, conn, want)
}

func TestServer_ProtocolErrorClosesConnection(t *testing.T) {
	_, addr := startServer(t, echoHandler)
	conn := dial(t, addr)

	conn.Write([]byte("?garbage\r\n"))
	data, err := io.ReadAll(conn) // 서버가 연결을 닫아야 EOF 로 끝난다
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(data), "-ERR Protocol error") {
		t.Errorf("got %q, want prefix -ERR Protocol error", data)
	}
}

func TestServer_RejectsNonBulkArrayElements(t *testing.T) {
	_, addr := startServer(t, echoHandler)
	conn := dial(t, addr)

	conn.Write([]byte("*1\r\n:1\r\n"))
	data, _ := io.ReadAll(conn)
	if !strings.HasPrefix(string(data), "-ERR Protocol error") {
		t.Errorf("got %q, want prefix -ERR Protocol error", data)
	}
}

// 모든 명령은 단일 executor 에서 실행된다.
// handler 가 락 없이 공유 상태를 건드려도 -race 가 통과해야 한다.
func TestServer_SingleExecutorSerializesCommands(t *testing.T) {
	const clients, perClient = 100, 50
	counter := 0 // 의도적으로 동기화하지 않음
	h := handlerFunc(func(args [][]byte) protocol.Value {
		counter++
		return bulk(string(args[1]))
	})
	srv, addr := startServer(t, h)

	var wg sync.WaitGroup
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			for i := 0; i < perClient; i++ {
				id := fmt.Sprintf("c%d-%d", c, i)
				if _, err := conn.Write(command("ID", id)); err != nil {
					t.Error(err)
					return
				}
				want := reply(id)
				got := make([]byte, len(want))
				if _, err := io.ReadFull(conn, got); err != nil || string(got) != string(want) {
					t.Errorf("client %d: got %q err %v, want %q", c, got, err, want)
					return
				}
			}
		}(c)
	}
	wg.Wait()

	srv.Close() // executor 종료까지 기다리므로 이후 counter 읽기는 안전하다
	if counter != clients*perClient {
		t.Errorf("counter = %d, want %d", counter, clients*perClient)
	}
}

func TestServer_CloseDisconnectsIdleClients(t *testing.T) {
	srv, addr := startServer(t, echoHandler)
	conn := dial(t, addr)
	conn.Write(command("PING"))
	expectRead(t, conn, reply("PING")) // 연결이 서버에 등록된 것을 보장

	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return; goroutine leak")
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("expected client connection to be closed")
	}
}

// 완결되지 못한 채 쌓이는 query buffer 가 상한을 넘으면 에러 응답 후 연결을 끊어야 한다.
func TestServer_QueryBufferLimitClosesConnection(t *testing.T) {
	const limit = 1024
	_, addr := startServerWithLimit(t, echoHandler, limit)
	conn := dial(t, addr)

	// 2000바이트 bulk 를 선언하고 상한보다 많은 본문만 보낸다(아직 미완성).
	conn.Write([]byte("*1\r\n$2000\r\n" + strings.Repeat("a", limit+100)))

	want := protocol.Append(nil, errorReply("Protocol error: query buffer limit exceeded"))
	expectRead(t, conn, want)
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read after error: err = %v, want io.EOF", err)
	}
}

// 상한은 "미완성으로 남은 부분"에만 적용된다. 완결된 명령이 한 번에 상한보다 많이 와도 모두 처리한다.
func TestServer_QueryBufferLimitIgnoresCompleteCommands(t *testing.T) {
	_, addr := startServerWithLimit(t, echoHandler, 64)
	conn := dial(t, addr)

	const n = 20 // 명령 20개 * 약 24바이트 > 64바이트
	var req, want []byte
	for i := 0; i < n; i++ {
		req = append(req, command("ECHO", "hello")...)
		want = append(want, reply("ECHO hello")...)
	}
	conn.Write(req)
	expectRead(t, conn, want)
}

// 큰 명령을 한 번 받은 연결이 그 메모리를 연결 종료까지 쥐고 있으면 안 된다.
func TestCompactQuery(t *testing.T) {
	t.Run("keeps remaining bytes", func(t *testing.T) {
		got := compactQuery([]byte("done|rest"), 5)
		if string(got) != "rest" {
			t.Errorf("got %q, want %q", got, "rest")
		}
	})
	t.Run("small buffer is compacted in place", func(t *testing.T) {
		buf := make([]byte, 0, 4096)
		buf = append(buf, "done|rest"...)
		got := compactQuery(buf, 5)
		if cap(got) != 4096 {
			t.Errorf("cap = %d, want 4096 (재할당 없이 재사용)", cap(got))
		}
	})
	t.Run("large mostly-empty buffer is released", func(t *testing.T) {
		buf := make([]byte, 0, 8*idleQueryCap)
		buf = append(buf, "done|rest"...)
		got := compactQuery(buf, 5)
		if string(got) != "rest" {
			t.Errorf("got %q, want %q", got, "rest")
		}
		if cap(got) > idleQueryCap {
			t.Errorf("cap = %d, want <= %d", cap(got), idleQueryCap)
		}
	})
	t.Run("large buffer still mostly full is kept", func(t *testing.T) {
		buf := make([]byte, 4*idleQueryCap)
		got := compactQuery(buf, 0)
		if len(got) != len(buf) {
			t.Errorf("len = %d, want %d", len(got), len(buf))
		}
	})
}

// Handler 가 panic 해도 executor 는 살아 있어야 한다.
func TestServer_HandlerPanicIsRecovered(t *testing.T) {
	h := handlerFunc(func(args [][]byte) protocol.Value {
		if string(args[0]) == "BOOM" {
			panic("boom")
		}
		return echoHandler.Handle(args)
	})
	srv, addr := startServer(t, h)
	conn := dial(t, addr)

	conn.Write(command("BOOM"))
	expectRead(t, conn, protocol.Append(nil, errorReply("internal error")))

	// 같은 연결과 다른 연결 모두 이후 명령이 정상 처리된다.
	conn.Write(command("PING"))
	expectRead(t, conn, reply("PING"))
	other := dial(t, addr)
	other.Write(command("PING"))
	expectRead(t, other, reply("PING"))

	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after handler panic")
	}
}

// panic 은 pipeline 중간에 나도 그 명령 자리에만 에러가 들어가고 응답 순서는 유지된다.
func TestServer_HandlerPanicKeepsPipelineOrder(t *testing.T) {
	h := handlerFunc(func(args [][]byte) protocol.Value {
		if string(args[0]) == "BOOM" {
			panic("boom")
		}
		return echoHandler.Handle(args)
	})
	_, addr := startServer(t, h)
	conn := dial(t, addr)

	conn.Write(append(append(command("A"), command("BOOM")...), command("B")...))
	want := append(append(reply("A"), protocol.Append(nil, errorReply("internal error"))...), reply("B")...)
	expectRead(t, conn, want)
}
