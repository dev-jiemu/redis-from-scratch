package network

import (
	"errors"
	"net"
	"sync"

	"redis-from-scratch/internal/protocol"
)

const readSize = 16 * 1024

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
	handler Handler
	reqs    chan request

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
	s := &Server{
		handler:  h,
		reqs:     make(chan request),
		execDone: make(chan struct{}),
		active:   make(map[net.Conn]struct{}),
	}
	go s.execute()
	return s
}

func (s *Server) execute() {
	defer close(s.execDone)
	for r := range s.reqs {
		r.reply <- s.handler.Handle(r.args)
	}
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

		if len(out) > 0 {
			if _, err := conn.Write(out); err != nil {
				return
			}
			out = out[:0]
		}
		query = append(query[:0], query[consumed:]...)

		if readErr != nil {
			return
		}
	}
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
