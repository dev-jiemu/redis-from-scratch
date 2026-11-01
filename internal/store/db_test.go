package store

import "testing"

func TestDB_GetMissing(t *testing.T) {
	db := New()
	if obj, ok := db.Get("nope"); ok || obj != nil {
		t.Errorf("Get(missing) = %v, %v; want nil, false", obj, ok)
	}
}

func TestDB_SetThenGet(t *testing.T) {
	db := New()
	db.Set("k", NewString([]byte("v")))

	obj, ok := db.Get("k")
	if !ok {
		t.Fatal("Get(k) not found")
	}
	if obj.Type != TypeString || string(obj.Str) != "v" {
		t.Errorf("got %+v, want string object holding %q", obj, "v")
	}
}

func TestDB_SetOverwrites(t *testing.T) {
	db := New()
	db.Set("k", NewString([]byte("old")))
	db.Set("k", NewString([]byte("new")))

	obj, _ := db.Get("k")
	if string(obj.Str) != "new" {
		t.Errorf("got %q, want %q", obj.Str, "new")
	}
	if db.Len() != 1 {
		t.Errorf("Len = %d, want 1", db.Len())
	}
}

func TestDB_Delete(t *testing.T) {
	db := New()
	db.Set("k", NewString([]byte("v")))

	if !db.Delete("k") {
		t.Error("Delete(existing) = false, want true")
	}
	if db.Delete("k") {
		t.Error("Delete(already deleted) = true, want false")
	}
	if _, ok := db.Get("k"); ok {
		t.Error("key still present after Delete")
	}
}

func TestDB_Exists(t *testing.T) {
	db := New()
	if db.Exists("k") {
		t.Error("Exists(missing) = true")
	}
	db.Set("k", NewString([]byte("v")))
	if !db.Exists("k") {
		t.Error("Exists(present) = false")
	}
}

// 키는 바이너리 안전해야 한다(빈 문자열, NUL, CRLF 포함 가능).
func TestDB_BinarySafeKeys(t *testing.T) {
	db := New()
	keys := []string{"", "a\x00b", "a\r\nb"}
	for i, k := range keys {
		db.Set(k, NewString([]byte{byte(i)}))
	}
	for i, k := range keys {
		obj, ok := db.Get(k)
		if !ok || obj.Str[0] != byte(i) {
			t.Errorf("key %q: got %+v, %v", k, obj, ok)
		}
	}
}
