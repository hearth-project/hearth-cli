// Package ttyrun runs an interactive restore step (a login that shows a QR
// code in the terminal) under a pseudo-terminal in the background, and reads
// its output back: the text, and the QR code as a PNG. Agents drive it from
// another machine over `hearth ssh`, so the user scans the code in the chat.
package ttyrun

import (
	"strings"
	"unicode/utf8"
)

// cell is one character position of the emulated terminal.
type cell struct {
	r rune
	// bg: the cell's background is set (ANSI background colour or reverse
	// video); light tells which kind, for QR codes drawn with coloured spaces.
	bg, light bool
}

// Screen is a small terminal emulator: enough of VT100 for programs that
// print text, move the cursor and draw QR codes (colour, reverse video,
// clearing). Lines that scroll off the top stay in History.
type Screen struct {
	Cols, Rows int
	History    [][]cell
	grid       [][]cell
	x, y       int
	bg, light  bool // current SGR background
	reverse    bool
	pending    []byte // an incomplete escape sequence or UTF-8 rune
}

// NewScreen makes an empty screen of the given size.
func NewScreen(cols, rows int) *Screen {
	s := &Screen{Cols: cols, Rows: rows}
	s.grid = make([][]cell, rows)
	for i := range s.grid {
		s.grid[i] = s.blank()
	}
	return s
}

func (s *Screen) blank() []cell {
	row := make([]cell, s.Cols)
	for i := range row {
		row[i].r = ' '
	}
	return row
}

// Write feeds terminal output to the screen.
func (s *Screen) Write(p []byte) (int, error) {
	data := append(s.pending, p...)
	s.pending = nil
	i := 0
	for i < len(data) {
		b := data[i]
		switch {
		case b == 0x1b:
			n, ok := s.escape(data[i:])
			if !ok {
				s.pending = append([]byte(nil), data[i:]...)
				return len(p), nil
			}
			i += n
			continue
		case b == '\r':
			s.x = 0
		case b == '\n':
			s.lineFeed()
		case b == '\b':
			if s.x > 0 {
				s.x--
			}
		case b == '\t':
			s.x = min((s.x/8+1)*8, s.Cols-1)
		case b < 0x20 || b == 0x7f:
			// other controls: ignored
		default:
			if !utf8.FullRune(data[i:]) {
				s.pending = append([]byte(nil), data[i:]...)
				return len(p), nil
			}
			r, n := utf8.DecodeRune(data[i:])
			s.put(r)
			i += n
			continue
		}
		i++
	}
	return len(p), nil
}

func (s *Screen) put(r rune) {
	if s.x >= s.Cols {
		s.x = 0
		s.lineFeed()
	}
	c := cell{r: r, bg: s.bg || s.reverse, light: s.light}
	if s.reverse && !s.bg {
		c.light = true // reverse video on a dark terminal: a light background
	}
	s.grid[s.y][s.x] = c
	s.x++
}

func (s *Screen) lineFeed() {
	if s.y < s.Rows-1 {
		s.y++
		return
	}
	s.History = append(s.History, s.grid[0])
	copy(s.grid, s.grid[1:])
	s.grid[s.Rows-1] = s.blank()
}

// escape handles one escape sequence at the start of b and returns its
// length; ok is false when b ends before the sequence does.
func (s *Screen) escape(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	switch b[1] {
	case '[':
		j := 2
		for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
			j++
		}
		if j >= len(b) {
			return 0, false
		}
		s.csi(string(b[2:j]), b[j])
		return j + 1, true
	case ']': // OSC: up to BEL or ST
		for j := 2; j < len(b); j++ {
			if b[j] == 0x07 {
				return j + 1, true
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				return j + 2, true
			}
		}
		return 0, false
	case '(', ')', '*', '+':
		if len(b) < 3 {
			return 0, false
		}
		return 3, true
	case 'c':
		*s = *NewScreen(s.Cols, s.Rows)
		return 2, true
	default:
		return 2, true
	}
}

func atoi(p string, def int) int {
	n, ok := 0, false
	for _, c := range p {
		if c < '0' || c > '9' {
			break
		}
		n, ok = n*10+int(c-'0'), true
	}
	if !ok {
		return def
	}
	return n
}

