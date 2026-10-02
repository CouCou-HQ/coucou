package voice

import (
	"bytes"
	"io"
	"testing"

	"github.com/disgoorg/disgo/voice"
)

type eofProvider struct{}

func (eofProvider) ProvideOpusFrame() ([]byte, error) { return nil, io.EOF }
func (eofProvider) Close()                            {}

func TestNotifyingProviderPlaysSilenceAfterEOF(t *testing.T) {
	done := make(chan struct{})
	p := &notifyingProvider{inner: eofProvider{}, done: done}
	for range 10 {
		b, err := p.ProvideOpusFrame()
		if err != nil || !bytes.Equal(b, voice.SilenceAudioFrame) {
			t.Fatalf("got %v, %v; want silence and no error", b, err)
		}
	}
	select {
	case <-done:
	default:
		t.Fatal("done not closed at EOF")
	}
}
