package main

// ----------------- Offline QR Code Generator (pure Go, zero external services) -----------------
//
// Renders the QR symbol directly as an SVG document, so `GET /api/qr?data=...` works
// completely offline with no third-party API (previously api.qrserver.com).
//
// Implements ISO/IEC 18004 byte-mode encoding with Reed-Solomon error correction for
// versions 1..10 and error correction levels L and M (up to 271 bytes of payload),
// automatic version selection, all eight data masks with penalty scoring.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ----------------- GF(256) arithmetic for Reed-Solomon -----------------

var (
	gfExpTable [512]byte
	gfLogTable [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExpTable[i] = byte(x)
		gfLogTable[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExpTable[i] = gfExpTable[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExpTable[int(gfLogTable[a])+int(gfLogTable[b])]
}

func polyMul(a, b []byte) []byte {
	res := make([]byte, len(a)+len(b)-1)
	for i := range a {
		if a[i] == 0 {
			continue
		}
		for j := range b {
			res[i+j] ^= gfMul(a[i], b[j])
		}
	}
	return res
}

func rsGeneratorPoly(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		g = polyMul(g, []byte{1, gfExpTable[i]})
	}
	return g
}

func rsEncode(data []byte, ecLen int) []byte {
	gen := rsGeneratorPoly(ecLen)
	res := make([]byte, ecLen)
	for _, d := range data {
		factor := d ^ res[0]
		copy(res, res[1:])
		res[ecLen-1] = 0
		if factor != 0 {
			for i := 0; i < ecLen; i++ {
				res[i] ^= gfMul(gen[i+1], factor)
			}
		}
	}
	return res
}

// ----------------- Version / error-correction block tables (levels L and M) -----------------

type qrBlockSpec struct {
	ecPerBlock int
	groups     []qrGroup // consecutive groups of equal-sized blocks
}

type qrGroup struct {
	count         int
	dataCodewords int
}

// qrVersions[v-1][lvl] where lvl 0 = L, 1 = M.
var qrVersions = [10][2]qrBlockSpec{
	{{ecPerBlock: 7, groups: []qrGroup{{1, 19}}}, {ecPerBlock: 10, groups: []qrGroup{{1, 16}}}},
	{{ecPerBlock: 10, groups: []qrGroup{{1, 34}}}, {ecPerBlock: 16, groups: []qrGroup{{1, 28}}}},
	{{ecPerBlock: 15, groups: []qrGroup{{1, 55}}}, {ecPerBlock: 26, groups: []qrGroup{{1, 44}}}},
	{{ecPerBlock: 20, groups: []qrGroup{{1, 80}}}, {ecPerBlock: 18, groups: []qrGroup{{2, 32}}}},
	{{ecPerBlock: 26, groups: []qrGroup{{1, 108}}}, {ecPerBlock: 24, groups: []qrGroup{{2, 43}}}},
	{{ecPerBlock: 18, groups: []qrGroup{{2, 68}}}, {ecPerBlock: 16, groups: []qrGroup{{4, 27}}}},
	{{ecPerBlock: 20, groups: []qrGroup{{2, 78}}}, {ecPerBlock: 18, groups: []qrGroup{{4, 31}}}},
	{{ecPerBlock: 24, groups: []qrGroup{{2, 97}}}, {ecPerBlock: 22, groups: []qrGroup{{2, 38}, {2, 39}}}},
	{{ecPerBlock: 30, groups: []qrGroup{{2, 116}}}, {ecPerBlock: 22, groups: []qrGroup{{3, 36}, {2, 37}}}},
	{{ecPerBlock: 18, groups: []qrGroup{{2, 68}, {2, 69}}}, {ecPerBlock: 26, groups: []qrGroup{{4, 43}, {1, 44}}}},
}

// qrAlignmentCenters is indexed by version; version 0 is a placeholder and version 1
// has no alignment pattern at all.
var qrAlignmentCenters = [11][]int{
	{},
	{},
	{6, 18},
	{6, 22},
	{6, 26},
	{6, 30},
	{6, 34},
	{6, 22, 38},
	{6, 24, 42},
	{6, 26, 46},
	{6, 28, 50},
}

const (
	qrECLevelL = 0
	qrECLevelM = 1
)

func (spec qrBlockSpec) totalDataCodewords() int {
	total := 0
	for _, g := range spec.groups {
		total += g.count * g.dataCodewords
	}
	return total
}

// qrECFormatBits maps an internal level index to the 2-bit format value used in the symbol.
func qrECFormatBits(level int) int {
	if level == qrECLevelM {
		return 0 // M = 00
	}
	return 1 // L = 01
}

// ----------------- Bit buffer -----------------

type bitBuffer struct {
	bits []bool
}

func (b *bitBuffer) append(value int, length int) {
	for i := length - 1; i >= 0; i-- {
		b.bits = append(b.bits, (value>>uint(i))&1 == 1)
	}
}

func (b *bitBuffer) appendBytes(data []byte) {
	for _, c := range data {
		b.append(int(c), 8)
	}
}

// ----------------- Matrix -----------------

type qrMatrix struct {
	size       int
	dark       []bool
	isFunction []bool
}

func newQRMatrix(size int) *qrMatrix {
	return &qrMatrix{
		size:       size,
		dark:       make([]bool, size*size),
		isFunction: make([]bool, size*size),
	}
}

func (m *qrMatrix) set(row, col int, dark bool) {
	m.dark[row*m.size+col] = dark
	m.isFunction[row*m.size+col] = true
}

func (m *qrMatrix) setFunctionOnly(row, col int) {
	m.isFunction[row*m.size+col] = true
}

func (m *qrMatrix) isDark(row, col int) bool {
	if row < 0 || col < 0 || row >= m.size || col >= m.size {
		return false
	}
	return m.dark[row*m.size+col]
}

func (m *qrMatrix) isFunc(row, col int) bool { return m.isFunction[row*m.size+col] }

func (m *qrMatrix) placeFinder(row, col int) {
	for r := -1; r <= 7; r++ {
		for c := -1; c <= 7; c++ {
			rr, cc := row+r, col+c
			if rr < 0 || cc < 0 || rr >= m.size || cc >= m.size {
				continue
			}
			inRing := (r >= 0 && r <= 6 && (c == 0 || c == 6)) || (c >= 0 && c <= 6 && (r == 0 || r == 6))
			inCore := r >= 2 && r <= 4 && c >= 2 && c <= 4
			m.set(rr, cc, inRing || inCore)
		}
	}
}

func (m *qrMatrix) placeAlignment(row, col int) {
	for r := -2; r <= 2; r++ {
		for c := -2; c <= 2; c++ {
			onRing := r == -2 || r == 2 || c == -2 || c == 2
			m.set(row+r, col+c, onRing || (r == 0 && c == 0))
		}
	}
}

func (m *qrMatrix) placeTimingPatterns() {
	for i := 8; i < m.size-8; i++ {
		if !m.isFunc(6, i) {
			m.set(6, i, i%2 == 0)
		}
		if !m.isFunc(i, 6) {
			m.set(i, 6, i%2 == 0)
		}
	}
}

func (m *qrMatrix) reserveFormatAreas() {
	for i := 0; i < 9; i++ {
		m.setFunctionOnly(8, i)
		m.setFunctionOnly(i, 8)
	}
	for i := 0; i < 8; i++ {
		m.setFunctionOnly(m.size-1-i, 8)
		m.setFunctionOnly(8, m.size-1-i)
	}
}

func (m *qrMatrix) reserveVersionAreas() {
	if m.size < 45 {
		return
	}
	for i := 0; i < 18; i++ {
		m.setFunctionOnly(i/3, m.size-11+i%3)
		m.setFunctionOnly(m.size-11+i%3, i/3)
	}
}

// ----------------- Encoding -----------------

func qrChooseVersion(dataLen, level int) (int, error) {
	for v := 1; v <= 10; v++ {
		spec := qrVersions[v-1][level]
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		neededBits := 4 + countBits + dataLen*8
		if neededBits <= spec.totalDataCodewords()*8 {
			return v, nil
		}
	}
	return 0, errors.New("data too long for QR versions 1-10")
}

func qrBuildCodewords(data []byte, version, level int) []byte {
	spec := qrVersions[version-1][level]
	countBits := 8
	if version >= 10 {
		countBits = 16
	}

	var buf bitBuffer
	buf.append(0x4, 4) // byte mode indicator
	buf.append(len(data), countBits)
	buf.appendBytes(data)

	capacityBits := spec.totalDataCodewords() * 8
	// Terminator (up to four zero bits).
	for i := 0; i < 4 && len(buf.bits) < capacityBits; i++ {
		buf.bits = append(buf.bits, false)
	}
	// Pad to byte boundary.
	for len(buf.bits)%8 != 0 {
		buf.bits = append(buf.bits, false)
	}
	// Pad codewords.
	padBytes := []byte{0xEC, 0x11}
	pi := 0
	for len(buf.bits) < capacityBits {
		buf.append(int(padBytes[pi%2]), 8)
		pi++
	}

	dataCodewords := make([]byte, capacityBits/8)
	for i := range dataCodewords {
		var b byte
		for j := 0; j < 8; j++ {
			if buf.bits[i*8+j] {
				b |= 1 << uint(7-j)
			}
		}
		dataCodewords[i] = b
	}

	// Split into blocks, compute EC and interleave.
	var dataBlocks [][]byte
	var ecBlocks [][]byte
	pos := 0
	for _, g := range spec.groups {
		for i := 0; i < g.count; i++ {
			blk := dataCodewords[pos : pos+g.dataCodewords]
			pos += g.dataCodewords
			dataBlocks = append(dataBlocks, blk)
			ecBlocks = append(ecBlocks, rsEncode(blk, spec.ecPerBlock))
		}
	}

	out := make([]byte, 0, pos+len(ecBlocks)*spec.ecPerBlock)
	maxData := 0
	for _, b := range dataBlocks {
		if len(b) > maxData {
			maxData = len(b)
		}
	}
	for i := 0; i < maxData; i++ {
		for _, b := range dataBlocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < spec.ecPerBlock; i++ {
		for _, b := range ecBlocks {
			out = append(out, b[i])
		}
	}
	return out
}

func qrPlaceDataBits(m *qrMatrix, codewords []byte) {
	totalBits := len(codewords) * 8
	bitIdx := 0
	row := m.size - 1
	upward := true

	for col := m.size - 1; col > 0; col -= 2 {
		if col == 6 {
			col--
		}
		for {
			for dx := 0; dx < 2; dx++ {
				c := col - dx
				if m.isFunc(row, c) {
					continue
				}
				dark := false
				if bitIdx < totalBits {
					dark = (codewords[bitIdx/8]>>uint(7-bitIdx%8))&1 == 1
				}
				bitIdx++
				m.dark[row*m.size+c] = dark
			}
			if upward {
				if row == 0 {
					upward = false
					break
				}
				row--
			} else {
				if row == m.size-1 {
					upward = true
					break
				}
				row++
			}
		}
	}
}

func qrMaskCondition(mask, row, col int) bool {
	switch mask {
	case 0:
		return (row+col)%2 == 0
	case 1:
		return row%2 == 0
	case 2:
		return col%3 == 0
	case 3:
		return (row+col)%3 == 0
	case 4:
		return (row/2+col/3)%2 == 0
	case 5:
		return (row*col)%2+(row*col)%3 == 0
	case 6:
		return ((row*col)%2+(row*col)%3)%2 == 0
	default:
		return ((row+col)%2+(row*col)%3)%2 == 0
	}
}

func bchFormatInfo(data int) int {
	d := data << 10
	g := 0x537
	for i := 14; i >= 10; i-- {
		if d&(1<<uint(i)) != 0 {
			d ^= g << uint(i-10)
		}
	}
	return ((data << 10) | d) ^ 0x5412
}

func bchVersionInfo(version int) int {
	d := version << 12
	g := 0x1F25
	for i := 17; i >= 12; i-- {
		if d&(1<<uint(i)) != 0 {
			d ^= g << uint(i-12)
		}
	}
	return (version << 12) | d
}

func (m *qrMatrix) writeFormatInfo(level, mask int) {
	info := bchFormatInfo((qrECFormatBits(level) << 3) | mask)
	bit := func(i int) bool { return (info>>uint(i))&1 == 1 }

	// Vertical copy down column 8 (skipping row 6, the timing row).
	for i := 0; i <= 5; i++ {
		m.set(i, 8, bit(i))
	}
	m.set(7, 8, bit(6))
	m.set(8, 8, bit(7))
	for i := 8; i <= 14; i++ {
		m.set(m.size-15+i, 8, bit(i))
	}

	// Horizontal copy along row 8 (skipping column 6, the timing column).
	for i := 0; i <= 7; i++ {
		m.set(8, m.size-1-i, bit(i))
	}
	m.set(8, 7, bit(8))
	for i := 9; i <= 14; i++ {
		m.set(8, 14-i, bit(i))
	}
}

func (m *qrMatrix) writeVersionInfo(version int) {
	if m.size < 45 {
		return
	}
	info := bchVersionInfo(version)
	for i := 0; i < 18; i++ {
		b := (info>>uint(i))&1 == 1
		m.set(i/3, m.size-11+i%3, b)
		m.set(m.size-11+i%3, i/3, b)
	}
}

func (m *qrMatrix) scorePenalty() int {
	n := m.size
	penalty := 0

	// Rule 1: runs of five or more modules in a row/column.
	for i := 0; i < n; i++ {
		runRow, runCol := 1, 1
		for j := 1; j < n; j++ {
			if m.isDark(i, j) == m.isDark(i, j-1) {
				runRow++
			} else {
				if runRow >= 5 {
					penalty += 3 + runRow - 5
				}
				runRow = 1
			}
			if m.isDark(j, i) == m.isDark(j-1, i) {
				runCol++
			} else {
				if runCol >= 5 {
					penalty += 3 + runCol - 5
				}
				runCol = 1
			}
		}
		if runRow >= 5 {
			penalty += 3 + runRow - 5
		}
		if runCol >= 5 {
			penalty += 3 + runCol - 5
		}
	}

	// Rule 2: 2x2 blocks of the same colour.
	for r := 0; r < n-1; r++ {
		for c := 0; c < n-1; c++ {
			v := m.isDark(r, c)
			if v == m.isDark(r, c+1) && v == m.isDark(r+1, c) && v == m.isDark(r+1, c+1) {
				penalty += 3
			}
		}
	}

	// Rule 3: finder-like 1011101 patterns.
	pattern := []bool{true, false, true, true, true, false, true}
	matchAt := func(get func(int) bool, start, length int) bool {
		if start+7 > length {
			return false
		}
		for k := 0; k < 7; k++ {
			if get(start+k) != pattern[k] {
				return false
			}
		}
		// Four light modules on either side.
		before := start-4 >= 0
		for k := 1; k <= 4 && before; k++ {
			if get(start - k) {
				before = false
			}
		}
		after := start+11 <= length
		for k := 0; k < 4 && after; k++ {
			if get(start + 7 + k) {
				after = false
			}
		}
		return before || after
	}
	for i := 0; i < n; i++ {
		rowGet := func(idx int) bool { return m.isDark(i, idx) }
		colGet := func(idx int) bool { return m.isDark(idx, i) }
		for j := 0; j < n; j++ {
			if matchAt(rowGet, j, n) {
				penalty += 40
			}
			if matchAt(colGet, j, n) {
				penalty += 40
			}
		}
	}

	// Rule 4: deviation of the dark module ratio from 50%.
	dark := 0
	for _, d := range m.dark {
		if d {
			dark++
		}
	}
	total := n * n
	percent := dark * 100 / total
	deviation := percent - 50
	if deviation < 0 {
		deviation = -deviation
	}
	penalty += (deviation / 5) * 10

	return penalty
}

// qrEncode builds the final module matrix for the given payload.
func qrEncode(data []byte, level int) (*qrMatrix, error) {
	version, err := qrChooseVersion(len(data), level)
	if err != nil {
		return nil, err
	}
	codewords := qrBuildCodewords(data, version, level)

	size := 4*version + 17
	base := newQRMatrix(size)

	base.placeFinder(0, 0)
	base.placeFinder(0, size-7)
	base.placeFinder(size-7, 0)

	centers := qrAlignmentCenters[version]
	for _, r := range centers {
		for _, c := range centers {
			if (r == 6 && c == 6) || (r == 6 && c == size-7) || (r == size-7 && c == 6) {
				continue
			}
			base.placeAlignment(r, c)
		}
	}

	base.set(size-8, 8, true) // dark module
	base.placeTimingPatterns()
	base.reserveFormatAreas()
	base.reserveVersionAreas()
	qrPlaceDataBits(base, codewords)

	bestScore := -1
	var best *qrMatrix

	for mask := 0; mask < 8; mask++ {
		candidate := &qrMatrix{
			size:       size,
			dark:       make([]bool, len(base.dark)),
			isFunction: base.isFunction,
		}
		copy(candidate.dark, base.dark)
		for r := 0; r < size; r++ {
			for c := 0; c < size; c++ {
				if base.isFunc(r, c) {
					continue
				}
				if qrMaskCondition(mask, r, c) {
					candidate.dark[r*size+c] = !candidate.dark[r*size+c]
				}
			}
		}
		candidate.writeFormatInfo(level, mask)
		candidate.writeVersionInfo(version)

		score := candidate.scorePenalty()
		if bestScore == -1 || score < bestScore {
			bestScore = score
			best = candidate
		}
	}
	return best, nil
}

// ----------------- SVG rendering -----------------

func qrRenderSVG(m *qrMatrix, moduleSize, quietZone int, dark, light string) string {
	n := m.size
	dimension := (n + quietZone*2) * moduleSize

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, dimension, dimension, dimension, dimension)
	fmt.Fprintf(&sb, `<rect width="%d" height="%d" fill="%s"/>`, dimension, dimension, light)
	fmt.Fprintf(&sb, `<path fill="%s" d="`, dark)

	for row := 0; row < n; row++ {
		col := 0
		for col < n {
			if !m.isDark(row, col) {
				col++
				continue
			}
			start := col
			for col < n && m.isDark(row, col) {
				col++
			}
			x := (start + quietZone) * moduleSize
			y := (row + quietZone) * moduleSize
			run := (col - start) * moduleSize
			fmt.Fprintf(&sb, "M%d %dh%dv%dh-%dz", x, y, run, moduleSize, run)
		}
	}
	sb.WriteString(`"/></svg>`)
	return sb.String()
}

// generateQRSVG encodes data and returns a ready-to-serve SVG document.
func generateQRSVG(data string) (string, error) {
	payload := []byte(data)
	if len(payload) == 0 {
		return "", errors.New("empty data")
	}
	if len(payload) > 271 {
		return "", errors.New("data too long (max 271 bytes for QR version 10)")
	}

	// Prefer level M for better readability; fall back to L for larger payloads.
	matrix, err := qrEncode(payload, qrECLevelM)
	if err != nil {
		matrix, err = qrEncode(payload, qrECLevelL)
	}
	if err != nil {
		return "", err
	}

	moduleSize := 8
	if matrix.size >= 49 {
		moduleSize = 6
	}
	return qrRenderSVG(matrix, moduleSize, 4, "#0E0E12", "#FFFFFF"), nil
}

// handleAPIQR serves an offline QR code as SVG: GET /api/qr?data=...
func handleAPIQR(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	data := r.URL.Query().Get("data")
	if data == "" {
		data = "BigPocket"
	}
	svg, err := generateQRSVG(data)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"success":false,"error":%q}`, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(svg))
}
