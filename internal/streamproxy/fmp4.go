package streamproxy

// Minimal fMP4/CMAF audio demuxer for HLS (stage 3). Apple's live radio
// (Apple Music Club and the other itsliveradio.apple.com stations) ships HLS
// with an EXT-X-MAP init segment and fragmented-MP4 media segments carrying
// raw AAC access units. The Bose player wants ADTS, so this reads the
// AudioSpecificConfig from the init segment once, then cuts every sample out
// of each fragment's mdat and prefixes it with a 7-byte ADTS header. MP3 in
// MP4 passes through as bare frames.
//
// Like the MPEG-TS demuxer it is deliberately small and tolerant: encrypted
// tracks (enca / SAMPLE-AES), non-AAC/MP3 codecs, and anything it cannot parse
// yield ok=false so the caller falls back to not-playable instead of feeding
// the box garbage.

import (
	"encoding/binary"
	"errors"
)

// fmp4Track is what the init segment tells us about the first audio track.
type fmp4Track struct {
	trackID     uint32
	mp3         bool // MPEG-1/2 audio: samples are complete MP3 frames
	aacProfile  byte // ADTS profile (audio object type - 1)
	freqIndex   byte // ADTS sampling frequency index
	channels    byte // ADTS channel configuration
	defaultSize uint32
}

// contentType is the Content-Type the box gets for this track's output.
func (t *fmp4Track) contentType() string {
	if t.mp3 {
		return "audio/mpeg"
	}
	return "audio/aac"
}

var errFMP4Unsupported = errors.New("fmp4: no playable audio track")

// mp4Box is one ISO-BMFF box: its type, the offset of its start relative to
// the base passed to mp4Boxes, and its payload (the bytes after the header,
// bounded by the box size).
type mp4Box struct {
	typ     string
	start   int
	payload []byte
}

// mp4Boxes splits data into its top-level boxes; base is the offset of data[0].
// Only the top-level moof offsets matter (trun data offsets count from the
// moof start). Parsing stops at the first malformed header.
func mp4Boxes(data []byte, base int) []mp4Box {
	var out []mp4Box
	off := 0
	for off+8 <= len(data) {
		size := uint64(binary.BigEndian.Uint32(data[off:]))
		typ := string(data[off+4 : off+8])
		hdr := 8
		switch size {
		case 1:
			if off+16 > len(data) {
				return out
			}
			size = binary.BigEndian.Uint64(data[off+8:])
			hdr = 16
		case 0:
			size = uint64(len(data) - off)
		}
		if size < uint64(hdr) || size > uint64(len(data)-off) {
			return out
		}
		end := off + int(size)
		out = append(out, mp4Box{typ: typ, start: base + off, payload: data[off+hdr : end]})
		off = end
	}
	return out
}

func mp4Child(b []mp4Box, typ string) (mp4Box, bool) {
	for _, c := range b {
		if c.typ == typ {
			return c, true
		}
	}
	return mp4Box{}, false
}

// mp4Path walks down nested container boxes by type.
func mp4Path(box mp4Box, path ...string) (mp4Box, bool) {
	cur := box
	for _, typ := range path {
		next, ok := mp4Child(mp4Boxes(cur.payload, 0), typ)
		if !ok {
			return mp4Box{}, false
		}
		cur = next
	}
	return cur, true
}

