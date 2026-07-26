package p2p

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/p2p/syncengine"
)

func TestEncodeBlocksRoundTrips(t *testing.T) {
	// Save-like content: repetitive JSON, which is what most games write.
	savey := []byte(strings.Repeat(`{"slot":1,"gold":9999,"name":"hero","flags":[0,0,1]},`, 4000))
	blocks := []delta.BlockSource{{Index: 0, Data: savey}}

	wire := encodeBlocks(blocks, true)
	if wire[0].Enc != encodingGzip {
		t.Fatalf("compressible save data was sent raw (enc=%q)", wire[0].Enc)
	}
	if len(wire[0].Data) >= len(savey) {
		t.Errorf("compressed size %d is not smaller than %d", len(wire[0].Data), len(savey))
	}
	if wire[0].Length != len(savey) {
		t.Errorf("Length = %d, want the uncompressed %d — progress accounting depends on it", wire[0].Length, len(savey))
	}
	onWire := len(wire[0].Data) // decodeBlocks rewrites this in place

	got, err := decodeBlocks(wire)
	if err != nil {
		t.Fatalf("decodeBlocks: %v", err)
	}
	if !bytes.Equal(got[0].Data, savey) {
		t.Error("round trip changed the block contents")
	}
	if got[0].Enc != "" {
		t.Error("decoded block still claims an encoding")
	}
	t.Logf("%d bytes -> %d on the wire (%.1fx)", len(savey), onWire,
		float64(len(savey))/float64(onWire))
}

// Compressing an already-compressed save wastes CPU on both ends and can make
// the payload bigger, so incompressible blocks must go out untouched.
func TestEncodeBlocksSkipsIncompressibleData(t *testing.T) {
	noise := make([]byte, 256<<10)
	rand.New(rand.NewSource(7)).Read(noise)

	wire := encodeBlocks([]delta.BlockSource{{Index: 0, Data: noise}}, true)
	if wire[0].Enc != "" {
		t.Errorf("incompressible block was sent encoded (enc=%q)", wire[0].Enc)
	}
	if !bytes.Equal(wire[0].Data, noise) {
		t.Error("raw block was altered")
	}
}

// The whole negotiation rests on this: a peer that never advertises support
// must never be handed something it can't read.
func TestEncodeBlocksLeavesDataRawWhenNotRequested(t *testing.T) {
	savey := []byte(strings.Repeat("aaaabbbbcccc", 5000))

	wire := encodeBlocks([]delta.BlockSource{{Index: 0, Data: savey}}, false)
	if wire[0].Enc != "" {
		t.Fatalf("compressed for a peer that never asked (enc=%q)", wire[0].Enc)
	}
	if !bytes.Equal(wire[0].Data, savey) {
		t.Error("block was altered despite no encoding being requested")
	}
}

// Responses from peers that predate the field carry no marker at all, and
// must pass straight through.
func TestDecodeBlocksPassesThroughUnencoded(t *testing.T) {
	raw := []byte("plain old save bytes")
	got, err := decodeBlocks([]syncengine.BlockData{{Index: 3, Data: raw, Length: len(raw)}})
	if err != nil {
		t.Fatalf("decodeBlocks: %v", err)
	}
	if !bytes.Equal(got[0].Data, raw) {
		t.Error("unencoded block was altered")
	}
}

func TestWantsGzip(t *testing.T) {
	if wantsGzip(nil) {
		t.Error("a peer advertising nothing must not be sent gzip")
	}
	if wantsGzip([]string{"br", "zstd"}) {
		t.Error("unknown encodings must not be read as gzip support")
	}
	if !wantsGzip([]string{"br", encodingGzip}) {
		t.Error("gzip in the advertised list was missed")
	}
}

func TestDecodeBlocksRejectsUnknownEncoding(t *testing.T) {
	_, err := decodeBlocks([]syncengine.BlockData{{Index: 0, Data: []byte("x"), Enc: "brotli"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported encoding") {
		t.Errorf("expected an unsupported-encoding error, got %v", err)
	}
}

// A peer must not be able to make this side allocate arbitrarily by declaring
// a small block and sending a stream that expands far beyond it.
func TestDecodeBlocksRejectsOversizedExpansion(t *testing.T) {
	bomb := bytes.Repeat([]byte{0}, 4<<20)
	packed, err := gzipBytes(bomb)
	if err != nil {
		t.Fatal(err)
	}
	_, err = decodeBlocks([]syncengine.BlockData{
		{Index: 0, Data: packed, Length: 1024, Enc: encodingGzip},
	})
	if err == nil {
		t.Fatal("a block expanding past its declared length was accepted")
	}
	if !strings.Contains(err.Error(), "declared") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeBlocksRejectsCorruptStream(t *testing.T) {
	_, err := decodeBlocks([]syncengine.BlockData{
		{Index: 0, Data: []byte("not actually gzip"), Length: 64, Enc: encodingGzip},
	})
	if err == nil {
		t.Fatal("corrupt gzip stream was accepted")
	}
}
