package voice

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// oggPage builds one Ogg page with the given lacing table and payload. The demuxer ignores every
// header field except the segment count, so version/granule/serial/seq/crc are only shaped, not valid.
func oggPage(t *testing.T, seq uint32, lacing []byte, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("OggS")
	b.WriteByte(0)                                         // version
	b.WriteByte(0)                                         // flags
	b.Write(make([]byte, 8))                               // granule position
	b.Write(binary.LittleEndian.AppendUint32(nil, 0xB01A)) // serial
	b.Write(binary.LittleEndian.AppendUint32(nil, seq))
	b.Write(make([]byte, 4)) // crc — unchecked by the demuxer
	nsegs, ok := segmentCount(len(lacing))
	if !ok {
		t.Fatalf("a page holds at most 255 segments, got %d", len(lacing))
	}
	b.WriteByte(nsegs)
	b.Write(lacing)
	b.Write(data)
	return b.Bytes()
}

// segmentCount narrows the lacing length to the single byte the page header carries.
func segmentCount(n int) (uint8, bool) {
	if n < 0 || n > 255 {
		return 0, false
	}
	return uint8(n), true
}

func fill(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}

func TestOggDemuxSkipsHeadersAndSpansPages(t *testing.T) {
	head := append([]byte("OpusHead"), fill(11, 0x01)...) // 19 bytes
	tags := append([]byte("OpusTags"), fill(8, 0x02)...)  // 16 bytes

	audio1 := fill(100, 0xA1)
	audio2 := fill(300, 0xA2) // 300 > 255, so it needs two lacing values and straddles a page break
	audio3 := fill(80, 0xA3)

	var stream bytes.Buffer
	stream.Write(oggPage(t, 0, []byte{19}, head))
	stream.Write(oggPage(t, 1, []byte{16}, tags))
	// audio1 completes (100 < 255); audio2 starts with a full 255-byte segment that continues onward.
	stream.Write(oggPage(t, 2, []byte{100, 255}, append(append([]byte{}, audio1...), audio2[:255]...)))
	// the 45-byte remainder finishes audio2, then audio3 completes on its own.
	stream.Write(oggPage(t, 3, []byte{45, 80}, append(append([]byte{}, audio2[255:]...), audio3...)))

	r := newOggOpusReader(bytes.NewReader(stream.Bytes()))

	var got [][]byte
	for {
		pkt, err := r.ProvideOpusFrame()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ProvideOpusFrame: %v", err)
		}
		got = append(got, pkt)
	}

	want := [][]byte{audio1, audio2, audio3}
	if len(got) != len(want) {
		t.Fatalf("got %d packets, want %d (the two header packets must be skipped)", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("packet %d: got %d bytes, want %d bytes", i, len(got[i]), len(want[i]))
		}
	}
	if bytes.Contains(bytes.Join(got, nil), []byte("OpusHead")) {
		t.Error("OpusHead leaked into the audio packets")
	}
}

func TestOggDemuxEmptyStreamIsEOF(t *testing.T) {
	r := newOggOpusReader(bytes.NewReader(nil))
	if _, err := r.ProvideOpusFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
}