// adtsFreqs is the ADTS sampling-frequency table, indexed by freqIndex.
var adtsFreqs = []uint32{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

// parseFMP4Init reads the first sound track's codec setup out of an init
// segment (ftyp + moov).
func parseFMP4Init(init []byte) (*fmp4Track, error) {
	moov, ok := mp4Child(mp4Boxes(init, 0), "moov")
	if !ok {
		return nil, errFMP4Unsupported
	}
	defaults := map[uint32]uint32{}
	if mvex, ok := mp4Child(mp4Boxes(moov.payload, 0), "mvex"); ok {
		for _, trex := range mp4Boxes(mvex.payload, 0) {
			// trex: version/flags, track_ID, desc index, duration, size, flags.
			if trex.typ == "trex" && len(trex.payload) >= 20 {
				defaults[binary.BigEndian.Uint32(trex.payload[4:])] = binary.BigEndian.Uint32(trex.payload[16:])
			}
		}
	}
	for _, trak := range mp4Boxes(moov.payload, 0) {
		if trak.typ != "trak" {
			continue
		}
		hdlr, ok := mp4Path(trak, "mdia", "hdlr")
		if !ok || len(hdlr.payload) < 12 || string(hdlr.payload[8:12]) != "soun" {
			continue
		}
		tkhd, ok := mp4Path(trak, "tkhd")
		if !ok || len(tkhd.payload) < 4 {
			continue
		}
		idOff := 12 // version 0: flags, creation, modification
		if tkhd.payload[0] == 1 {
			idOff = 20
		}
		if len(tkhd.payload) < idOff+4 {
			continue
		}
		stsd, ok := mp4Path(trak, "mdia", "minf", "stbl", "stsd")
		if !ok || len(stsd.payload) < 8 {
			continue
		}
		// stsd: version/flags, entry count, then sample entries. An audio
		// sample entry has a 28-byte fixed part before its child boxes.
		entries := mp4Boxes(stsd.payload[8:], 0)
		if len(entries) == 0 || entries[0].typ != "mp4a" || len(entries[0].payload) < 28 {
			continue // enca (encrypted), ac-3, Opus, ...: not for the box
		}
		esds, ok := mp4Child(mp4Boxes(entries[0].payload[28:], 0), "esds")
		if !ok || len(esds.payload) < 4 {
			continue
		}
		tr, err := parseESDS(esds.payload[4:])
		if err != nil {
			continue
		}
		tr.trackID = binary.BigEndian.Uint32(tkhd.payload[idOff:])
		tr.defaultSize = defaults[tr.trackID]
		return tr, nil
	}
	return nil, errFMP4Unsupported
}

// parseESDS reads an ES_Descriptor: the DecoderConfigDescriptor's object type
// picks AAC or MP3, and for AAC the DecoderSpecificInfo is the
// AudioSpecificConfig the ADTS header is built from.
func parseESDS(d []byte) (*fmp4Track, error) {
	tag, body, _, ok := mp4Descriptor(d)
	if !ok || tag != 0x03 || len(body) < 3 {
		return nil, errFMP4Unsupported
	}
	flags := body[2]
	body = body[3:]
	if flags&0x80 != 0 { // streamDependenceFlag
		body = skipBytes(body, 2)
	}
	if flags&0x40 != 0 { // URL_Flag
		if len(body) < 1 {
			return nil, errFMP4Unsupported
		}
		body = skipBytes(body, 1+int(body[0]))
	}
	if flags&0x20 != 0 { // OCRstreamFlag
		body = skipBytes(body, 2)
	}
	tag, dcd, _, ok := mp4Descriptor(body)
	if !ok || tag != 0x04 || len(dcd) < 13 {
		return nil, errFMP4Unsupported
	}
	switch dcd[0] {
	case 0x69, 0x6B: // MPEG-2 / MPEG-1 audio (MP3)
		return &fmp4Track{mp3: true}, nil
	case 0x40, 0x66, 0x67, 0x68: // MPEG-4 AAC, MPEG-2 AAC profiles
	default:
		return nil, errFMP4Unsupported
	}
	tag, asc, _, ok := mp4Descriptor(dcd[13:])
	if !ok || tag != 0x05 {
		return nil, errFMP4Unsupported
	}
	return parseASC(asc)
}

// parseASC turns an AudioSpecificConfig into ADTS header fields. HE-AAC
// (SBR/PS, object types 5 and 29) is signalled as its AAC-LC core at the core
// sample rate; decoders detect the SBR layer implicitly, exactly as with the
// ADTS streams the MPEG-TS path forwards.
func parseASC(asc []byte) (*fmp4Track, error) {
	br := bitReader{b: asc}
	readAOT := func() uint32 {
		aot := br.read(5)
		if aot == 31 {
			aot = 32 + br.read(6)
		}
		return aot
	}
	readFreq := func() (byte, bool) {
		idx := br.read(4)
		if idx != 15 {
			return byte(idx), idx < uint32(len(adtsFreqs))
		}
		hz := br.read(24)
		for i, f := range adtsFreqs {
			if f == hz {
				return byte(i), true
			}
		}
		return 0, false
	}
	aot := readAOT()
	freq, okFreq := readFreq()
	ch := br.read(4)
	if aot == 5 || aot == 29 {
		// The extension sampling rate follows; the core object type after it.
		if _, ok := readFreq(); !ok {
			return nil, errFMP4Unsupported
		}
		aot = readAOT()
	}
	// ADTS has a 2-bit profile: object types 1..4 (Main, LC, SSR, LTP) only.
	// Channel configuration 0 means a program config element, which ADTS
	// cannot carry in its header.
	if br.err || !okFreq || aot < 1 || aot > 4 || ch == 0 || ch > 7 {
		return nil, errFMP4Unsupported
	}
	return &fmp4Track{aacProfile: byte(aot - 1), freqIndex: freq, channels: byte(ch)}, nil
}

// mp4Descriptor reads one MPEG-4 descriptor (tag + variable-length size) and
// returns its tag, its body, and the bytes after it.
func mp4Descriptor(d []byte) (tag byte, body, rest []byte, ok bool) {
	if len(d) < 2 {
		return 0, nil, nil, false
	}
	tag = d[0]
	size := 0
	i := 1
	for n := 0; n < 4; n++ {
		if i >= len(d) {
			return 0, nil, nil, false
		}
		c := d[i]
		i++
		size = size<<7 | int(c&0x7F)
		if c&0x80 == 0 {
			break
		}
	}
	if size > len(d)-i {
		return 0, nil, nil, false
	}
	return tag, d[i : i+size], d[i+size:], true
}

func skipBytes(b []byte, n int) []byte {
	if n > len(b) {
		return nil
	}
	return b[n:]
}

// fmp4Audio demuxes one fMP4 media segment for tr: every moof/traf of the
// track yields its samples, each AAC sample framed as ADTS (MP3 frames as-is).
// Returns ok=false when the segment carries no usable sample.
func fmp4Audio(seg []byte, tr *fmp4Track) ([]byte, bool) {
	var out []byte
	for _, moof := range mp4Boxes(seg, 0) {
		if moof.typ != "moof" {
			continue
		}
		for _, traf := range mp4Boxes(moof.payload, 0) {
			if traf.typ == "traf" {
				out = appendTrafSamples(out, seg, moof.start, traf, tr)
			}
		}
	}
	return out, len(out) > 0
}

// appendTrafSamples appends the framed samples of one track fragment.
func appendTrafSamples(out, seg []byte, moofStart int, traf mp4Box, tr *fmp4Track) []byte {
	children := mp4Boxes(traf.payload, 0)
	tfhd, ok := mp4Child(children, "tfhd")
	if !ok || len(tfhd.payload) < 8 {
		return out
	}
	p := tfhd.payload
	flags := uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
	if binary.BigEndian.Uint32(p[4:]) != tr.trackID {
		return out
	}
	p = p[8:]
	base := int64(moofStart)
	defaultSize := tr.defaultSize
	if flags&0x01 != 0 { // base-data-offset-present
		if len(p) < 8 {
			return out
		}
		base = int64(binary.BigEndian.Uint64(p))
		p = p[8:]
	}
	for _, f := range []uint32{0x02, 0x08} { // sample-description-index, default-duration
		if flags&f != 0 {
			p = skipBytes(p, 4)
		}
	}
	if flags&0x10 != 0 { // default-sample-size
		if len(p) < 4 {
			return out
		}
		defaultSize = binary.BigEndian.Uint32(p)
	}

	next := base // a trun without data offset continues after the previous one
	for _, trun := range children {
		if trun.typ != "trun" || len(trun.payload) < 8 {
			continue
		}
		q := trun.payload
		tflags := uint32(q[1])<<16 | uint32(q[2])<<8 | uint32(q[3])
		count := binary.BigEndian.Uint32(q[4:])
		q = q[8:]
		pos := next
		if tflags&0x01 != 0 { // data-offset-present
			if len(q) < 4 {
				return out
			}
			pos = base + int64(int32(binary.BigEndian.Uint32(q)))
			q = q[4:]
		}
		if tflags&0x04 != 0 { // first-sample-flags-present
			q = skipBytes(q, 4)
		}
		perSample := 0
		for _, f := range []uint32{0x100, 0x200, 0x400, 0x800} {
			if tflags&f != 0 {
				perSample += 4
			}
		}
		if perSample > 0 && uint64(count)*uint64(perSample) > uint64(len(q)) {
			return out
		}
		for i := uint32(0); i < count; i++ {
			size := defaultSize
			e := q
			if tflags&0x100 != 0 {
				e = e[4:]
			}
			if tflags&0x200 != 0 {
				size = binary.BigEndian.Uint32(e)
			}
			q = q[perSample:]
			if size == 0 || pos < 0 || pos+int64(size) > int64(len(seg)) {
				return out
			}
			out = appendSample(out, seg[pos:pos+int64(size)], tr)
			pos += int64(size)
		}
		next = pos
	}
	return out
}

// appendSample appends one access unit, framed as ADTS for AAC.
func appendSample(out, sample []byte, tr *fmp4Track) []byte {
	if tr.mp3 {
		return append(out, sample...)
	}
	n := len(sample) + 7
	if n > 0x1FFF { // ADTS frame length is 13 bits
		return out
	}
	out = append(out,
		0xFF,
		0xF1, // MPEG-4, layer 0, no CRC
		tr.aacProfile<<6|tr.freqIndex<<2|tr.channels>>2,
		(tr.channels&3)<<6|byte(n>>11),
		byte(n>>3),
		byte(n&7)<<5|0x1F,
		0xFC,
	)
	return append(out, sample...)
}

// bitReader reads big-endian bit fields; reading past the end sets err.
type bitReader struct {
	b   []byte
	pos int
	err bool
}

func (r *bitReader) read(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		byteIdx := r.pos >> 3
		if byteIdx >= len(r.b) {
			r.err = true
			return 0
		}
		v = v<<1 | uint32(r.b[byteIdx]>>(7-uint(r.pos&7))&1)
		r.pos++
	}
	return v
}
