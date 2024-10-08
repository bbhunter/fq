package midi

// https://www.midi.org/specifications/item/the-midi-1-0-specification

import (
	"bytes"
	"embed"
	"fmt"

	"github.com/wader/fq/format"
	"github.com/wader/fq/pkg/decode"
	"github.com/wader/fq/pkg/interp"
)

type context struct {
	tick    uint64
	running uint8
	casio   bool
}

//go:embed midi.md
var midiFS embed.FS

func init() {
	interp.RegisterFormat(
		format.MIDI,
		&decode.Format{
			Description: "Standard MIDI file",
			Groups:      []*decode.Group{format.Probe},
			DecodeFn:    decodeMIDI,
		})

	interp.RegisterFS(midiFS)
}

func decodeMIDI(d *decode.D) any {
	d.Endian = decode.BigEndian

	// ... decode header
	d.FieldStruct("header", decodeMThd)

	// ... decode tracks (and other chunks)
	d.FieldArray("content", func(d *decode.D) {
		for d.BitsLeft() > 0 {
			if bytes.Equal(d.PeekBytes(4), []byte("MTrk")) {
				d.FieldStruct("track", decodeMTrk)
			} else {
				d.FieldStruct("other", decodeOther)
			}
		}
	})

	return nil
}

func decodeMThd(d *decode.D) {
	if !bytes.Equal(d.PeekBytes(4), []byte("MThd")) {
		d.Errorf("missing MThd tag")
	}

	d.FieldUTF8("tag", 4)
	length := d.FieldS32("length")

	d.FramedFn(length*8, func(d *decode.D) {
		d.FieldU16("format")
		d.FieldU16("tracks")

		d.FieldStruct("division", func(d *decode.D) {
			if division := d.PeekUintBits(16); division&0x8000 == 0x8000 {
				d.FieldU8("fps", fps)
				d.FieldU8("resolution")
			} else {
				d.FieldU16("ppqn")
			}
		})
	})
}

func decodeMTrk(d *decode.D) {
	if !bytes.Equal(d.PeekBytes(4), []byte("MTrk")) {
		d.Errorf("missing MTrk tag")
	}

	d.FieldUTF8("tag", 4)
	length := d.FieldS32("length")

	d.FieldArray("events", func(d *decode.D) {
		d.FramedFn(length*8, func(d *decode.D) {
			ctx := context{
				tick:    0,
				running: 0x000,
				casio:   false,
			}

			for d.BitsLeft() > 0 {
				decodeEvent(d, &ctx)
			}
		})
	})
}

func decodeEvent(d *decode.D, ctx *context) {
	_, status, event := peekEvent(d)

	if status == 0xf0 || status == 0xf7 {
		decodeSysExEvent(d, status, ctx)
	} else if status == 0xff {
		decodeMetaEvent(d, event, ctx)
	} else if status < 0xf0 {
		decodeMIDIEvent(d, status, ctx)
	} else {
		d.Errorf("invalid status byte (%02x)", status)
	}
}

func peekEvent(d *decode.D) (uint64, uint8, uint8) {
	var N int = 1

	for {
		bytes := d.PeekBytes(N)
		delta := uint64(0)
		ix := 0

		for ix < N {
			b := bytes[ix]
			ix++

			delta <<= 7
			delta += uint64(b & 0x7f)

			if b&0x80 == 0 {
				if ix < N {
					status := bytes[ix]
					ix++

					// ... MIDI event?
					if status < 0xf0 {
						return delta, status, 0x00
					}

					// ... sysex?
					if status == 0xf0 || status == 0xf7 {
						return delta, status, 0x00
					}

					// ... (invalid) real-time event
					if status != 0xff {
						return delta, status, 0x00
					}

					// ... meta-event
					if ix < N {
						event := bytes[ix]
						return delta, status, event
					}
				}
			}
		}

		N++
	}
}

func decodeOther(d *decode.D) {
	d.FieldUTF8("tag", 4)
	length := d.FieldS32("length")
	d.FieldRawLen("data", length*8)
}

// Big endian varint
func vlq(d *decode.D) uint64 {
	vlq := uint64(0)

	for {
		b := d.U8()

		vlq <<= 7
		vlq += b & 0x7f

		if b&0x80 == 0 {
			break
		}
	}

	return vlq
}

// Byte array with a big endian varint length
func vlf(d *decode.D) ([]uint8, error) {
	N := vlq(d)

	if N*8 > uint64(d.BitsLeft()) {
		d.Fatalf("invalid field length")
	}

	if int64(N*8) > d.BitsLeft() {
		return nil, fmt.Errorf("invalid field length")
	} else {
		return d.BytesLen(int(N)), nil
	}
}

// String with a big endian varint length
func vlstring(d *decode.D) string {
	if data, err := vlf(d); err != nil {
		d.Fatalf("%v", err)
	} else {
		return string(data)
	}

	return ""
}

func flush(d *decode.D, format string, args ...any) {
	d.Errorf(format, args...)

	var N int = int(d.BitsLeft())

	d.Bits(N)
}
