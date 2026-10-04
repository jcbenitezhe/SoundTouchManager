package tunein

import "testing"

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"https://itsliveradio.apple.com/x/index-ts.m3u8?accessKey=ABC123":                    "https://itsliveradio.apple.com/x/index-ts.m3u8?accessKey=[REDACTED]",
		"https://a.example/x.m3u8?foo=1&accesskey=ABC&bar=2":                                 "https://a.example/x.m3u8?foo=1&accesskey=[REDACTED]&bar=2",
		"https://cdn.example/s.mp3?Policy=P&Signature=S&Key-Pair-Id=K":                       "https://cdn.example/s.mp3?Policy=[REDACTED]&Signature=[REDACTED]&Key-Pair-Id=[REDACTED]",
		"https://cdn.example/s?hdnts=exp=1~hmac=2":                                           "https://cdn.example/s?hdnts=[REDACTED]",
		`play request url="https://a.example/p.m3u8?accessKey=XYZ" title="Apple Music Club"`: `play request url="https://a.example/p.m3u8?accessKey=[REDACTED]" title="Apple Music Club"`,
		"https://plain.example/stream.mp3":                                                   "https://plain.example/stream.mp3",
		"https://a.example/?author=jane":                                                     "https://a.example/?author=jane",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestHasToken(t *testing.T) {
	if !HasToken("https://x/y.m3u8?accessKey=1") || HasToken("https://x/y.mp3") || HasToken("tunein:s345726") {
		t.Error("HasToken misclassified")
	}
}

// asciiLower keeps byte offsets intact, unlike strings.ToLower on invalid UTF-8.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func FuzzRedactNeverKeepsAccessKey(f *testing.F) {
	f.Add("https://x/y?accessKey=SECRET")
	f.Add("a?b=1&ACCESSKEY=SECRET&c")
	f.Add("\xff&ACCessKeY=")
	f.Fuzz(func(t *testing.T, s string) {
		out := Redact(s)
		low := asciiLower(out)
		for _, sep := range []string{"?accesskey=", "&accesskey=", ";accesskey="} {
			off := 0
			for {
				i := indexFrom(low, sep, off)
				if i < 0 {
					break
				}
				if rest := out[i+len(sep):]; len(rest) < len("[REDACTED]") || rest[:len("[REDACTED]")] != "[REDACTED]" {
					t.Fatalf("value survived: %q -> %q", s, out)
				}
				off = i + 1
			}
		}
	})
}

func indexFrom(s, sub string, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
