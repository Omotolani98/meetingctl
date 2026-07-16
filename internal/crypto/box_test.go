package crypto

import (
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := NewBox("test", key)
	if err != nil {
		t.Fatal(err)
	}
	keyID, nonce, cipher, err := box.Seal("hello meeting")
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "test" {
		t.Fatalf("key id %q", keyID)
	}
	if nonce == "" || cipher == "" {
		t.Fatal("empty seal fields")
	}
	plain, err := box.Open(keyID, nonce, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "hello meeting" {
		t.Fatalf("got %q", plain)
	}
}

func TestDecodeHexKey(t *testing.T) {
	hexKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key, err := decodeKey(hexKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("len %d", len(key))
	}
}
