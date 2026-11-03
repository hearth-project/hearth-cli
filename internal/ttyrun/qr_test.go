package ttyrun

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"
)

// The fixtures draw the same code the ways terminal programs do (testdata,
// made with the qrcode npm package): coloured spaces (mdp/qrterminal, which
// DUCX uses), half blocks with light or dark glyphs, reverse video.
func TestFindQR(t *testing.T) {
	want, err := os.ReadFile("testdata/matrix.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(string(want)), "\n")
	for _, f := range []string{"ansi-bg", "half-light", "half-dark", "reverse"} {
		b, err := os.ReadFile("testdata/" + f + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		s := NewScreen(200, 60)
		// in small pieces, to cut escape sequences and runes apart
		for i := 0; i < len(b); i += 7 {
			s.Write(b[i:min(i+7, len(b))])
		}
		q := s.FindQR()
		if q == nil {
			t.Fatalf("%s: no QR found", f)
		}
		if len(q.Modules) != len(rows) {
			t.Fatalf("%s: %d modules, want %d", f, len(q.Modules), len(rows))
		}
		for r, line := range rows {
			for c, ch := range line {
				if q.Modules[r][c] != (ch == '#') {
					t.Fatalf("%s: module %d,%d differs", f, r, c)
				}
			}
		}
		img, err := png.Decode(bytes.NewReader(q.PNG(8)))
		if err != nil || img.Bounds().Dx() != (len(rows)+8)*8 {
			t.Fatalf("%s: png %v", f, err)
		}
		if q2 := QRFromPNG(q.PNG(8), 8); q2 == nil || q2.HalfBlocks() != q.HalfBlocks() {
			t.Fatalf("%s: png does not read back", f)
		}
		// The terminal drawing reads back as the same code.
		back := NewScreen(200, 60)
		back.Write([]byte(strings.ReplaceAll(q.HalfBlocks(), "\n", "\r\n")))
		if q3 := back.FindQR(); q3 == nil || q3.Hash != q.Hash {
			t.Fatalf("%s: half-block drawing does not read back", f)
		}
		if u := URLs(s.Text()); len(u) != 1 || !strings.HasPrefix(u[0], "https://login.example.com/qrcode/scan?processId=") {
			t.Fatalf("%s: urls %v", f, u)
		}
	}
}

func TestScreenText(t *testing.T) {
	s := NewScreen(40, 5)
	s.Write([]byte("hello\r\nwor\x1b[1mld\x1b[0m\r\nprogress 10%\rprogress 99%\r\n\x1b[31mred\x1b[0m line\r\n"))
	got := strings.Join(s.Text(), "|")
	if got != "hello|world|progress 99%|red line" {
		t.Fatalf("%q", got)
	}
	for i := 0; i < 10; i++ {
		s.Write([]byte("x\r\n"))
	}
	if len(s.History) == 0 || s.FindQR() != nil {
		t.Fatal("scrolling")
	}
}

func TestRedactor(t *testing.T) {
	var b strings.Builder
	r := newRedactor(&b, []string{"abcd1234"})
	for _, p := range []string{"key=ab", "cd12", "34 done; url https://x/y", " and ab"} {
		r.Write([]byte(p))
	}
	if b.String() != "key=******** done; url https://x/y and " {
		t.Fatalf("%q", b.String())
	}
	r.Flush()
	if !strings.HasSuffix(b.String(), " and ab") {
		t.Fatalf("flush %q", b.String())
	}
}
