package p2p

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/p2p/syncengine"
)

// Block payloads travel as base64 inside JSON, which inflates them by a
// third before a single byte leaves the machine. Save data — JSON, XML,
// name tables, repeated struct dumps — usually compresses several times over,
// so gzipping first more than pays that back on a link where bandwidth, not
// CPU, is the limit.
//
// The negotiation is deliberately one-sided and self-describing: a requester
// advertises what it can decode, and the responder marks what it actually
// did, per block. A peer that predates this never advertises anything, so it
// never receives an encoded block; and a response from such a peer carries no
// marker, so it is read as raw. Neither side needs to know the other's
// version.
const encodingGzip = "gzip"

// gzipEncodings is what a requester advertises. Sent only over the relay:
// on a LAN the link is usually faster than the compressor, so paying CPU to
// save bytes is the wrong trade.
var gzipEncodings = []string{encodingGzip}

// compressionWorthwhile is the ratio a block must beat to be sent encoded.
// Already-compressed saves (some games ship zipped or encrypted blobs) gain
// nothing from a second pass, and shipping them encoded would cost the
// receiver a pointless decompress.
const compressionWorthwhile = 0.9

// wantsGzip reports whether a requester's advertised encodings include gzip.
func wantsGzip(encodings []string) bool {
	for _, e := range encodings {
		if e == encodingGzip {
			return true
		}
	}
	return false
}

// encodeBlocks turns freshly read blocks into wire form, compressing
// individually where it helps and the requester asked for it.
func encodeBlocks(blocks []delta.BlockSource, allowGzip bool) []syncengine.BlockData {
	out := make([]syncengine.BlockData, len(blocks))
	for i, b := range blocks {
		out[i] = syncengine.BlockData{Index: b.Index, Data: b.Data, Length: len(b.Data)}
		if !allowGzip || len(b.Data) == 0 {
			continue
		}
		packed, err := gzipBytes(b.Data)
		if err != nil || float64(len(packed)) > float64(len(b.Data))*compressionWorthwhile {
			continue // incompressible: send it raw rather than pay for nothing
		}
		out[i].Data = packed
		out[i].Enc = encodingGzip
	}
	return out
}

// decodeBlocks restores any encoded block to its raw bytes, so the sync
// engine only ever handles real save data. It rewrites the slice in place;
// callers pass a freshly decoded response they own.
func decodeBlocks(blocks []syncengine.BlockData) ([]syncengine.BlockData, error) {
	for i, b := range blocks {
		switch b.Enc {
		case "":
			continue
		case encodingGzip:
			raw, err := gunzipBytes(b.Data, b.Length)
			if err != nil {
				return nil, fmt.Errorf("decompress block %d: %w", b.Index, err)
			}
			blocks[i].Data = raw
			blocks[i].Enc = ""
		default:
			return nil, fmt.Errorf("block %d uses unsupported encoding %q", b.Index, b.Enc)
		}
	}
	return blocks, nil
}

func gzipBytes(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(raw) / 2)
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// gunzipBytes decompresses into a buffer sized from the manifest, and refuses
// to keep going past it — a peer must not be able to make this side allocate
// without bound by claiming a small block and sending a huge stream.
func gunzipBytes(packed []byte, expected int) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	if expected < 0 {
		return nil, fmt.Errorf("negative block length %d", expected)
	}
	out := make([]byte, 0, expected)
	raw, err := io.ReadAll(io.LimitReader(zr, int64(expected)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > expected {
		return nil, fmt.Errorf("block expands to more than its declared %d bytes", expected)
	}
	return append(out, raw...), nil
}
