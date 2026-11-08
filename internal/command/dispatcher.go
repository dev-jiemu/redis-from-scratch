package command

import (
	"math"
	"strconv"
	"strings"

	"redis-from-scratch/internal/protocol"
	"redis-from-scratch/internal/store"
)

// maxEchoedInput bounds how many bytes of client input an error message
// repeats back (Redis uses 128), so one huge argument cannot be amplified.
const maxEchoedInput = 128

// command is one entry of the command table.
type command struct {
	name string
	// arity follows Redis: N > 0 means exactly N arguments including the
	// command name, -N means at least N.
	arity int
	run   func(db *store.DB, args [][]byte) protocol.Value
}

func (c *command) arityOK(argc int) bool {
	if c.arity >= 0 {
		return argc == c.arity
	}
	return argc >= -c.arity
}

var table = map[string]*command{
	"ping":   {"ping", -1, ping},
	"echo":   {"echo", 2, echo},
	"get":    {"get", 2, get},
	"set":    {"set", 3, set},
	"del":    {"del", -2, del},
	"exists": {"exists", -2, exists},
	"incr":   {"incr", 2, incr},
	"decr":   {"decr", 2, decr},
	"append": {"append", 3, cmdAppend},
	"mget":   {"mget", -2, mget},
	"mset":   {"mset", -3, mset},
	"setnx":  {"setnx", 3, setnx},
}

// Dispatcher looks up a command and runs it against a DB. It implements
// network.Handler and is called from the single executor goroutine only.
type Dispatcher struct {
	db *store.DB
}

func New(db *store.DB) *Dispatcher {
	return &Dispatcher{db: db}
}

// Handle runs one command. args is non-empty: args[0] is the command name.
// Arguments are validated before anything is changed, so a rejected command
// never leaves partial state behind.
func (d *Dispatcher) Handle(args [][]byte) protocol.Value {
	cmd, ok := table[strings.ToLower(string(args[0]))]
	if !ok {
		return unknownCommand(args)
	}
	if !cmd.arityOK(len(args)) {
		return wrongArity(cmd.name)
	}
	return cmd.run(d.db, args)
}

func ping(_ *store.DB, args [][]byte) protocol.Value {
	switch len(args) {
	case 1:
		return simple("PONG")
	case 2:
		return bulk(args[1])
	default:
		return wrongArity("ping")
	}
}

func echo(_ *store.DB, args [][]byte) protocol.Value {
	return bulk(args[1])
}

func get(db *store.DB, args [][]byte) protocol.Value {
	obj, ok := db.Get(string(args[1]))
	if !ok {
		return protocol.Value{Kind: protocol.BulkString, Null: true}
	}
	return bulk(obj.Str)
}

func set(db *store.DB, args [][]byte) protocol.Value {
	db.Set(string(args[1]), store.NewString(args[2]))
	return simple("OK")
}

func del(db *store.DB, args [][]byte) protocol.Value {
	var n int64
	for _, key := range args[1:] {
		if db.Delete(string(key)) {
			n++
		}
	}
	return integer(n)
}

func exists(db *store.DB, args [][]byte) protocol.Value {
	var n int64
	for _, key := range args[1:] {
		if db.Exists(string(key)) {
			n++
		}
	}
	return integer(n)
}

func incr(db *store.DB, args [][]byte) protocol.Value {
	return incrBy(db, string(args[1]), 1)
}

func decr(db *store.DB, args [][]byte) protocol.Value {
	return incrBy(db, string(args[1]), -1)
}

func incrBy(db *store.DB, key string, delta int64) protocol.Value {
	var cur int64
	if obj, ok := db.Get(key); ok {
		n, err := strconv.ParseInt(string(obj.Str), 10, 64)
		if err != nil {
			return errNotInteger()
		}
		cur = n
	}
	if (delta > 0 && cur > math.MaxInt64-delta) || (delta < 0 && cur < math.MinInt64-delta) {
		return errOverflow()
	}
	result := cur + delta
	db.Set(key, store.NewString(strconv.AppendInt(nil, result, 10)))
	return integer(result)
}

func cmdAppend(db *store.DB, args [][]byte) protocol.Value {
	key := string(args[1])
	var base []byte
	if obj, ok := db.Get(key); ok {
		base = obj.Str
	}
	newVal := make([]byte, len(base)+len(args[2]))
	copy(newVal, base)
	copy(newVal[len(base):], args[2])
	db.Set(key, store.NewString(newVal))
	return integer(int64(len(newVal)))
}

func mget(db *store.DB, args [][]byte) protocol.Value {
	elems := make([]protocol.Value, len(args)-1)
	for i, key := range args[1:] {
		if obj, ok := db.Get(string(key)); ok {
			elems[i] = bulk(obj.Str)
		} else {
			elems[i] = protocol.Value{Kind: protocol.BulkString, Null: true}
		}
	}
	return protocol.Value{Kind: protocol.Array, Array: elems}
}

func mset(db *store.DB, args [][]byte) protocol.Value {
	if len(args[1:])%2 != 0 {
		return wrongArity("mset")
	}
	for i := 1; i < len(args); i += 2 {
		db.Set(string(args[i]), store.NewString(args[i+1]))
	}
	return simple("OK")
}

func setnx(db *store.DB, args [][]byte) protocol.Value {
	key := string(args[1])
	if db.Exists(key) {
		return integer(0)
	}
	db.Set(key, store.NewString(args[2]))
	return integer(1)
}

func errNotInteger() protocol.Value {
	return errReply("value is not an integer or out of range")
}

func errOverflow() protocol.Value {
	return errReply("increment or decrement would overflow")
}

func unknownCommand(args [][]byte) protocol.Value {
	msg := make([]byte, 0, 2*maxEchoedInput)
	msg = append(msg, "unknown command '"...)
	msg = append(msg, clip(args[0], maxEchoedInput)...)
	msg = append(msg, '\'')
	if len(args) == 1 {
		return errReply(string(msg))
	}
	msg = append(msg, ", with args beginning with: "...)

	var rest []byte
	for i := 1; i < len(args) && len(rest) < maxEchoedInput; i++ {
		rest = append(rest, '\'')
		rest = append(rest, clip(args[i], maxEchoedInput-len(rest))...)
		rest = append(rest, '\'', ' ')
	}
	return errReply(string(append(msg, rest...)))
}

func wrongArity(name string) protocol.Value {
	return errReply("wrong number of arguments for '" + name + "' command")
}

func clip(b []byte, max int) []byte {
	if len(b) > max {
		return b[:max]
	}
	return b
}

func errReply(msg string) protocol.Value {
	return protocol.Value{Kind: protocol.Error, Str: []byte("ERR " + msg)}
}

func simple(s string) protocol.Value {
	return protocol.Value{Kind: protocol.SimpleString, Str: []byte(s)}
}

func bulk(b []byte) protocol.Value {
	return protocol.Value{Kind: protocol.BulkString, Str: b}
}

func integer(n int64) protocol.Value {
	return protocol.Value{Kind: protocol.Integer, Int: n}
}
