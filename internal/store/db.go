package store

// Type is the logical data type of an Object.
type Type uint8

const (
	TypeString Type = iota
)

// Object is a value stored under a key (Redis robj). Phase 1 only holds
// strings; later phases add types and encodings.
type Object struct {
	Type Type
	// Str must never be modified in place; replace it with a new slice instead.
	// A reply shares these bytes with a connection goroutine that copies them
	// after the executor has moved on, so in-place changes would be a data race.
	Str []byte // TypeString
}

// NewString returns a string Object that owns s.
func NewString(s []byte) *Object {
	return &Object{Type: TypeString, Str: s}
}

// DB is the in-memory keyspace. It is not safe for concurrent use: the server
// runs every command on a single executor goroutine, so it needs no locking.
type DB struct {
	data map[string]*Object
}

func New() *DB {
	return &DB{data: make(map[string]*Object)}
}

// Get returns the object stored at key.
func (db *DB) Get(key string) (*Object, bool) {
	obj, ok := db.data[key]
	return obj, ok
}

// Set stores obj at key, replacing any existing value.
func (db *DB) Set(key string, obj *Object) {
	db.data[key] = obj
}

// Delete removes key and reports whether it existed.
func (db *DB) Delete(key string) bool {
	_, ok := db.data[key]
	delete(db.data, key)
	return ok
}

func (db *DB) Exists(key string) bool {
	_, ok := db.data[key]
	return ok
}

// Len returns the number of keys.
func (db *DB) Len() int {
	return len(db.data)
}
