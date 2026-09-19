package sandbox

import "testing"

func TestUserRootfsCapacity(t *testing.T) {
	got := UserRootfsCapacity(5 * 1024 * 1024) // busybox-sized
	if got.Value() != 256*1024*1024 {
		t.Fatalf("small image cap %d want 256Mi", got.Value())
	}
	got = UserRootfsCapacity(400 * 1024 * 1024)
	want := int64((400 + 128) * 1024 * 1024)
	if got.Value() != want {
		t.Fatalf("large image cap %d want %d", got.Value(), want)
	}
}
