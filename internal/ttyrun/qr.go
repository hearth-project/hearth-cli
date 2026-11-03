package ttyrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
)

// QR is a QR code found in the terminal output.
type QR struct {
	Modules [][]bool // true = dark, without the quiet zone
	Rows    [2]int   // first and last output line it covers
	Hash    string   // of the modules: a new code (expired, refreshed) has another
}

// Programs draw QR codes in a terminal in a few ways: half blocks (▀ ▄ █,
// each character two modules high), full blocks or two spaces per module,
// or spaces with a background colour (ANSI 40/47, reverse video). Every
// character cell is read as two pixels stacked, which makes the modules
// square in all of them; then the light quiet zone is cut off, the polarity
// and module size are found from the finder pattern in the corners.
func isBlock(r rune) bool { return r == '█' || r == '▀' || r == '▄' }

// qrRow: a line that is part of a drawn QR code.
func qrRow(row []cell) bool {
	n := 0
	for _, c := range row {
		if isBlock(c.r) || c.bg {
			n++
		}
	}
	return n >= 10
}

// FindQR returns the last QR code in the output, or nil.
func (s *Screen) FindQR() *QR {
	lines := s.lines()
	end := len(lines) - 1
	for end >= 0 {
		for end >= 0 && !qrRow(lines[end]) {
			end--
		}
		start := end
		for start > 0 && qrRow(lines[start-1]) {
			start--
		}
		if end >= 0 && end-start+1 >= 8 {
			if q := decodeRegion(lines[start : end+1]); q != nil {
				q.Rows = [2]int{start, end}
				return q
			}
		}
		end = start - 1
	}
	return nil
}

func decodeRegion(rows [][]cell) *QR {
	lo, hi := -1, -1
	for _, row := range rows {
		for x, c := range row {
			if isBlock(c.r) || c.bg {
				if lo < 0 || x < lo {
					lo = x
				}
				if x > hi {
					hi = x
				}
			}
		}
	}
	if lo < 0 {
		return nil
	}
	// Spaces on the terminal's own background: the other colour of a code
	// drawn with coloured cells (light cells only: the plain ones are dark).
	lightBg, glyphs := 0, 0
	for _, row := range rows {
		for x := lo; x <= hi && x < len(row); x++ {
			if isBlock(row[x].r) {
				glyphs++
			} else if row[x].bg && row[x].light {
				lightBg++
			}
		}
	}
	plainIsDark := glyphs == 0 && lightBg > 0
	w, h := hi-lo+1, 2*len(rows)
	// ink: the glyph, or a dark background (true); light background, or a
	// space on the default background (false).
	ink := make([][]bool, h)
	for y := range ink {
		ink[y] = make([]bool, w)
	}
	for ry, row := range rows {
		for x := lo; x <= hi && x < len(row); x++ {
			c := row[x]
			var top, bot bool
			switch {
			case c.r == '█':
				top, bot = true, true
			case c.r == '▀':
				top = true
			case c.r == '▄':
				bot = true
			case c.bg:
				top, bot = !c.light, !c.light
			default:
				top, bot = plainIsDark, plainIsDark
			}
			ink[2*ry][x-lo], ink[2*ry+1][x-lo] = top, bot
		}
	}
	for _, darkIsInk := range []bool{true, false} {
		if q := modules(ink, darkIsInk); q != nil {
			return q
		}
	}
	return nil
}

