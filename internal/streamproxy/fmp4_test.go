package streamproxy

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// box builds one ISO-BMFF box.
func box(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out, uint32(8+len(body)))
	copy(out[4:], typ)
	return append(out, body...)
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// descr builds an MPEG-4 descriptor with a single-byte size.
func descr(tag byte, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	return append([]byte{tag, byte(len(b))}, b...)
}

// testInit builds an init segment with one sound track (ID trackID) whose
// esds carries objectType and, for AAC, the AudioSpecificConfig asc.
func testInit(trackID uint32, objectType byte, asc []byte) []byte {
	dcd := append([]byte{objectType, 0x15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, descr(0x05, asc)...)
	esds := box("esds", u32(0), descr(0x03, []byte{0, 1, 0}, descr(0x04, dcd)))
	mp4a := box("mp4a", make([]byte, 28), esds)
	stsd := box("stsd", u32(0), u32(1), mp4a)
	hdlr := box("hdlr", u32(0), u32(0), []byte("soun"), make([]byte, 13))
	tkhd := box("tkhd", u32(0), u32(0), u32(0), u32(trackID), make([]byte, 64))
	trak := box("trak", tkhd, box("mdia", hdlr, box("minf", box("stbl", stsd))))
	trex := box("trex", u32(0), u32(trackID), u32(1), u32(1024), u32(0), u32(0))
	return append(box("ftyp", []byte("iso6"), u32(0)), box("moov", box("mvhd", make([]byte, 100)), trak, box("mvex", trex))...)
}

// testSegment builds a media segment: styp, one moof with a default-base-is-moof
// traf and a trun listing each sample's size, then an mdat with the samples.
func testSegment(trackID uint32, samples ...[]byte) []byte {
	build := func(dataOffset uint32) []byte {
		trunBody := [][]byte{{0, 0, 0x02, 0x01}, u32(uint32(len(samples))), u32(dataOffset)}
		for _, s := range samples {
			trunBody = append(trunBody, u32(uint32(len(s))))
		}
		tfhd := box("tfhd", []byte{0, 0x02, 0, 0}, u32(trackID))
		return box("moof", box("mfhd", u32(0), u32(1)), box("traf", tfhd, box("trun", trunBody...)))
	}
	moof := build(0)
	moof = build(uint32(len(moof) + 8)) // samples start right after the mdat header
	styp := box("styp", []byte("msdh"), u32(0))
	return append(append(styp, moof...), box("mdat", samples...)...)
}

// ascHEAAC is an AudioSpecificConfig for HE-AAC v1: object type 5 (SBR),
// 24 kHz core (index 6), stereo, 48 kHz extension (index 3), core AAC-LC.
var ascHEAAC = []byte{0x2B, 0x11, 0x88, 0x00}

func TestParseFMP4InitAAC(t *testing.T) {
	tr, err := parseFMP4Init(testInit(7, 0x40, []byte{0x12, 0x10})) // AAC-LC 44.1 kHz stereo
	if err != nil {
		t.Fatal(err)
	}
	if tr.trackID != 7 || tr.mp3 || tr.aacProfile != 1 || tr.freqIndex != 4 || tr.channels != 2 {
		t.Fatalf("track = %+v, want id 7 LC 44.1 kHz stereo", *tr)
	}
}

func TestParseFMP4InitHEAACUsesCore(t *testing.T) {
	tr, err := parseFMP4Init(testInit(1, 0x40, ascHEAAC))
	if err != nil {
		t.Fatal(err)
	}
	if tr.aacProfile != 1 || tr.freqIndex != 6 || tr.channels != 2 {
		t.Fatalf("track = %+v, want the LC core at 24 kHz stereo", *tr)
	}
}

func TestParseFMP4InitMP3AndRejects(t *testing.T) {
	tr, err := parseFMP4Init(testInit(1, 0x6B, nil))
	if err != nil || !tr.mp3 || tr.contentType() != "audio/mpeg" {
		t.Fatalf("MP3 track: tr=%+v err=%v", tr, err)
	}
	if _, err := parseFMP4Init(testInit(1, 0xA5, nil)); err == nil { // AC-3
		t.Errorf("AC-3 track accepted, want unsupported")
	}
	if _, err := parseFMP4Init([]byte("not an mp4 at all")); err == nil {
		t.Errorf("garbage init accepted")
	}
	enc := bytes.Replace(testInit(1, 0x40, ascHEAAC), []byte("mp4a"), []byte("enca"), 1)
	if _, err := parseFMP4Init(enc); err == nil {
		t.Errorf("encrypted (enca) track accepted, want unsupported")
	}
}

func TestFMP4AudioFramesSamplesAsADTS(t *testing.T) {
	tr, err := parseFMP4Init(testInit(1, 0x40, ascHEAAC))
	if err != nil {
		t.Fatal(err)
	}
	s1, s2 := bytes.Repeat([]byte{0x11}, 300), bytes.Repeat([]byte{0x22}, 5)
	out, ok := fmp4Audio(testSegment(1, s1, s2), tr)
	if !ok {
		t.Fatal("no audio from a valid segment")
	}
	if len(out) != 2*7+len(s1)+len(s2) {
		t.Fatalf("len = %d, want two ADTS frames", len(out))
	}
	hdr := out[:7]
	if hdr[0] != 0xFF || hdr[1] != 0xF1 {
		t.Fatalf("bad ADTS sync %x", hdr[:2])
	}
	if profile, freq := hdr[2]>>6, hdr[2]>>2&0xF; profile != 1 || freq != 6 {
		t.Errorf("profile=%d freq=%d, want 1 (LC) and 6 (24 kHz)", profile, freq)
	}
	if ch := (hdr[2]&1)<<2 | hdr[3]>>6; ch != 2 {
		t.Errorf("channels = %d, want 2", ch)
	}
	if n := int(hdr[3]&3)<<11 | int(hdr[4])<<3 | int(hdr[5]>>5); n != 307 {
		t.Errorf("frame length = %d, want 307", n)
	}
	if !bytes.Equal(out[7:307], s1) || !bytes.Equal(out[314:], s2) {
		t.Errorf("sample payloads not copied in order")
	}
}

func TestFMP4AudioRejectsOtherTrackAndTruncation(t *testing.T) {
	tr, _ := parseFMP4Init(testInit(1, 0x40, ascHEAAC))
	if _, ok := fmp4Audio(testSegment(2, []byte{1, 2, 3}), tr); ok {
		t.Errorf("samples of another track were used")
	}
	seg := testSegment(1, bytes.Repeat([]byte{0x33}, 100))
	if _, ok := fmp4Audio(seg[:len(seg)-50], tr); ok {
		t.Errorf("a sample running past the segment end was accepted")
	}
}

// FuzzFMP4 feeds arbitrary bytes into both fMP4 entry points. Box sizes,
// descriptor lengths, sample counts and data offsets are all read from the
// input, so the target asserts neither parser panics nor runs away.
func FuzzFMP4(f *testing.F) {
	init := testInit(1, 0x40, ascHEAAC)
	f.Add(init)
	f.Add(testSegment(1, []byte{1, 2, 3}, bytes.Repeat([]byte{9}, 40)))
	f.Add([]byte{0, 0, 0, 1, 'm', 'o', 'o', 'f', 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{})
	tr, err := parseFMP4Init(init)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		begin := time.Now()
		_, _ = parseFMP4Init(data)
		_, _ = fmp4Audio(data, tr)
		if d := time.Since(begin); d > 5*time.Second {
			t.Fatalf("took %s on %d bytes", d, len(data))
		}
	})
}

