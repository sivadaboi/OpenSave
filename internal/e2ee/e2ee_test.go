package e2ee

import (
	"bytes"
	"strings"
	"testing"
)

// The property the whole scheme rests on: two devices, exchanging only public
// halves, arrive at the same key without it ever crossing the wire.
func TestBothSidesDeriveTheSameKey(t *testing.T) {
	a, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}

	fromA, err := SharedKey(a.Private, b.Public)
	if err != nil {
		t.Fatal(err)
	}
	fromB, err := SharedKey(b.Private, a.Public)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromA, fromB) {
		t.Fatal("the two devices derived different keys; nothing either sealed could be opened by the other")
	}
	if bytes.Equal(fromA, make([]byte, len(fromA))) {
		t.Fatal("derived an all-zero key")
	}
}

// A third device must not land on the same key, or pairing would grant nothing.
func TestAStrangerDerivesADifferentKey(t *testing.T) {
	a, _ := GenerateIdentity()
	b, _ := GenerateIdentity()
	eve, _ := GenerateIdentity()

	pair, _ := SharedKey(a.Private, b.Public)
	stranger, _ := SharedKey(eve.Private, a.Public)
	if bytes.Equal(pair, stranger) {
		t.Fatal("an unpaired device derived the pair's key")
	}

	// And what the pair sealed must not open with the stranger's key.
	sealed, err := Seal(pair, []byte("save data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(stranger, sealed); err == nil {
		t.Fatal("a stranger's key opened the pair's payload")
	}
}

func TestSealRoundTrip(t *testing.T) {
	a, _ := GenerateIdentity()
	b, _ := GenerateIdentity()
	key, _ := SharedKey(a.Private, b.Public)

	for _, plain := range [][]byte{
		[]byte(""),
		[]byte("a"),
		[]byte("ER0000.sl2 contents, 40 hours in"),
		bytes.Repeat([]byte("save block "), 100000), // ~1 MB, a real block
	} {
		sealed, err := Seal(key, plain)
		if err != nil {
			t.Fatalf("Seal(%d bytes): %v", len(plain), err)
		}
		// Only worth asserting for a plaintext long enough that finding it by
		// chance is impossible. A single byte turns up in forty bytes of
		// ciphertext about one time in seven, which says nothing about the
		// cipher and fails the test at random.
		if len(plain) >= 8 && bytes.Contains(sealed, plain) {
			t.Errorf("the plaintext is visible inside the sealed output")
		}
		got, err := Open(key, sealed)
		if err != nil {
			t.Fatalf("Open(%d bytes): %v", len(plain), err)
		}
		if !bytes.Equal(got, plain) {
			t.Errorf("round trip changed the data (%d bytes in, %d out)", len(plain), len(got))
		}
	}
}

// Tampering has to be caught. A relay that flips one byte must not be able to
// land a corrupted save on disk.
func TestAlteredPayloadIsRejected(t *testing.T) {
	a, _ := GenerateIdentity()
	b, _ := GenerateIdentity()
	key, _ := SharedKey(a.Private, b.Public)

	sealed, _ := Seal(key, []byte("the original save"))
	for _, at := range []int{0, len(sealed) / 2, len(sealed) - 1} {
		bad := append([]byte(nil), sealed...)
		bad[at] ^= 0x01
		if _, err := Open(key, bad); err == nil {
			t.Errorf("a payload altered at byte %d was accepted", at)
		}
	}
	if _, err := Open(key, sealed[:5]); err == nil {
		t.Error("a truncated payload was accepted")
	}
}

// Nonces must not repeat: sealing the same thing twice with one key has to
// produce different bytes, or the construction leaks.
func TestSealIsNotDeterministic(t *testing.T) {
	a, _ := GenerateIdentity()
	b, _ := GenerateIdentity()
	key, _ := SharedKey(a.Private, b.Public)

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		sealed, err := Seal(key, []byte("identical plaintext"))
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(sealed)] {
			t.Fatal("the same ciphertext was produced twice — the nonce is repeating")
		}
		seen[string(sealed)] = true
	}
}

// Both devices must show the same fingerprint, though each holds the two keys
// in the opposite order. If they differ, comparing them proves nothing and the
// check people are asked to perform is theatre.
func TestFingerprintIsTheSameOnBothDevices(t *testing.T) {
	a, _ := GenerateIdentity()
	b, _ := GenerateIdentity()

	onA := Fingerprint(a.Public, b.Public)
	onB := Fingerprint(b.Public, a.Public)
	if onA != onB {
		t.Fatalf("the two devices show different fingerprints:\n  %s\n  %s", onA, onB)
	}
	if strings.TrimSpace(onA) == "" {
		t.Fatal("empty fingerprint")
	}
}

// A different pairing must look different, or the comparison catches nothing.
func TestFingerprintDiffersPerPairing(t *testing.T) {
	a, _ := GenerateIdentity()
	b, _ := GenerateIdentity()
	imposter, _ := GenerateIdentity()

	real := Fingerprint(a.Public, b.Public)
	swapped := Fingerprint(a.Public, imposter.Public)
	if real == swapped {
		t.Fatal("a substituted key produced the same fingerprint — the out-of-band check would not catch a relay in the middle")
	}
}

func TestFingerprintIsReadable(t *testing.T) {
	a, _ := GenerateIdentity()
	b, _ := GenerateIdentity()
	fp := Fingerprint(a.Public, b.Public)

	groups := strings.Fields(fp)
	if len(groups) != 6 {
		t.Errorf("got %d groups (%q); six is short enough to read aloud", len(groups), fp)
	}
	for _, g := range groups {
		if len(g) != 4 {
			t.Errorf("group %q is not 4 characters", g)
		}
	}
}

func TestKeyEncodingRoundTrip(t *testing.T) {
	id, _ := GenerateIdentity()
	got, err := DecodeKey(EncodeKey(id.Public))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, id.Public) {
		t.Error("a key did not survive encode/decode")
	}
}

// Anything that is not a key must be refused before it reaches the curve,
// where the failure would be far less clear.
func TestDecodeKeyRejectsRubbish(t *testing.T) {
	for _, s := range []string{"", "not base64!!", "c2hvcnQ=", strings.Repeat("A", 64)} {
		if _, err := DecodeKey(s); err == nil {
			t.Errorf("DecodeKey(%q) was accepted", s)
		}
	}
}

func TestSharedKeyRejectsWrongSizedKeys(t *testing.T) {
	id, _ := GenerateIdentity()
	if _, err := SharedKey(id.Private, []byte("too short")); err == nil {
		t.Error("a short peer key was accepted")
	}
	if _, err := SharedKey([]byte("too short"), id.Public); err == nil {
		t.Error("a short private key was accepted")
	}
}