// modules reads the module grid from pixels, with ink as dark or as light.
func modules(ink [][]bool, darkIsInk bool) *QR {
	dark := func(y, x int) bool { return ink[y][x] == darkIsInk }
	h, w := len(ink), len(ink[0])
	top, bot, left, right := 0, h-1, 0, w-1
	rowLight := func(y int) bool {
		for x := left; x <= right; x++ {
			if dark(y, x) {
				return false
			}
		}
		return true
	}
	colLight := func(x int) bool {
		for y := top; y <= bot; y++ {
			if dark(y, x) {
				return false
			}
		}
		return true
	}
	for top <= bot && rowLight(top) {
		top++
	}
	for bot >= top && rowLight(bot) {
		bot--
	}
	for left <= right && colLight(left) {
		left++
	}
	for right >= left && colLight(right) {
		right--
	}
	if top > bot || left > right {
		return nil
	}
	// The top-left finder pattern: a dark run of 7 modules.
	run := 0
	for x := left; x <= right && dark(top, x); x++ {
		run++
	}
	if run < 7 || run%7 != 0 {
		return nil
	}
	m := run / 7
	pw, ph := right-left+1, bot-top+1
	if pw%m != 0 || ph%m != 0 || pw/m != ph/m {
		return nil
	}
	n := pw / m
	if n < 21 || (n-21)%4 != 0 {
		return nil
	}
	at := func(r, c int) bool { return dark(top+r*m+m/2, left+c*m+m/2) }
	// Finder rings in three corners: dark, light, dark core.
	finder := func(r0, c0 int) bool {
		for i := 0; i < 7; i++ {
			if !at(r0, c0+i) || !at(r0+6, c0+i) || !at(r0+i, c0) || !at(r0+i, c0+6) {
				return false
			}
		}
		for i := 1; i < 6; i++ {
			if at(r0+1, c0+i) || at(r0+5, c0+i) {
				return false
			}
		}
		return at(r0+3, c0+3) && at(r0+2, c0+2) && at(r0+4, c0+4)
	}
	if !finder(0, 0) || !finder(0, n-7) || !finder(n-7, 0) {
		return nil
	}
	q := &QR{Modules: make([][]bool, n)}
	hsh := sha256.New()
	for r := 0; r < n; r++ {
		q.Modules[r] = make([]bool, n)
		for c := 0; c < n; c++ {
			q.Modules[r][c] = at(r, c)
			if q.Modules[r][c] {
				hsh.Write([]byte{1})
			} else {
				hsh.Write([]byte{0})
			}
		}
	}
	q.Hash = hex.EncodeToString(hsh.Sum(nil))[:16]
	return q
}

// PNG draws the code black on white, scale pixels per module, with the
// standard quiet zone of 4 modules.
func (q *QR) PNG(scale int) []byte {
	n := len(q.Modules)
	size := (n + 8) * scale
	img := image.NewGray(image.Rect(0, 0, size, size))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if !q.Modules[r][c] {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray((c+4)*scale+dx, (r+4)*scale+dy, color.Gray{})
				}
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

var urlRE = regexp.MustCompile(`https?://[^\s"'<>]+`)

// URLs are the links printed in the output (outside the QR code), newest last.
func URLs(lines []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lines {
		for _, u := range urlRE.FindAllString(l, -1) {
			u = strings.TrimRight(u, ".,;:)]")
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

// HalfBlocks draws the code two module rows per line (▀ ▄ █), light
// modules as glyphs: square and tight in a terminal, where lines touch.
func (q *QR) HalfBlocks() string {
	n := len(q.Modules)
	light := func(r, c int) bool { return r < 0 || c < 0 || r >= n || c >= n || !q.Modules[r][c] }
	var b strings.Builder
	for r := -2; r < n+2; r += 2 {
		for c := -2; c < n+2; c++ {
			t, u := light(r, c), light(r+1, c)
			switch {
			case t && u:
				b.WriteRune('█')
			case t:
				b.WriteRune('▀')
			case u:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// QRFromPNG reads back what PNG drew (scale px per module, a quiet zone
// of 4 modules): a terminal on another machine prints it with half blocks.
func QRFromPNG(b []byte, scale int) *QR {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	w := img.Bounds().Dx()
	if w%scale != 0 || w != img.Bounds().Dy() {
		return nil
	}
	n := w/scale - 8
	if n < 21 {
		return nil
	}
	q := &QR{Modules: make([][]bool, n)}
	for r := 0; r < n; r++ {
		q.Modules[r] = make([]bool, n)
		for c := 0; c < n; c++ {
			y, _, _, _ := img.At(img.Bounds().Min.X+(c+4)*scale+scale/2, img.Bounds().Min.Y+(r+4)*scale+scale/2).RGBA()
			q.Modules[r][c] = y < 0x8000
		}
	}
	return q
}