func TestParseMediaPlaylistFMP4MapAndKey(t *testing.T) {
	pl := parseMediaPlaylist("#EXTM3U\n"+
		"#EXT-X-MAP:URI=\"init.mp4?token=a,b\"\n"+
		"#EXTINF:4.0,\nseg0.m4s\n", "https://x/y/playlist.m3u8")
	if pl.mapURI != "https://x/y/init.mp4?token=a,b" || pl.mapRange != "" || pl.encrypted {
		t.Fatalf("pl = %+v", pl)
	}
	pl = parseMediaPlaylist("#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"skd://k\"\nseg0.m4s\n", "https://x/p.m3u8")
	if !pl.encrypted {
		t.Errorf("SAMPLE-AES playlist not flagged encrypted")
	}
	pl = parseMediaPlaylist("#EXTM3U\n#EXT-X-KEY:METHOD=NONE\nseg0.ts\n", "https://x/p.m3u8")
	if pl.encrypted {
		t.Errorf("METHOD=NONE flagged encrypted")
	}
}

// TestServeHLSFMP4 plays a two-segment fMP4 VOD playlist end to end: the init
// segment is fetched once and the box gets one continuous ADTS stream.
func TestServeHLSFMP4(t *testing.T) {
	init := testInit(1, 0x40, ascHEAAC)
	segs := map[string][]byte{
		"/s0.m4s": testSegment(1, bytes.Repeat([]byte{0x44}, 50)),
		"/s1.m4s": testSegment(1, bytes.Repeat([]byte{0x55}, 60)),
	}
	initHits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MAP:URI=\"init.mp4?k=1\"\n" +
				"#EXTINF:4,\ns0.m4s\n#EXTINF:4,\ns1.m4s\n#EXT-X-ENDLIST\n"))
		case "/init.mp4":
			initHits++
			_, _ = w.Write(init)
		default:
			if b, ok := segs[r.URL.Path]; ok {
				_, _ = w.Write(b)
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer up.Close()

	s := New(nil, silentLogger())
	s.client = &http.Client{} // the production client refuses loopback dials
	req := httptest.NewRequest(http.MethodGet, "/stream/1", nil)
	rw := httptest.NewRecorder()
	if err := s.serveHLS(req.Context(), rw, req, up.URL+"/live.m3u8"); err != nil {
		t.Fatalf("serveHLS: %v", err)
	}
	if ct := rw.Header().Get("Content-Type"); ct != "audio/aac" {
		t.Errorf("Content-Type = %q, want audio/aac", ct)
	}
	body := rw.Body.Bytes()
	if len(body) != 7+50+7+60 || body[0] != 0xFF || body[57] != 0xFF {
		t.Errorf("body = %d bytes, want two ADTS frames back to back", len(body))
	}
	if initHits != 1 {
		t.Errorf("init segment fetched %d times, want 1", initHits)
	}
}

func TestServeHLSEncryptedNotPlayable(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n#EXTINF:4,\ns0.ts\n#EXT-X-ENDLIST\n"))
	}))
	defer up.Close()
	s := New(nil, silentLogger())
	s.client = &http.Client{}
	req := httptest.NewRequest(http.MethodGet, "/stream/1", nil)
	err := s.serveHLS(req.Context(), httptest.NewRecorder(), req, up.URL+"/p.m3u8")
	if err == nil || !strings.Contains(err.Error(), "hls") {
		t.Fatalf("err = %v, want an hls not-playable error", err)
	}
}