func (s *Screen) csi(params string, final byte) {
	private := strings.HasPrefix(params, "?")
	ps := strings.Split(strings.TrimLeft(params, "?>="), ";")
	arg := func(i, def int) int {
		if i < len(ps) {
			return atoi(ps[i], def)
		}
		return def
	}
	clampY := func() { s.y = max(0, min(s.y, s.Rows-1)) }
	clampX := func() { s.x = max(0, min(s.x, s.Cols-1)) }
	switch final {
	case 'm':
		if private {
			return
		}
		s.sgr(ps)
	case 'A':
		s.y -= arg(0, 1)
		clampY()
	case 'B':
		s.y += arg(0, 1)
		clampY()
	case 'C':
		s.x += arg(0, 1)
		clampX()
	case 'D':
		s.x -= arg(0, 1)
		clampX()
	case 'G':
		s.x = arg(0, 1) - 1
		clampX()
	case 'H', 'f':
		s.y, s.x = arg(0, 1)-1, arg(1, 1)-1
		clampY()
		clampX()
	case 'J':
		switch arg(0, 0) {
		case 0:
			s.clearLine(s.y, s.x, s.Cols)
			for y := s.y + 1; y < s.Rows; y++ {
				s.grid[y] = s.blank()
			}
		case 1:
			for y := 0; y < s.y; y++ {
				s.grid[y] = s.blank()
			}
			s.clearLine(s.y, 0, s.x+1)
		default: // 2, 3: the whole screen; keep what was on it in the history
			s.History = append(s.History, s.grid...)
			s.grid = make([][]cell, s.Rows)
			for y := range s.grid {
				s.grid[y] = s.blank()
			}
		}
	case 'K':
		switch arg(0, 0) {
		case 0:
			s.clearLine(s.y, s.x, s.Cols)
		case 1:
			s.clearLine(s.y, 0, s.x+1)
		default:
			s.clearLine(s.y, 0, s.Cols)
		}
	}
}

func (s *Screen) clearLine(y, from, to int) {
	for x := max(0, from); x < min(to, s.Cols); x++ {
		s.grid[y][x] = cell{r: ' '}
	}
}

func (s *Screen) sgr(ps []string) {
	for i := 0; i < len(ps); i++ {
		n := atoi(ps[i], 0)
		switch {
		case n == 0:
			s.bg, s.light, s.reverse = false, false, false
		case n == 7:
			s.reverse = true
		case n == 27:
			s.reverse = false
		case n >= 40 && n <= 47:
			s.bg, s.light = true, n == 47 || n == 43 || n == 46
		case n >= 100 && n <= 107:
			s.bg, s.light = true, n >= 103
		case n == 49:
			s.bg, s.light = false, false
		case n == 48 && i+1 < len(ps): // 48;5;N or 48;2;R;G;B
			if atoi(ps[i+1], 0) == 5 && i+2 < len(ps) {
				c := atoi(ps[i+2], 0)
				s.bg, s.light = true, c == 7 || c == 15 || (c >= 250 && c <= 255) || (c >= 188 && c <= 231 && c%36 > 30)
				i += 2
			} else if atoi(ps[i+1], 0) == 2 && i+4 < len(ps) {
				lum := atoi(ps[i+2], 0) + atoi(ps[i+3], 0) + atoi(ps[i+4], 0)
				s.bg, s.light = true, lum > 3*128
				i += 4
			}
		case n == 38 && i+1 < len(ps): // foreground colours: skip their arguments
			if atoi(ps[i+1], 0) == 5 {
				i += 2
			} else if atoi(ps[i+1], 0) == 2 {
				i += 4
			}
		}
	}
}

// lines is the history and the screen, oldest first, without the blank
// rows below the cursor.
func (s *Screen) lines() [][]cell {
	out := append([][]cell(nil), s.History...)
	last := s.y
	for y := s.Rows - 1; y > s.y; y-- {
		if strings.TrimSpace(rowText(s.grid[y])) != "" || rowHasBg(s.grid[y]) {
			last = y
			break
		}
	}
	return append(out, s.grid[:last+1]...)
}

func rowText(row []cell) string {
	var b strings.Builder
	for _, c := range row {
		b.WriteRune(c.r)
	}
	return strings.TrimRight(b.String(), " ")
}

func rowHasBg(row []cell) bool {
	for _, c := range row {
		if c.bg {
			return true
		}
	}
	return false
}

// Text is everything printed so far as plain lines (QR rows included).
func (s *Screen) Text() []string {
	var out []string
	for _, row := range s.lines() {
		out = append(out, rowText(row))
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}
