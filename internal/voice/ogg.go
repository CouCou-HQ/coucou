package voice

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// oggOpusReader demuxes an Ogg Opus file into raw Opus packets, one per ProvideOpusFrame call.
// It implements disgo's voice.OpusFrameProvider. ~60 lines beats a dependency.
//
// Ogg page: "OggS" | version(1) | flags(1) | granule(8) | serial(4) | seq(4) | crc(4) | nsegs(1) | lacing[nsegs] | data
// A packet spans lacing values until one < 255. The first two packets are OpusHead and OpusTags; skip them.
type oggOpusReader struct {
	r       *bufio.Reader
	queue   [][]byte // packets from the current page not yet handed out
	partial []byte   // packet continued onto the next page
	skipped int      // header packets skipped so far (need 2)
	done    bool
}

func newOggOpusReader(r io.Reader) *oggOpusReader {
	return &oggOpusReader{r: bufio.NewReaderSize(r, 64<<10)}
}

func (o *oggOpusReader) ProvideOpusFrame() ([]byte, error) {
	for {
		if len(o.queue) > 0 {
			pkt := o.queue[0]
			o.queue = o.queue[1:]
			if o.skipped < 2 {
				o.skipped++
				continue
			}
			return pkt, nil
		}
		if o.done {
			return nil, io.EOF
		}
		if err := o.readPage(); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				o.done = true
				continue
			}
			return nil, err
		}
	}
}

func (o *oggOpusReader) readPage() error {
	var hdr [27]byte
	if _, err := io.ReadFull(o.r, hdr[:]); err != nil {
		return err
	}
	if string(hdr[:4]) != "OggS" {
		return fmt.Errorf("ogg: bad capture pattern")
	}
	nsegs := int(hdr[26])
	lacing := make([]byte, nsegs)
	if _, err := io.ReadFull(o.r, lacing); err != nil {
		return err
	}
	total := 0
	for _, l := range lacing {
		total += int(l)
	}
	data := make([]byte, total)
	if _, err := io.ReadFull(o.r, data); err != nil {
		return err
	}
	_ = binary.LittleEndian // header fields other than nsegs are not needed for playback

	off := 0
	for _, l := range lacing {
		o.partial = append(o.partial, data[off:off+int(l)]...)
		off += int(l)
		if l < 255 { // packet complete
			o.queue = append(o.queue, o.partial)
			o.partial = nil
		}
	}
	return nil
}

func (o *oggOpusReader) Close() {}
