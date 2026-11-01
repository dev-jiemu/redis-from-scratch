package network

import (
	"errors"
	"log"
	"net"
	"runtime/debug"
	"sync"

	"redis-from-scratch/internal/protocol"
)

const (
	readSize = 16 * 1024

	// defaultQueryLimit는 Redis client-query-buffer-limit 기본값(1GB)과 같다.
	defaultQueryLimit = 1 << 30

	// idleQueryCap보다 큰 query buffer는 내용이 거의 비면 반납한다.
	idleQueryCap = 64 * 1024
)

// Handler executes one parsed command. The server calls it from a single
// executor goroutine only, so implementations need no locking.
type Handler interface {
	Handle(args [][]byte) protocol.Value
}

type request struct {
	args  [][]byte
	reply chan<- protocol.Value
}

// Server accepts TCP connections, one goroutine per connection for I/O and
// parsing, and runs every command on one executor goroutine, like Redis's
// single-threaded command execution.
type Server struct {
	handler    Handler
	reqs       chan request
	queryLimit int // 연결당 미완성 query buffer 상한(바이트)

	// connections
	execDone chan struct{}
	conns    sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	listener net.Listener
	active   map[net.Conn]struct{}
	closeOne sync.Once
}

// New creates a Server and starts its executor. Close must be called to stop it.
func New(h Handler) *Server {
	return newServer(h, defaultQueryLimit)
}

func newServer(h Handler, queryLimit int) *Server {
	s := &Server{
		handler:    h,
		reqs:       make(chan request),
		queryLimit: queryLimit,
		execDone:   make(chan struct{}),
		active:     make(map[net.Conn]struct{}),
	}
	go s.execute()
	return s
}

func (s *Server) execute() {
	defer close(s.execDone)
	for r := range s.reqs {
		r.reply <- s.handle(r.args)
	}
}

// handle runs the handler and turns a panic into an error reply. If the
// executor died instead, connection goroutines would block on their reply
// channel forever and Close could never return.
func (s *Server) handle(args [][]byte) (reply protocol.Value) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler: %v\n%s", r, debug.Stack())
			reply = errorReply("internal error")
		}
	}()
	return s.handler.Handle(args)
}

// Serve accepts connections on ln until Close is called, then returns nil.
// Any other accept failure is returned.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ln.Close()
		return nil
	}
	s.listener = ln
	s.mu.Unlock()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		if !s.track(conn) {
			conn.Close()
			return nil
		}
		go s.handleConn(conn)
	}
}

// conn을 추적하도록 등록해서 Close가 해당 연결에 접근할 수 있게 한다. 연결이 닫히면 등록 여부를 false로 반환한다.
func (s *Server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.active[conn] = struct{}{}
	s.conns.Add(1)
	return true
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	delete(s.active, conn)
	s.mu.Unlock()
}

// Close : 새로운 연결을 받는 것을 중단하고, 모든 클라이언트의 연결을 끊은 뒤, 서버의 모든 고루틴이 종료될 때까지 기다린다. 여러 번 호출해도 안전하다.
func (s *Server) Close() error {
	s.closeOne.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.listener != nil {
			s.listener.Close()
		}
		for conn := range s.active {
			conn.Close()
		}
		s.mu.Unlock()

		s.conns.Wait()
		close(s.reqs) // 연결 goroutine 이 모두 끝난 뒤에만 닫아야 send-on-closed 가 없다
		<-s.execDone
	})
	return nil
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.conns.Done()
	defer s.untrack(conn)
	defer conn.Close()

	replyCh := make(chan protocol.Value, 1)
	readBuf := make([]byte, readSize)
	var query []byte // 아직 완성되지 않은 명령을 포함하는 query buffer
	var out []byte

	for {
		n, readErr := conn.Read(readBuf)
		query = append(query, readBuf[:n]...)

		consumed := 0
		for {
			v, used, err := protocol.Parse(query[consumed:])
			if errors.Is(err, protocol.ErrIncomplete) {
				break
			}
			if err != nil {
				out = protocol.Append(out, errorReply("Protocol error: "+protoMsg(err)))
				conn.Write(out)
				return
			}
			consumed += used

			args, ok := toArgs(v)
			if !ok {
				out = protocol.Append(out, errorReply("Protocol error: expected array of bulk strings"))
				conn.Write(out)
				return
			}
			if len(args) == 0 {
				continue
			}
			s.reqs <- request{args: args, reply: replyCh}
			out = protocol.Append(out, <-replyCh)
		}

		// 완결된 명령은 이미 처리했으므로, 상한은 아직 미완성으로 남은 부분에만 적용한다.
		if len(query)-consumed > s.queryLimit {
			out = protocol.Append(out, errorReply("Protocol error: query buffer limit exceeded"))
			conn.Write(out)
			return
		}

		if len(out) > 0 {
			if _, err := conn.Write(out); err != nil {
				return
			}
			out = out[:0]
		}
		query = compactQuery(query, consumed)

		if readErr != nil {
			return
		}
	}
}

// compactQuery는 query에서 처리가 끝난 앞 consumed 바이트를 버리고 남은 부분을 반환한다.
// append(query[:0], ...)는 길이만 줄이고 cap은 유지하므로, 큰 명령을 한 번 받은 연결이
// 메모리를 계속 쥐게 된다. 큰 버퍼가 거의 비었으면 새 슬라이스로 옮겨 원본을 GC에 돌려준다.
func compactQuery(query []byte, consumed int) []byte {
	rest := query[consumed:]
	if cap(query) > idleQueryCap && len(rest) <= cap(query)/4 {
		return append([]byte(nil), rest...)
	}
	return append(query[:0], rest...)
}

func errorReply(msg string) protocol.Value {
	return protocol.Value{Kind: protocol.Error, Str: []byte("ERR " + msg)}
}

func protoMsg(err error) string {
	var pe *protocol.ProtocolError
	if errors.As(err, &pe) {
		return pe.Msg
	}
	return err.Error()
}

// toArgs converts a command array of bulk strings into its arguments.
// An empty or null array yields no arguments and is ignored by the caller.
func toArgs(v protocol.Value) ([][]byte, bool) {
	if v.Kind != protocol.Array {
		return nil, false
	}
	args := make([][]byte, 0, len(v.Array))
	for _, e := range v.Array {
		if e.Kind != protocol.BulkString || e.Null {
			return nil, false
		}
		args = append(args, e.Str)
	}
	return args, true
}
