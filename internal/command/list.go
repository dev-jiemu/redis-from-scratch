package command

import (
	"strconv"

	"redis-from-scratch/internal/protocol"
	"redis-from-scratch/internal/store"
)

// getList returns the list at key. obj is nil when the key does not exist;
// errv is non-nil when the key holds another type.
func getList(db *store.DB, key string) (obj *store.Object, errv *protocol.Value) {
	obj, ok := db.Get(key)
	if !ok {
		return nil, nil
	}
	if obj.Type != store.TypeList {
		e := errWrongType()
		return nil, &e
	}
	return obj, nil
}

func lpush(db *store.DB, args [][]byte) protocol.Value { return push(db, args, true) }
func rpush(db *store.DB, args [][]byte) protocol.Value { return push(db, args, false) }

// push never modifies the existing slice in place: LPUSH builds a new one and
// RPUSH only appends past the current length.
func push(db *store.DB, args [][]byte, left bool) protocol.Value {
	key := string(args[1])
	obj, errv := getList(db, key)
	if errv != nil {
		return *errv
	}
	var cur [][]byte
	if obj != nil {
		cur = obj.List
	}
	vals := args[2:]
	var next [][]byte
	if left {
		// Redis pushes one element at a time, so the last argument ends up first.
		next = make([][]byte, 0, len(vals)+len(cur))
		for i := len(vals) - 1; i >= 0; i-- {
			next = append(next, vals[i])
		}
		next = append(next, cur...)
	} else {
		next = append(cur, vals...)
	}
	db.Set(key, store.NewList(next))
	return integer(int64(len(next)))
}

func llen(db *store.DB, args [][]byte) protocol.Value {
	obj, errv := getList(db, string(args[1]))
	if errv != nil {
		return *errv
	}
	if obj == nil {
		return integer(0)
	}
	return integer(int64(len(obj.List)))
}

func lrange(db *store.DB, args [][]byte) protocol.Value {
	start, err := strconv.ParseInt(string(args[2]), 10, 64)
	if err != nil {
		return errNotInteger()
	}
	stop, err := strconv.ParseInt(string(args[3]), 10, 64)
	if err != nil {
		return errNotInteger()
	}
	obj, errv := getList(db, string(args[1]))
	if errv != nil {
		return *errv
	}
	elems := []protocol.Value{}
	if obj == nil {
		return protocol.Value{Kind: protocol.Array, Array: elems}
	}
	n := int64(len(obj.List))
	if start < 0 {
		start += n
	}
	if start < 0 {
		start = 0
	}
	if stop < 0 {
		stop += n
	}
	if stop >= n {
		stop = n - 1
	}
	for i := start; i <= stop; i++ {
		elems = append(elems, bulk(obj.List[i]))
	}
	return protocol.Value{Kind: protocol.Array, Array: elems}
}

func lpop(db *store.DB, args [][]byte) protocol.Value { return pop(db, args, true) }
func rpop(db *store.DB, args [][]byte) protocol.Value { return pop(db, args, false) }

// pop removes by re-slicing, never by editing the slice in place. A list that
// becomes empty is deleted, as in Redis.
func pop(db *store.DB, args [][]byte, left bool) protocol.Value {
	name := "rpop"
	if left {
		name = "lpop"
	}
	if len(args) > 3 {
		return wrongArity(name)
	}
	hasCount := len(args) == 3
	var count int64 = 1
	if hasCount {
		// Redis uses the same message for a non-integer and a negative count.
		n, err := strconv.ParseInt(string(args[2]), 10, 64)
		if err != nil || n < 0 {
			return errReply("value is out of range, must be positive")
		}
		count = n
	}

	key := string(args[1])
	obj, errv := getList(db, key)
	if errv != nil {
		return *errv
	}
	if obj == nil {
		if hasCount {
			return protocol.Value{Kind: protocol.Array, Null: true}
		}
		return protocol.Value{Kind: protocol.BulkString, Null: true}
	}

	list := obj.List
	if count > int64(len(list)) {
		count = int64(len(list))
	}
	n := int(count)
	popped := make([]protocol.Value, n)
	var rest [][]byte
	if left {
		for i := 0; i < n; i++ {
			popped[i] = bulk(list[i])
		}
		rest = list[n:]
	} else {
		for i := 0; i < n; i++ {
			popped[i] = bulk(list[len(list)-1-i])
		}
		rest = list[:len(list)-n]
	}
	if len(rest) == 0 {
		db.Delete(key)
	} else if n > 0 {
		db.Set(key, store.NewList(rest))
	}

	if hasCount {
		return protocol.Value{Kind: protocol.Array, Array: popped}
	}
	return popped[0]
}
